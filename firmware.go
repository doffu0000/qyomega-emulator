// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"encoding/binary"
	"fmt"
	"os"
)

// firmwarePath is where the firmware image is read from. The program does not include
// Yamaha's firmware: the image has to come from the user's own copy of the update. It is taken
// from the QYOMEGA_FIRMWARE environment variable, else sequencer_flash.bin next to the program or
// in the folder above it.
func firmwarePath() string {
	if p := os.Getenv("QYOMEGA_FIRMWARE"); p != "" {
		return p
	}
	for _, p := range []string{"sequencer_flash.bin", "../sequencer_flash.bin", "qy100_flash_v137.bin", "../qy100_flash_v137.bin"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "sequencer_flash.bin"
}

// imageSize is the size of the flash image the emulator reads: 2 MiB, file offset == flash address.
const imageSize = 0x200000

// ImageFromUpdate builds the flash image from Yamaha's own MIDI update file for version 1.37: a
// standard MIDI file whose events are SysEx bulk blocks, 7 bit packed. A select message
// (43 10 5F 01 <bank>) says which 64 KB bank follows and each data block (43 00 5F <count> <offset>
// <packed bytes> <checksum>) carries its own offset inside the bank. Bytes the update never writes
// (the protected boot block at 0..0xFFFF) stay 0xFF. The same layout is decoded by re/build_image.py.
func ImageFromUpdate(mid []byte) ([]byte, error) {
	img := make([]byte, imageSize)
	for i := range img {
		img[i] = 0xFF
	}
	msgs, err := midiSysexMessages(mid)
	if err != nil {
		return nil, err
	}
	bank, blocks, written := -1, 0, 0
	for _, d := range msgs {
		switch {
		case len(d) >= 8 && d[0] == 0x43 && d[1] == 0x10 && d[2] == 0x5F && d[3] == 0x01:
			bank = unpack7(d[4:8])
		case len(d) >= 9 && d[0] == 0x43 && d[1] == 0x00 && d[2] == 0x5F:
			if bank < 0 {
				return nil, fmt.Errorf("data block before any bank select")
			}
			count := unpack7(d[3:5])
			off := unpack7(d[5:8])
			if len(d) < 9+count {
				return nil, fmt.Errorf("block at bank %06X offset %04X is cut short", bank, off)
			}
			sum := 0
			for _, v := range d[3 : 8+count] {
				sum += int(v)
			}
			if (-sum)&0x7F != int(d[8+count]) {
				return nil, fmt.Errorf("checksum error in the block at bank %06X offset %04X: the file is damaged", bank, off)
			}
			data := unpackBytes(d[8 : 8+count])
			if count == 0x18 { // the short final block of a bank carries 16 bytes
				data = data[:16]
			}
			a := bank + off
			if a < 0 || a+len(data) > imageSize {
				return nil, fmt.Errorf("a block lands outside the 2 MiB flash (address %06X)", a)
			}
			copy(img[a:], data)
			blocks++
			written += len(data)
		}
	}
	if blocks == 0 {
		return nil, fmt.Errorf("no update blocks found in the file")
	}
	return img, checkImage(img)
}

// unpack7 reads big endian 7 bit digits.
func unpack7(b []byte) int {
	v := 0
	for _, x := range b {
		v = v<<7 | int(x)
	}
	return v
}

// unpackBytes turns groups of 8 (seven low halves, then one byte holding their top bits) into 7 bytes.
func unpackBytes(d []byte) []byte {
	var out []byte
	for g := 0; g+8 <= len(d); g += 8 {
		msb := d[g+7]
		for k := 0; k < 7; k++ {
			out = append(out, d[g+k]|((msb>>(6-k))&1)<<7)
		}
	}
	return out
}

// midiSysexMessages returns the data of every SysEx event of a standard MIDI file, without the
// leading F0 and the closing F7.
func midiSysexMessages(b []byte) ([][]byte, error) {
	if len(b) < 14 || string(b[:4]) != "MThd" {
		return nil, fmt.Errorf("not a MIDI file")
	}
	pos := 8 + int(binary.BigEndian.Uint32(b[4:8]))
	var out [][]byte
	vlq := func(p *int) (int, error) {
		v := 0
		for i := 0; i < 4; i++ {
			if *p >= len(b) {
				return 0, fmt.Errorf("the MIDI file is cut short")
			}
			c := b[*p]
			*p++
			v = v<<7 | int(c&0x7F)
			if c&0x80 == 0 {
				return v, nil
			}
		}
		return 0, fmt.Errorf("bad MIDI delta time")
	}
	for pos+8 <= len(b) {
		id, n := string(b[pos:pos+4]), int(binary.BigEndian.Uint32(b[pos+4:pos+8]))
		pos += 8
		end := pos + n
		if end > len(b) {
			end = len(b)
		}
		if id != "MTrk" {
			pos = end
			continue
		}
		p, run := pos, byte(0)
		for p < end {
			if _, err := vlq(&p); err != nil {
				return nil, err
			}
			if p >= end {
				break
			}
			st := b[p]
			switch {
			case st == 0xFF:
				p += 2
				l, err := vlq(&p)
				if err != nil {
					return nil, err
				}
				p += l
			case st == 0xF0 || st == 0xF7:
				p++
				l, err := vlq(&p)
				if err != nil {
					return nil, err
				}
				if p+l > end {
					return nil, fmt.Errorf("the MIDI file is cut short")
				}
				d := b[p : p+l]
				if st == 0xF0 {
					if len(d) > 0 && d[len(d)-1] == 0xF7 {
						d = d[:len(d)-1]
					}
					out = append(out, d)
				}
				p += l
			default:
				if st&0x80 != 0 {
					run = st
					p++
				}
				if run>>4 == 0xC || run>>4 == 0xD {
					p++
				} else {
					p += 2
				}
			}
		}
		pos = end
	}
	return out, nil
}

// checkImage rejects a file that cannot be a version 1.37 flash image: the reset handler the
// emulator starts at (0xDCEEA) and the vector table at 0x10000 must be present.
func checkImage(img []byte) error {
	if len(img) != imageSize {
		return fmt.Errorf("the image is %d bytes, a flash image is %d (2 MiB)", len(img), imageSize)
	}
	for _, r := range [][2]int{{0x10000, 0x10010}, {0xDCEEA, 0xDCEF8}} {
		blank := true
		for _, v := range img[r[0]:r[1]] {
			if v != img[r[0]] { // all the same byte (0xFF erased, 0x00 empty) is not program code
				blank = false
			}
		}
		if blank {
			return fmt.Errorf("the image is blank where the program should be (%06X): it is not a version 1.37 firmware image", r[0])
		}
	}
	return nil
}

// NormalizeFirmware accepts either form the user may have: the 2 MiB flash image itself, or
// Yamaha's MIDI update file, and returns the 2 MiB image.
func NormalizeFirmware(data []byte) ([]byte, error) {
	if len(data) >= 4 && string(data[:4]) == "MThd" {
		return ImageFromUpdate(data)
	}
	if err := checkImage(data); err != nil {
		return nil, err
	}
	return data, nil
}
