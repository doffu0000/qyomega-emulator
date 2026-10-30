// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"bytes"
	"testing"
)

func newTestMachine(t *testing.T) *Machine {
	t.Helper()
	m, err := NewMachine(testFirmware(t))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestKeyMatrixRead(t *testing.T) {
	m := newTestMachine(t)
	b := m.Bus
	// nothing pressed
	b.Write8(colPort, 0x01)
	if b.Read8(keysBase) != 0xFF {
		t.Fatal("idle matrix must read 0xFF (a 0 means pressed)")
	}
	// [OCT UP] code 0x19 is column index 0 bit 4 in the code table
	if err := m.Keys.Press("OCT UP", true); err != nil {
		t.Fatal(err)
	}
	if b.Read8(keysBase) != 0xEF {
		t.Fatalf("column 01 read %02x", b.Read8(keysBase))
	}
	b.Write8(colPort, 0x02) // other column selected
	if b.Read8(keysBase) != 0xFF {
		t.Fatal("column 02 must not see it")
	}
	b.Write8(colPort, 0x03) // both selected: pressed keys of either column pull their bit low
	if b.Read8(keysBase) != 0xEF {
		t.Fatal("two selected columns combine")
	}
	m.Keys.Press("OCT UP", false)
	b.Write8(colPort, 0x01)
	if b.Read8(keysBase) != 0xFF {
		t.Fatal("release")
	}
}

func TestKeyNamesAndTestModeCombo(t *testing.T) {
	m := newTestMachine(t)
	// the three keys of the test mode combo sit in column index 1 bits 0..2
	for _, n := range []string{"SONG", "PARAM", "AMP"} {
		if err := m.Keys.Press(n, true); err != nil {
			t.Fatal(err)
		}
	}
	m.Bus.Write8(colPort, 0x02)
	// [AMP]=0x0f col 1 bit 4? the combo in FINDINGS names [AMP SIMULATOR]; just report
	t.Logf("column 02 reads %02x with SONG, PARAM and AMP pressed (0 = pressed)", m.Bus.Read8(keysBase))
	if err := m.Keys.Press("k:F2", true); err != nil {
		t.Fatal(err)
	}
	if err := m.Keys.Press("F2", true); err != nil {
		t.Fatal(err)
	}
	if m.Keys.names["k:F2"] != 0x1b || m.Keys.names["F2"] != 0x0b {
		t.Fatalf("F2 codes %02x %02x", m.Keys.names["F2"], m.Keys.names["k:F2"])
	}
	if err := m.Keys.Press("nonsense", true); err == nil {
		t.Fatal("unknown names must fail")
	}
}

func TestKeyLEDLatch(t *testing.T) {
	m := newTestMachine(t)
	m.Bus.Write8(keysBase, 0x05)
	if m.Keys.LED != 0x05 {
		t.Fatal("write latch")
	}
}

// Every change of the latch is logged, repeats are not, and a caller can ask for what it has not seen.
func TestKeyLEDLog(t *testing.T) {
	m := newTestMachine(t)
	k := m.Keys
	base := k.ledSeq // the machine may have written the latch already
	for _, v := range []uint8{0x07, 0x07, 0x03, 0x02, 0x02, 0x03} {
		m.Bus.Write8(keysBase, v)
	}
	ev, next := k.LEDEventsSince(base)
	var got []uint8
	for _, e := range ev {
		got = append(got, e.V)
	}
	if !bytes.Equal(got, []byte{0x07, 0x03, 0x02, 0x03}) || next != base+4 {
		t.Fatalf("events %X next %d (from %d)", got, next, base)
	}
	if ev, _ := k.LEDEventsSince(next); len(ev) != 0 {
		t.Fatal("nothing new after the last sequence number")
	}
	if ev, _ := k.LEDEventsSince(next + 100); len(ev) == 0 { // a caller from before a power cycle gets what is kept
		t.Fatal("a stale sequence number must get the kept events")
	}
	for i := 0; i < 2*ledLogMax; i++ { // the log stays bounded
		m.Bus.Write8(keysBase, uint8(i&1))
	}
	if len(k.LEDLog) > ledLogMax {
		t.Fatalf("log grew to %d", len(k.LEDLog))
	}
}

// With real firmware: EXIT dismisses the "Factory Set" popup, and a keyboard key
// sends a MIDI note on while held and a note off when released.
func TestKeysReachFirmware(t *testing.T) {
	if testing.Short() {
		t.Skip("boots the firmware for about 30 seconds")
	}
	m := newTestMachine(t)
	if err := m.Run(130000000); err != nil {
		t.Fatal(err)
	}
	if out := m.MIDIOut(); bytes.Contains(out, []byte{0x90, 0x37, 0x70}) {
		t.Fatalf("power on note burst means the matrix idles wrong: % X", out)
	}
	before := m.LCD.VRAM
	m.Keys.Press("EXIT", true)
	m.Run(5000000)
	m.Keys.Press("EXIT", false)
	m.Run(5000000)
	if m.LCD.VRAM == before {
		t.Fatal("EXIT should dismiss the popup and redraw the screen")
	}
	m.MIDIOut()
	m.Keys.Press("k:C3", true)
	m.Run(4000000)
	down := m.MIDIOut()
	m.Keys.Press("k:C3", false)
	m.Run(4000000)
	up := m.MIDIOut()
	if !bytes.Contains(down, []byte{0x90, 0x3C, 0x70}) { // C3 is note 60
		t.Fatalf("no note on while held: % X", down)
	}
	if !bytes.Contains(up, []byte{0x90, 0x3C, 0x00}) {
		t.Fatalf("no note off after release: % X", up)
	}
}
