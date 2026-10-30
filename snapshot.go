// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

// Machine snapshots. One routine describes every field of the state and runs in
// both directions (save appends to a buffer, load reads it back), so the two
// cannot drift apart. The flash and the style ROM are not stored: they come from
// the image, and a checksum of the flash guards against loading a snapshot made
// with another firmware.

const snapMagic = "QY1SNAP1"

type snapIO struct {
	save bool
	buf  []byte
	pos  int
	err  error
}

func (s *snapIO) raw(p []byte) {
	if s.err != nil {
		return
	}
	if s.save {
		s.buf = append(s.buf, p...)
		return
	}
	if s.pos+len(p) > len(s.buf) {
		s.err = io.ErrUnexpectedEOF
		return
	}
	copy(p, s.buf[s.pos:])
	s.pos += len(p)
}

func (s *snapIO) u64(p *uint64) {
	var b [8]byte
	if s.save {
		binary.LittleEndian.PutUint64(b[:], *p)
	}
	s.raw(b[:])
	if !s.save && s.err == nil {
		*p = binary.LittleEndian.Uint64(b[:])
	}
}

func (s *snapIO) u32(p *uint32) {
	v := uint64(*p)
	s.u64(&v)
	*p = uint32(v)
}

func (s *snapIO) u16(p *uint16) {
	v := uint64(*p)
	s.u64(&v)
	*p = uint16(v)
}

func (s *snapIO) u8(p *uint8) {
	v := uint64(*p)
	s.u64(&v)
	*p = uint8(v)
}

func (s *snapIO) i(p *int) {
	v := uint64(int64(*p))
	s.u64(&v)
	*p = int(int64(v))
}

func (s *snapIO) i64(p *int64) {
	v := uint64(*p)
	s.u64(&v)
	*p = int64(v)
}

func (s *snapIO) flag(p *bool) {
	var v uint8
	if *p {
		v = 1
	}
	s.u8(&v)
	*p = v != 0
}

// fixed copies a fixed size byte slice or array view.
func (s *snapIO) fixed(p []byte) { s.raw(p) }

// vary stores a byte slice of any length.
func (s *snapIO) vary(p *[]byte) {
	n := uint32(len(*p))
	s.u32(&n)
	if !s.save {
		if s.err != nil || int(n) > len(s.buf)-s.pos {
			if s.err == nil {
				s.err = io.ErrUnexpectedEOF
			}
			return
		}
		*p = make([]byte, n)
	}
	s.raw(*p)
}

// state describes the whole machine, in a fixed order.
func (m *Machine) state(s *snapIO) {
	c := m.CPU
	for i := range c.R {
		s.u32(&c.R[i])
	}
	s.u32(&c.PC)
	s.u8(&c.CCR)
	s.u8(&c.EXR)
	s.u64(&c.Cycles)
	s.flag(&c.Halted)
	s.i(&c.IntMode)
	s.i64(&c.MAC)
	s.fixed(c.pending[:])
	c.recountPending()
	s.flag(&c.irqInhibit)

	for _, r := range m.Bus.Regions {
		if r.Write {
			s.fixed(r.Data)
		}
	}

	ch := m.Chip
	s.fixed(ch.mem[:])
	for i := range ch.AD {
		s.u16(&ch.AD[i])
	}
	t := ch.tpu
	s.u8(&t.tstr)
	s.u64(&t.pending)
	for _, tc := range t.ch {
		s.fixed(tc.regs[:])
		s.u16(&tc.cnt)
		for i := range tc.tgr {
			s.u16(&tc.tgr[i])
		}
		s.u64(&tc.frac)
		tc.lastActive = lastUnknown // pending was loaded too: make the next refresh set every line again
	}
	for _, sc := range ch.sci {
		s.fixed(sc.regs[:])
		s.u8(&sc.tdr)
		s.u8(&sc.rdr)
		s.u8(&sc.ssr)
		s.u64(&sc.txLeft)
		s.u64(&sc.rxLeft)
		s.vary(&sc.rxq)
	}

	l := m.LCD
	s.fixed(l.VRAM[:])
	s.u16(&l.adp)
	s.u16(&l.TextHome)
	s.u16(&l.TextArea)
	s.u16(&l.GfxHome)
	s.u16(&l.GfxArea)
	s.u16(&l.Offset)
	s.u8(&l.Mode)
	s.u8(&l.Disp)
	s.vary(&l.params)
	s.flag(&l.autoWrite)
	s.flag(&l.autoRead)
	s.i(&l.Commands)

	k := m.Keys
	s.fixed(k.rows[:])
	s.u8(&k.LED)
	s.i(&k.Reads)
	s.i(&k.Hits)
}

func (m *Machine) flashSum() uint32 {
	return crc32.ChecksumIEEE(m.Bus.Regions[0].Data)
}

// Snapshot returns the compressed machine state.
func (m *Machine) Snapshot() ([]byte, error) {
	s := &snapIO{save: true}
	s.raw([]byte(snapMagic))
	sum := m.flashSum()
	s.u32(&sum)
	m.state(s)
	if s.err != nil {
		return nil, s.err
	}
	var out bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&out, gzip.BestSpeed)
	if _, err := zw.Write(s.buf); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Restore loads a snapshot made by Snapshot into a freshly built machine.
func (m *Machine) Restore(data []byte) error {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return err
	}
	s := &snapIO{buf: raw}
	magic := make([]byte, len(snapMagic))
	s.raw(magic)
	if s.err != nil || string(magic) != snapMagic {
		return fmt.Errorf("not an emulator snapshot")
	}
	var sum uint32
	s.u32(&sum)
	if s.err == nil && sum != m.flashSum() {
		return fmt.Errorf("snapshot was made with a different firmware image")
	}
	m.state(s)
	if s.err != nil {
		return s.err
	}
	if s.pos != len(s.buf) {
		return fmt.Errorf("snapshot has %d unread bytes", len(s.buf)-s.pos)
	}
	for _, sc := range m.Chip.sci {
		sc.TxLog = sc.TxLog[:0]
	}
	return nil
}

// SaveSnapshot writes the state to a file.
func (m *Machine) SaveSnapshot(path string) error {
	d, err := m.Snapshot()
	if err != nil {
		return err
	}
	return os.WriteFile(path, d, 0o644)
}

// LoadSnapshot reads the state from a file.
func (m *Machine) LoadSnapshot(path string) error {
	d, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return m.Restore(d)
}
