// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"bytes"
	"os"
	"testing"
)

// parseSysEx pulls complete F0..F7 messages out of a MIDI byte stream, dropping
// real time bytes (FE active sensing and so on) that can sit between them.
func parseSysEx(stream []byte) [][]byte {
	var out [][]byte
	var cur []byte
	in := false
	for _, b := range stream {
		switch {
		case b == 0xF0:
			in, cur = true, []byte{b}
		case in && b >= 0xF8:
			// real time, ignore
		case in:
			cur = append(cur, b)
			if b == 0xF7 {
				out = append(out, cur)
				in = false
			}
		}
	}
	return out
}

// The firmware, running entirely inside the emulator, answers the toolkit's
// own SysEx: bulk mode on (echoed, flag set) then a SETUP dump request.
func TestSysExBulkModeAndSetupDump(t *testing.T) {
	if testing.Short() {
		t.Skip("boots the firmware for about 30 seconds")
	}
	m := newTestMachine(t)
	if err := m.Run(130000000); err != nil {
		t.Fatal(err)
	}
	m.MIDIOut()
	bulkOn := []byte{0xF0, 0x43, 0x10, 0x5F, 0x10, 0x00, 0x00, 0x01, 0xF7}
	m.MIDIIn(bulkOn)
	if err := m.Run(6000000); err != nil {
		t.Fatal(err)
	}
	out := m.MIDIOut()
	if !bytes.Contains(out, bulkOn) {
		t.Logf("output after bulk mode on: % X", out)
	}
	if m.Bus.Read8(0x407DAE) == 0 {
		t.Fatal("bulk mode flag at RAM 0x407DAE should be set")
	}
	m.MIDIIn([]byte{0xF0, 0x43, 0x20, 0x5F, 0x13, 0x00, 0x00, 0xF7}) // dump request SETUP
	if err := m.Run(6000000); err != nil {
		t.Fatal(err)
	}
	msgs := parseSysEx(m.MIDIOut())
	var dump []byte
	for _, x := range msgs {
		if len(x) > 8 && x[2] == 0x00 && x[3] == 0x5F {
			dump = x
		}
	}
	if dump == nil {
		t.Fatalf("no dump reply, messages: %x", msgs)
	}
	if dump[6] != 0x13 {
		t.Fatalf("address high byte %02x, want 13 (setup)", dump[6])
	}
	// checksum: two's complement of the 7 bit sum of byte count, address and data
	sum := 0
	for _, b := range dump[4 : len(dump)-2] {
		sum += int(b)
	}
	if (sum+int(dump[len(dump)-2]))&0x7f != 0 {
		t.Fatalf("bad checksum in % X", dump)
	}
	t.Logf("setup dump from the emulated unit: % X", dump)
}

// A real pattern dump (user pattern 1 of the actual unit) goes into the
// emulated unit and comes back: the track blocks and the first header blocks
// must be byte identical (padding and one derived byte legitimately differ).
func TestPatternSysExRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("boots the firmware for about 30 seconds")
	}
	data, err := os.ReadFile("../../qy100-syx/dumps/pattern01-20261026-194744-backup.syx")
	if err != nil {
		t.Skip("reference dump not available")
	}
	m := newTestMachine(t)
	if err := m.Run(130000000); err != nil {
		t.Fatal(err)
	}
	m.Keys.Press("EXIT", true)
	m.Run(3000000)
	m.Keys.Press("EXIT", false)
	m.Run(3000000)
	m.MIDIOut()
	send := func(msg []byte) []byte {
		out, err := m.SendSysEx(msg, 600000)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	send([]byte{0xF0, 0x43, 0x10, 0x5F, 0x10, 0x00, 0x00, 0x01, 0xF7})
	orig := SplitSysEx(data)
	for _, msg := range orig {
		send(msg)
	}
	var reply [][]byte
	for _, msg := range SplitSysEx(send([]byte{0xF0, 0x43, 0x20, 0x5F, 0x12, 0x00, 0x00, 0xF7})) {
		if len(msg) > 8 && msg[2] == 0x00 && msg[3] == 0x5F {
			reply = append(reply, msg)
		}
	}
	send([]byte{0xF0, 0x43, 0x10, 0x5F, 0x10, 0x00, 0x00, 0x00, 0xF7})
	if len(reply) != len(orig) {
		t.Fatalf("got %d messages back, sent %d", len(reply), len(orig))
	}
	for i := 0; i < len(orig)-2; i++ { // the last two header blocks carry padding and a derived byte
		if !bytes.Equal(reply[i], orig[i]) {
			t.Errorf("message %d differs:\n sent % X\n got  % X", i, orig[i], reply[i])
		}
	}
}
