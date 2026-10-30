// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Panel key matrix. The firmware drives one column at a time through the output
// port 0xFFE029 (one hot value from the table at flash 0x124AB8, inverse on
// 0xFFE02B) and reads the row byte from 0xA00000, where a 0 means pressed (an idle
// matrix reads 0xFF; reading 0 makes the firmware believe every key is held and it
// sends a note on for each keyboard key at power on).
// Key code = table[column*8 + bit], the 8 x 8 table at flash 0x124AC3.
// Writes to 0xA00000 go to a latch (the scanner code at 0x31D8A ORs and masks
// 3 bits into it, so these are probably indicator LEDs).

const (
	keysBase   = 0xA00000
	colPort    = 0xFFE029
	colTable   = 0x124AB8
	codeTable  = 0x124AC3
	nameTable  = 0x15A440
	numColumns = 7
)

// Keys is the key matrix device.
type Keys struct {
	bus   *Bus
	rows  [8]uint8 // pressed row bits per column
	codes [64]uint8
	names map[string]uint8
	LED   uint8 // last value written to 0xA00000
	// recent changes of LED, newest last; ledSeq counts every change ever made
	LEDLog []LEDEvent
	ledSeq int

	Reads, Hits int // reads of the row latch, and reads that returned a pressed key
}

// AttachKeys maps the matrix on the bus.
func AttachKeys(b *Bus) *Keys {
	k := &Keys{bus: b, names: map[string]uint8{}}
	for i := range k.codes {
		k.codes[i] = b.Read8(codeTable + uint32(i))
	}
	// Panel key names with their key codes; entries from the first keyboard
	// note on (index 27, [E2]) are the keyboard and get a "k:" prefix. The factory
	// key test (0x106C30) compares the pressed code with the word at 0x15A43E + 8*i,
	// so the code for name i is the last byte of the entry before it.
	for i := 0; i < 52; i++ {
		a := uint32(nameTable + i*8)
		ptr := b.Read32(a)
		var sb strings.Builder
		for p := ptr; b.Read8(p) != 0 && sb.Len() < 16; p++ {
			sb.WriteByte(b.Read8(p))
		}
		name := strings.Trim(sb.String(), "[]")
		if i >= 27 {
			name = "k:" + name
		}
		k.names[name] = b.Read8(a - 1)
	}
	b.Devs = append(b.Devs, &Dev{Name: "keys", Base: keysBase, Sz: 0x10000,
		R: func(a uint32, size int) uint32 { return k.read() * 0x01010101 & sizeMask(size) },
		W: func(a uint32, size int, v uint32) { k.setLED(uint8(v >> (8 * uint(size-1)))) },
	})
	return k
}

// LEDEvent is one change of the indicator latch, stamped with the wall clock (the emulator is paced to
// real time, so this is also where the unit's own time is).
type LEDEvent struct {
	At time.Time
	V  uint8
}

const ledLogMax = 512

func (k *Keys) setLED(v uint8) {
	if v == k.LED && k.ledSeq > 0 {
		return
	}
	k.LED = v
	k.ledSeq++
	if len(k.LEDLog) == ledLogMax {
		copy(k.LEDLog, k.LEDLog[ledLogMax/2:])
		k.LEDLog = k.LEDLog[:ledLogMax/2]
	}
	k.LEDLog = append(k.LEDLog, LEDEvent{time.Now(), v})
}

// LEDEventsSince returns the changes after sequence number since (the count of changes seen so far by
// the caller) and the new sequence number. A caller from before a power cycle, whose count is ahead of
// this latch's, gets everything that is kept.
func (k *Keys) LEDEventsSince(since int) ([]LEDEvent, int) {
	if since > k.ledSeq || since < 0 {
		since = k.ledSeq - len(k.LEDLog)
	}
	first := k.ledSeq - len(k.LEDLog) // sequence number of LEDLog[0]
	if since < first {
		since = first
	}
	return append([]LEDEvent(nil), k.LEDLog[since-first:]...), k.ledSeq
}

func (k *Keys) read() uint32 {
	sel := k.bus.Read8(colPort)
	var v uint8
	for c := 0; c < numColumns; c++ {
		if sel&(1<<uint(c)) != 0 {
			v |= k.rows[c]
		}
	}
	k.Reads++
	if v != 0 { // v is the pressed mask here
		k.Hits++
	}
	return uint32(^v) // pressed reads as 0, released as 1
}

// locate finds the matrix position of a key code.
func (k *Keys) locate(code uint8) (col, bit int, ok bool) {
	for i, c := range k.codes {
		if c == code && i/8 < numColumns {
			return i / 8, i % 8, true
		}
	}
	return 0, 0, false
}

// PressCode presses or releases a key by its code.
func (k *Keys) PressCode(code uint8, down bool) error {
	col, bit, ok := k.locate(code)
	if !ok {
		return fmt.Errorf("key code %02X is not in the matrix", code)
	}
	if down {
		k.rows[col] |= 1 << uint(bit)
	} else {
		k.rows[col] &^= 1 << uint(bit)
	}
	return nil
}

// Press presses or releases a key by name, e.g. "PLAY", "F2" (the function key)
// or "k:F2" (the keyboard note). Names are matched without brackets, ignoring case.
func (k *Keys) Press(name string, down bool) error {
	if strings.HasPrefix(name, "code:") { // raw key code in hex, e.g. code:06
		var code uint8
		if _, err := fmt.Sscanf(name[5:], "%x", &code); err != nil {
			return err
		}
		return k.PressCode(code, down)
	}
	for n, code := range k.names {
		if strings.EqualFold(n, name) {
			return k.PressCode(code, down)
		}
	}
	return fmt.Errorf("unknown key %q", name)
}

// Names lists the known key names, sorted.
func (k *Keys) Names() []string {
	var out []string
	for n := range k.names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Has reports whether a key name (or a code:XX form that is in the matrix) is known.
func (k *Keys) Has(name string) bool {
	if strings.HasPrefix(name, "code:") {
		var code uint8
		if _, err := fmt.Sscanf(name[5:], "%x", &code); err != nil {
			return false
		}
		_, _, ok := k.locate(code)
		return ok
	}
	for n := range k.names {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}
