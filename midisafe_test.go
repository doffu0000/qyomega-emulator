// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"bytes"
	"testing"
)

func outBytes(f *MIDIFilter) []byte {
	msgs, _, _ := f.Since(0)
	var b []byte
	for _, m := range msgs {
		b = append(b, m.Data...)
	}
	return b
}

func TestOutFilterBlocksHarmful(t *testing.T) {
	for name, in := range map[string][]byte{
		"XG system on":        {0xF0, 0x43, 0x10, 0x4C, 0x00, 0x00, 0x7E, 0x00, 0xF7},
		"GM reset":            {0xF0, 0x7E, 0x7F, 0x09, 0x01, 0xF7},
		"bulk dump":           {0xF0, 0x43, 0x00, 0x5F, 0x00, 0x40, 0x01, 0x02, 0xF7},
		"update select":       {0xF0, 0x43, 0x10, 0x5F, 0x01, 0x00, 0x00, 0x00, 0x00, 0xF7},
		"clear all":           {0xF0, 0x43, 0x10, 0x5F, 0x11, 0x7F, 0xF7},
		"system reset":        {0xFF},
		"tune request":        {0xF6},
		"song position":       {0xF2, 0x10, 0x00},
		"song select":         {0xF3, 0x02},
		"bank select msb":     {0xB0, 0x00, 0x7F},
		"bank select lsb":     {0xB3, 0x20, 0x01},
		"nrpn":                {0xB0, 0x63, 0x01, 0xB0, 0x62, 0x02, 0xB0, 0x06, 0x40},
		"rpn":                 {0xB0, 0x65, 0x00, 0xB0, 0x64, 0x00},
		"all sound off":       {0xB0, 0x78, 0x00},
		"reset all ctrl":      {0xB1, 0x79, 0x00},
		"local control":       {0xB0, 0x7A, 0x00},
		"all notes off":       {0xB0, 0x7B, 0x00},
		"program change":      {0xC0, 0x05},
		"clock":               {0xF8, 0xFA, 0xFB, 0xFC},
		"unterminated sysex":  {0xF0, 0x43, 0x10, 0x4C, 0x00, 0x00, 0x7E, 0x00, 0x01, 0x02, 0x03},
		"sysex then stray":    {0xF0, 0x43, 0x10, 0x4C, 0x08, 0x00, 0x11, 0x7F, 0xF7, 0x40, 0x40},
		"stray data":          {0x40, 0x7F, 0x00},
		"undefined real time": {0xF9, 0xFD},
	} {
		f := NewMIDIOutFilter()
		f.Feed(in, 0)
		if got := outBytes(f); len(got) != 0 {
			t.Errorf("%s: let through % X", name, got)
		}
	}
}

func TestOutFilterPassesPerformance(t *testing.T) {
	f := NewMIDIOutFilter()
	// note on, running status note, note off, pitch bend, aftertouch, mod wheel, volume, sustain
	in := []byte{0x90, 0x3C, 0x64, 0x40, 0x64, 0x80, 0x3C, 0x00, 0xE0, 0x00, 0x40, 0xD0, 0x30, 0xB0, 0x01, 0x20, 0x07, 0x64, 0x40, 0x7F}
	f.Feed(in, 0)
	want := []byte{0x90, 0x3C, 0x64, 0x90, 0x40, 0x64, 0x80, 0x3C, 0x00, 0xE0, 0x00, 0x40, 0xD0, 0x30, 0xB0, 0x01, 0x20, 0xB0, 0x07, 0x64, 0xB0, 0x40, 0x7F}
	if got := outBytes(f); !bytes.Equal(got, want) {
		t.Fatalf("got % X\nwant % X", got, want)
	}
}

func TestOutFilterMixedAndInterleaved(t *testing.T) {
	f := NewMIDIOutFilter()
	// a note, then a SysEx with an active sensing byte inside, then a note: only the notes pass
	f.Feed([]byte{0x90, 0x3C, 0x64, 0xF0, 0x43, 0xFE, 0x10, 0xF7, 0x90, 0x3E, 0x64}, 0)
	want := []byte{0x90, 0x3C, 0x64, 0x90, 0x3E, 0x64}
	if got := outBytes(f); !bytes.Equal(got, want) {
		t.Fatalf("got % X want % X", got, want)
	}
	// the running status must not survive a system message: the data below has no status
	f2 := NewMIDIOutFilter()
	f2.Feed([]byte{0x90, 0x3C, 0x64, 0xF3, 0x02, 0x3C, 0x64}, 0)
	if got := outBytes(f2); !bytes.Equal(got, []byte{0x90, 0x3C, 0x64}) {
		t.Fatalf("running status leaked: % X", got)
	}
	// a message split over two calls
	f3 := NewMIDIOutFilter()
	f3.Feed([]byte{0x90, 0x3C}, 0)
	f3.Feed([]byte{0x64}, 0)
	if got := outBytes(f3); !bytes.Equal(got, []byte{0x90, 0x3C, 0x64}) {
		t.Fatalf("split message: % X", got)
	}
}

func TestOutFilterOptIns(t *testing.T) {
	f := NewMIDIOutFilter()
	f.Feed([]byte{0xF8, 0xC0, 0x05}, 0)
	if len(outBytes(f)) != 0 {
		t.Fatal("clock and program change must start blocked")
	}
	f.SetPolicy(MIDIPolicy{Transport: true, ProgramChange: true})
	f.Feed([]byte{0xF8, 0xFA, 0xC0, 0x05}, 0)
	if got := outBytes(f); !bytes.Equal(got, []byte{0xF8, 0xFA, 0xC0, 0x05}) {
		t.Fatalf("opted in: % X", got)
	}
	// opting in never lets SysEx or resets through
	f.Feed([]byte{0xF0, 0x7E, 0x7F, 0x09, 0x01, 0xF7, 0xFF, 0xB0, 0x00, 0x01}, 0)
	if got := outBytes(f); len(got) != 4 {
		t.Fatalf("an opt in let something harmful through: % X", got)
	}
}

func TestOutFilterCountsBlocked(t *testing.T) {
	f := NewMIDIOutFilter()
	f.Feed([]byte{0xF0, 0x43, 0xF7, 0xB0, 0x00, 0x01, 0xB0, 0x78, 0x00, 0xFF}, 0)
	_, _, blocked := f.Since(0)
	if blocked[kindSysEx] != 1 || blocked[kindBank] != 1 || blocked[kindMode] != 1 || blocked[kindReset] != 1 {
		t.Fatalf("%v", blocked)
	}
}

func TestInFilterDropsSysExAndSystem(t *testing.T) {
	f := NewMIDIInFilter()
	f.Feed([]byte{0x90, 0x3C, 0x64, 0xF0, 0x43, 0x10, 0xF7, 0xFF, 0xB0, 0x00, 0x01, 0xC0, 0x02, 0xF8}, 0)
	want := []byte{0x90, 0x3C, 0x64, 0xB0, 0x00, 0x01, 0xC0, 0x02, 0xF8}
	if got := outBytes(f); !bytes.Equal(got, want) {
		t.Fatalf("got % X want % X", got, want)
	}
}

// What the real firmware sends, from a cold start and after a factory reset, must not get through
// the output filter in any harmful form. This boots the unit, so it only runs in the full test run.
func TestFirmwareOutputIsFilteredAfterFactoryReset(t *testing.T) {
	if testing.Short() {
		t.Skip("boots the firmware")
	}
	m := newTestMachine(t)
	raw := 0
	f := NewMIDIOutFilter()
	for round := 0; round < 2; round++ { // the second round is a factory reset: blank memory
		if round == 1 {
			if err := m.PowerCycle(nil); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 8; i++ {
			if err := m.Run(20000000); err != nil {
				t.Fatal(err)
			}
			sc := m.Chip.sci[1]
			raw += len(sc.TxFresh)
			f.Feed(sc.TxFresh, 0)
			sc.TxFresh = sc.TxFresh[:0]
		}
	}
	t.Logf("firmware sent %d bytes on MIDI OUT, %d bytes passed the filter", raw, len(outBytes(f)))
	msgs, _, _ := f.Since(0)
	for _, m := range msgs {
		st := m.Data[0]
		switch st & 0xF0 {
		case 0x80, 0x90, 0xA0, 0xD0, 0xE0:
		case 0xB0:
			if d := m.Data[1]; d == 0 || d == 32 || d == 6 || d == 38 || (d >= 96 && d <= 101) || d >= 120 {
				t.Errorf("harmful controller passed: % X", m.Data)
			}
		default:
			t.Errorf("message type passed that should be blocked: % X", m.Data)
		}
	}
}
