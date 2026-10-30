// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import "testing"

func TestTPU0CompareMatchClear(t *testing.T) {
	c, ch := chipCPU()
	c.Bus.Write8(0xFFFFD0, 0x21) // CCLR = TGRA, phi/4
	c.Bus.Write16(0xFFFFD8, 99)  // TGRA: period 100 ticks = 400 clocks
	c.Bus.Write8(0xFFFFD4, 0x01) // TGIEA
	c.Bus.Write8(regTSTR, 0x01)
	ch.Advance(399)
	ch.flushTPU()
	if c.Bus.Read8(0xFFFFD5)&1 != 0 {
		t.Fatal("flag set too early")
	}
	ch.Advance(1)
	ch.flushTPU()
	if c.Bus.Read8(0xFFFFD5)&1 == 0 {
		t.Fatal("TGFA should be set after 400 clocks")
	}
	if c.pending[32] == 0 {
		t.Fatal("vector 32 should be pending")
	}
	if c.Bus.Read16(0xFFFFD6) != 0 {
		t.Fatalf("TCNT should have been cleared, got %d", c.Bus.Read16(0xFFFFD6))
	}
	c.Bus.Write8(0xFFFFD5, 0xFE) // clear TGFA
	if c.pending[32] != 0 {
		t.Fatal("clearing the flag must lower the request")
	}
	// the next period
	ch.Advance(400)
	ch.flushTPU()
	if c.pending[32] == 0 {
		t.Fatal("second period should fire again")
	}
}

func TestTPUFirmwareSetting(t *testing.T) {
	// TGRA = 0x2112 at phi/4 must be 2.000 ms at 16.9344 MHz
	period := float64(0x2112+1) * 4 / 16934400
	if period < 0.0019999 || period > 0.0020001 {
		t.Fatalf("period %v", period)
	}
}

func TestTPUOverflowAndStop(t *testing.T) {
	c, ch := chipCPU()
	c.Bus.Write8(0xFFFFE0, 0x00) // TPU1, phi/1, no clear
	c.Bus.Write16(0xFFFFE6, 0xFFF0)
	c.Bus.Write8(0xFFFFE4, 0x10) // TCIEV
	ch.Advance(15)
	ch.flushTPU()
	if c.Bus.Read16(0xFFFFE6) != 0xFFF0 {
		t.Fatal("stopped counter must not run")
	}
	c.Bus.Write8(regTSTR, 0x02)
	ch.Advance(16)
	ch.flushTPU()
	if c.Bus.Read8(0xFFFFE5)&0x10 == 0 || c.pending[42] == 0 {
		t.Fatal("overflow flag and vector 42 expected")
	}
	if c.Bus.Read16(0xFFFFE6) != 0 {
		t.Fatalf("TCNT after wrap = %d", c.Bus.Read16(0xFFFFE6))
	}
}

func TestTPUCompareWithoutClear(t *testing.T) {
	c, ch := chipCPU()
	c.Bus.Write8(0xFFFFF0, 0x00) // TPU2 phi/1
	c.Bus.Write16(0xFFFFFA, 10)  // TGRB
	c.Bus.Write8(regTSTR, 0x04)
	ch.Advance(10)
	ch.flushTPU()
	if c.Bus.Read8(0xFFFFF5)&2 == 0 {
		t.Fatal("TGFB expected at count 10")
	}
	if c.Bus.Read16(0xFFFFF6) != 10 {
		t.Fatal("counter keeps running without clear")
	}
}

// With the timer running, the real firmware must move past the idle loop.
func TestBootGetsTimerInterrupts(t *testing.T) {
	b, err := NewUnitBus(testFirmware(t))
	if err != nil {
		t.Fatal(err)
	}
	c := NewCPU(b)
	AttachChip(c)
	c.PC = 0xDCEEA
	c.R[7] = 0xFFF45A
	taken := 0
	for n := 0; n < 30000000; n++ {
		if c.PC == 0xE0A16 { // TPU0 compare match A handler
			taken++
			if taken == 3 {
				return
			}
		}
		if err := c.Step(); err != nil {
			t.Fatalf("after %d: %v (pc=%06x)", n, err, c.PC)
		}
	}
	t.Fatalf("timer interrupt handler reached %d times", taken)
}
