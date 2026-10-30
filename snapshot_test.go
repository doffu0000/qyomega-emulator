// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"bytes"
	"hash/crc32"
	"testing"
)

func machineSum(m *Machine) uint32 {
	h := crc32.NewIEEE()
	for _, r := range m.Bus.Regions {
		if r.Write {
			h.Write(r.Data)
		}
	}
	h.Write(m.LCD.VRAM[:])
	return h.Sum32()
}

// A restored machine must continue exactly like the original.
func TestSnapshotContinuesIdentically(t *testing.T) {
	a := newTestMachine(t)
	if err := a.Run(2500000); err != nil {
		t.Fatal(err)
	}
	a.MIDIIn([]byte{0xFE, 0xFE}) // leave something in the serial receive queue
	if err := a.Keys.Press("EXIT", true); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(300000); err != nil {
		t.Fatal(err)
	}
	snap, err := a.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("snapshot is %d bytes", len(snap))

	b := newTestMachine(t)
	if err := b.Restore(snap); err != nil {
		t.Fatal(err)
	}
	if machineSum(a) != machineSum(b) || a.CPU.PC != b.CPU.PC || a.CPU.R != b.CPU.R {
		t.Fatal("state differs right after restore")
	}
	for round := 0; round < 4; round++ {
		if err := a.Run(400000); err != nil {
			t.Fatal(err)
		}
		if err := b.Run(400000); err != nil {
			t.Fatal(err)
		}
		if a.CPU.PC != b.CPU.PC || a.CPU.R != b.CPU.R || a.CPU.CCR != b.CPU.CCR || a.CPU.Cycles != b.CPU.Cycles {
			t.Fatalf("round %d: cpu differs: pc %06X/%06X", round, a.CPU.PC, b.CPU.PC)
		}
		if machineSum(a) != machineSum(b) {
			t.Fatalf("round %d: memory or display differs", round)
		}
		if !bytes.Equal(a.MIDIOut(), b.MIDIOut()) {
			t.Fatalf("round %d: midi out differs", round)
		}
	}
}

func TestSnapshotRejectsGarbage(t *testing.T) {
	m := newTestMachine(t)
	if err := m.Restore([]byte("not a snapshot")); err == nil {
		t.Fatal("garbage must be refused")
	}
	snap, err := m.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Restore(snap[:len(snap)/2]); err == nil {
		t.Fatal("a truncated snapshot must be refused")
	}
}
