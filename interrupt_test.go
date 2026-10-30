// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import "testing"

func irqCPU() *CPU {
	b := NewBus()
	b.Add("vec", vectorBase, 0x200, true)
	b.Add("ram", 0x400000, 0x2000, true)
	c := NewCPU(b)
	c.R[7] = 0x401000
	c.IntMode = 2
	return c
}

func setVec(c *CPU, vec int, target uint32) {
	c.Bus.Write32(vectorBase+uint32(vec)*4, target)
}

func TestInterruptFrameAndRte(t *testing.T) {
	c := irqCPU()
	setVec(c, 32, 0x400100)
	c.PC = 0x123456
	c.CCR = 0x05
	c.EXR = 0
	c.Raise(32, 3)
	if err := c.Step(); err != nil {
		t.Fatal(err)
	}
	if c.PC != 0x400100 {
		t.Fatalf("pc %x", c.PC)
	}
	if c.R[7] != 0x401000-6 {
		t.Fatalf("sp %x", c.R[7])
	}
	// frame: EXR, reserved, CCR, PC (24 bit)
	want := []uint8{0x00, 0x00, 0x05, 0x12, 0x34, 0x56}
	for i, w := range want {
		if g := c.Bus.Read8(c.R[7] + uint32(i)); g != w {
			t.Fatalf("frame byte %d: %02x want %02x", i, g, w)
		}
	}
	if c.EXR&7 != 3 {
		t.Fatalf("mask after accept = %d, want 3", c.EXR&7)
	}
	c.Lower(32)
	c.rte()
	if c.PC != 0x123456 || c.CCR != 0x05 || c.EXR != 0 || c.R[7] != 0x401000 {
		t.Fatalf("rte: pc=%x ccr=%x exr=%x sp=%x", c.PC, c.CCR, c.EXR, c.R[7])
	}
}

func TestInterruptPriority(t *testing.T) {
	c := irqCPU()
	setVec(c, 40, 0x400200)
	setVec(c, 44, 0x400300)
	setVec(c, 81, 0x400400)
	c.PC = 0x1000
	c.EXR = 3
	c.Raise(40, 3) // not above the mask
	c.Step()
	if c.PC == 0x400200 {
		t.Fatal("level equal to the mask must not interrupt")
	}
	c.PC = 0x1000
	c.Raise(44, 5)
	c.Raise(81, 5) // same level: lower vector first
	c.Step()
	if c.PC != 0x400300 {
		t.Fatalf("expected vector 44 first, pc=%x", c.PC)
	}
	if c.EXR&7 != 5 {
		t.Fatal("mask must become 5")
	}
	c.Lower(44)
	c.Step() // vector 81 has the same level as the mask now, so it waits
	if c.PC == 0x400400 {
		t.Fatal("same level must not nest")
	}
	c.Raise(81, 6)
	c.Step()
	if c.PC != 0x400400 {
		t.Fatalf("higher level should nest, pc=%x", c.PC)
	}
}

func TestLdcInhibitsOneInstruction(t *testing.T) {
	c := irqCPU()
	setVec(c, 32, 0x400100)
	c.PC = 0x400000
	// ldc #0x00,exr (01 41 07 00) then nop (00 00)
	for i, v := range []uint8{0x01, 0x41, 0x07, 0x00, 0x00, 0x00} {
		c.Bus.Write8(0x400000+uint32(i), v)
	}
	c.EXR = 7
	c.Raise(32, 2)
	c.Step() // executes ldc, lowering the mask to 0
	if c.EXR != 0 || c.PC != 0x400004 {
		t.Fatalf("after ldc: exr=%x pc=%x", c.EXR, c.PC)
	}
	c.Step() // the next instruction runs before the interrupt is taken
	if c.PC != 0x400006 {
		t.Fatalf("nop should execute first, pc=%x", c.PC)
	}
	c.Step()
	if c.PC != 0x400100 {
		t.Fatalf("interrupt should now be taken, pc=%x", c.PC)
	}
}

func TestSleepWakesOnInterrupt(t *testing.T) {
	c := irqCPU()
	setVec(c, 81, 0x400500)
	c.PC = 0x400000
	c.Halted = true
	c.Step()
	if !c.Halted {
		t.Fatal("must stay asleep without an interrupt")
	}
	c.Raise(81, 1)
	c.Step()
	if c.Halted || c.PC != 0x400500 {
		t.Fatalf("wake: halted=%v pc=%x", c.Halted, c.PC)
	}
}

func TestTrapa(t *testing.T) {
	c := irqCPU()
	setVec(c, 9, 0x400600)
	c.PC = 0x2222
	c.EXR = 2
	c.trapa(1)
	if c.PC != 0x400600 || c.EXR&7 != 2 {
		t.Fatalf("trapa: pc=%x exr=%x", c.PC, c.EXR)
	}
	// trapa leaves the mask alone and the saved EXR holds the old value
	if c.Bus.Read8(c.R[7]) != 0x02 {
		t.Fatalf("saved exr %x", c.Bus.Read8(c.R[7]))
	}
}

func TestMode0FrameAndMask(t *testing.T) {
	c := irqCPU()
	c.IntMode = 0
	setVec(c, 32, 0x400100)
	c.PC = 0x400800
	c.CCR = fI // masked
	c.Raise(32, 1)
	c.Step()
	if c.PC == 0x400100 {
		t.Fatal("I bit must mask interrupts in mode 0")
	}
	c.PC = 0x400800
	c.CCR = 0x05
	c.Step()
	if c.PC != 0x400100 || c.R[7] != 0x401000-4 {
		t.Fatalf("pc=%x sp=%x", c.PC, c.R[7])
	}
	for i, w := range []uint8{0x05, 0x40, 0x08, 0x00} {
		if g := c.Bus.Read8(c.R[7] + uint32(i)); g != w {
			t.Fatalf("frame byte %d: %02x want %02x", i, g, w)
		}
	}
	if !c.flag(fI) {
		t.Fatal("accepting an interrupt sets I")
	}
	c.Lower(32)
	c.rte()
	if c.PC != 0x400800 || c.CCR != 0x05 || c.R[7] != 0x401000 {
		t.Fatalf("rte: pc=%x ccr=%x sp=%x", c.PC, c.CCR, c.R[7])
	}
}
