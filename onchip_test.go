// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import "testing"

func chipCPU() (*CPU, *Chip) {
	b := NewBus()
	b.Add("ram", 0x400000, 0x1000, true)
	c := NewCPU(b)
	return c, AttachChip(c)
}

func TestADSingleConversion(t *testing.T) {
	c, ch := chipCPU()
	ch.AD[3] = 0x2A5
	c.Bus.Write8(regADCSR, 0x00)
	c.Bus.Write8(regADCSR, 0x23) // ADST, AN3
	if c.Bus.Read8(regADCSR)&0x80 == 0 {
		t.Fatal("ADF must be set after the conversion")
	}
	if c.Bus.Read8(regADCSR)&0x20 != 0 {
		t.Fatal("ADST must clear itself in single mode")
	}
	// ADDRD at 0xFFFF96, 10 bits in the upper bits
	if got := c.Bus.Read16(regADDRA+6) >> 6; got != 0x2A5 {
		t.Fatalf("ADDRD = %x", got)
	}
	// ADF is cleared by writing 0, and cannot be set by a write
	c.Bus.Write8(regADCSR, 0x00)
	if c.Bus.Read8(regADCSR)&0x80 != 0 {
		t.Fatal("ADF must clear")
	}
	c.Bus.Write8(regADCSR, 0x80)
	if c.Bus.Read8(regADCSR)&0x80 != 0 {
		t.Fatal("ADF must not be settable by software")
	}
}

func TestADScanAndInterrupt(t *testing.T) {
	c, ch := chipCPU()
	for i := range ch.AD {
		ch.AD[i] = uint16(0x100 + i)
	}
	c.Bus.Write8(regADCSR, 0x40|0x20|0x10|0x02) // ADIE, ADST, scan AN0..AN2
	for i := 0; i < 3; i++ {
		if got := c.Bus.Read16(regADDRA+uint32(i)*2) >> 6; got != uint16(0x100+i) {
			t.Fatalf("scan AN%d = %x", i, got)
		}
	}
	if c.pending[28] == 0 {
		t.Fatal("ADI should be pending")
	}
	c.Bus.Write8(regADCSR, 0x00)
	if c.pending[28] != 0 {
		t.Fatal("clearing ADF must lower ADI")
	}
}

func TestUnmodelledRegistersAreLogged(t *testing.T) {
	c, ch := chipCPU()
	c.Bus.Write8(0xFFFF30, 0x5A)
	if c.Bus.Read8(0xFFFF30) != 0x5A {
		t.Fatal("unmodelled registers keep what was written")
	}
	log := ch.Unmodelled()
	if len(log) != 2 || log[0].Addr != 0xFFFF30 {
		t.Fatalf("log: %+v", log)
	}
}

func TestBootPassesADWait(t *testing.T) {
	b, err := NewUnitBus(testFirmware(t))
	if err != nil {
		t.Fatal(err)
	}
	c := NewCPU(b)
	AttachChip(c)
	c.PC = 0xDCEEA
	c.R[7] = 0xFFF45A
	passed := false
	for n := 0; n < 40000000; n++ {
		if c.PC == 0x105EC2 { // the instruction after the ADF poll loop
			passed = true
			break
		}
		if err := c.Step(); err != nil {
			t.Fatalf("after %d instructions: %v", n, err)
		}
	}
	if !passed {
		t.Fatal("never left the A/D wait or never reached it")
	}
}
