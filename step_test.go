// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import "testing"

func r8(n int) Opnd     { return Opnd{Mode: mReg8, Reg: n} }
func r16(n int) Opnd    { return Opnd{Mode: mReg16, Reg: n} }
func r32(n int) Opnd    { return Opnd{Mode: mReg32, Reg: n} }
func imm(v uint32) Opnd { return Opnd{Mode: mImm, Val: v} }

func run(t *testing.T, c *CPU, name string, a, b Opnd) {
	t.Helper()
	if err := c.exec(&Inst{Name: name, A: a, B: b}); err != nil {
		t.Fatal(err)
	}
}

func flags(c *CPU) string {
	s := ""
	for _, f := range []struct {
		m uint8
		n string
	}{{fH, "H"}, {fN, "N"}, {fZ, "Z"}, {fV, "V"}, {fC, "C"}} {
		if c.flag(f.m) {
			s += f.n
		}
	}
	return s
}

func TestAddSubFlags(t *testing.T) {
	cases := []struct {
		name string
		d, s uint32
		res  uint32
		want string
		sz   int
	}{
		{"add.b", 0x7f, 1, 0x80, "HNV", 1},
		{"add.b", 0xff, 1, 0x00, "HZC", 1},
		{"add.b", 0x10, 0x20, 0x30, "", 1},
		{"add.w", 0x7fff, 1, 0x8000, "HNV", 2},
		{"add.l", 0xffffffff, 2, 1, "HC", 4},
		{"sub.b", 0x00, 1, 0xff, "HNC", 1},
		{"sub.b", 0x80, 1, 0x7f, "HV", 1},
		{"sub.b", 0x05, 0x05, 0x00, "Z", 1},
		{"sub.w", 0x0000, 0xffff, 0x0001, "HC", 2},
		{"sub.l", 0x80000000, 1, 0x7fffffff, "HV", 4},
	}
	for _, k := range cases {
		c := testCPU()
		var d, s Opnd
		switch k.sz {
		case 1:
			d, s = r8(8), imm(k.s)
		case 2:
			d, s = r16(0), imm(k.s)
		default:
			d, s = r32(0), imm(k.s)
		}
		c.Write(d, k.sz, k.d)
		c.CCR = 0
		run(t, c, k.name, s, d)
		got := c.Read(d, k.sz)
		if got != k.res || flags(c) != k.want {
			t.Errorf("%s %x,%x: got %x %q want %x %q", k.name, k.d, k.s, got, flags(c), k.res, k.want)
		}
	}
}

func TestCmpDoesNotStore(t *testing.T) {
	c := testCPU()
	c.R[0] = 5
	run(t, c, "cmp.l", imm(5), r32(0))
	if c.R[0] != 5 || flags(c) != "Z" {
		t.Fatalf("cmp: er0=%x flags=%q", c.R[0], flags(c))
	}
}

func TestAddxSubxZ(t *testing.T) {
	c := testCPU()
	c.SetReg8(8, 0xff)
	c.CCR = fZ | fC
	run(t, c, "addx.b", imm(0), r8(8)) // 0xff + 0 + 1 = 0 carry
	if c.GetReg8(8) != 0 || !c.flag(fZ) || !c.flag(fC) {
		t.Fatalf("addx wrap: %q", flags(c))
	}
	c.SetReg8(8, 1)
	c.CCR = fZ // carry clear, result nonzero clears Z
	run(t, c, "addx.b", imm(1), r8(8))
	if c.flag(fZ) {
		t.Fatal("addx must clear Z on nonzero")
	}
	c.SetReg8(8, 0)
	c.CCR = fZ
	run(t, c, "subx.b", imm(0), r8(8)) // zero result leaves Z alone
	if !c.flag(fZ) {
		t.Fatal("subx must leave Z when result is zero")
	}
}

func TestNegIncDecAddsSubs(t *testing.T) {
	c := testCPU()
	c.SetReg8(8, 0x80)
	c.CCR = 0
	run(t, c, "neg.b", r8(8), Opnd{})
	if c.GetReg8(8) != 0x80 || flags(c) != "NVC" {
		t.Fatalf("neg.b 0x80: %x %q", c.GetReg8(8), flags(c))
	}
	c.SetReg16(0, 0x7fff)
	c.CCR = fC
	run(t, c, "inc.w", imm(1), r16(0)) // C untouched, V set
	if c.GetReg16(0) != 0x8000 || flags(c) != "NVC" {
		t.Fatalf("inc.w: %x %q", c.GetReg16(0), flags(c))
	}
	c.R[1] = 0
	c.CCR = 0
	run(t, c, "dec.l", imm(2), r32(1))
	if c.R[1] != 0xfffffffe || flags(c) != "N" {
		t.Fatalf("dec.l: %x %q", c.R[1], flags(c))
	}
	c.R[2] = 0xfffffffc
	c.CCR = 0
	run(t, c, "adds.l", imm(4), r32(2)) // no flags at all
	if c.R[2] != 0 || c.CCR != 0 {
		t.Fatalf("adds: %x %x", c.R[2], c.CCR)
	}
	run(t, c, "subs.l", imm(1), r32(2))
	if c.R[2] != 0xffffffff || c.CCR != 0 {
		t.Fatalf("subs: %x", c.R[2])
	}
}

func TestExt(t *testing.T) {
	c := testCPU()
	c.SetReg16(0, 0x1280)
	run(t, c, "exts.w", r16(0), Opnd{})
	if c.GetReg16(0) != 0xff80 || !c.flag(fN) {
		t.Fatalf("exts.w %x", c.GetReg16(0))
	}
	c.R[1] = 0xabcd1234
	run(t, c, "extu.l", r32(1), Opnd{})
	if c.R[1] != 0x1234 {
		t.Fatalf("extu.l %x", c.R[1])
	}
	c.R[2] = 0xabcd9234
	run(t, c, "exts.l", r32(2), Opnd{})
	if c.R[2] != 0xffff9234 {
		t.Fatalf("exts.l %x", c.R[2])
	}
	c.SetReg16(3, 0x55ff)
	run(t, c, "extu.w", r16(3), Opnd{})
	if c.GetReg16(3) != 0x00ff {
		t.Fatalf("extu.w %x", c.GetReg16(3))
	}
}

func TestMulDiv(t *testing.T) {
	c := testCPU()
	c.SetReg16(0, 0x0010)
	c.SetReg8(1, 0x20) // r1h
	run(t, c, "mulxu.b", r8(1), r16(0))
	if c.GetReg16(0) != 0x0200 {
		t.Fatalf("mulxu.b %x", c.GetReg16(0))
	}
	c.R[0] = 0xffff0100
	c.SetReg16(1, 0x0300)
	run(t, c, "mulxu.w", r16(1), r32(0))
	if c.R[0] != 0x00030000 {
		t.Fatalf("mulxu.w %x", c.R[0])
	}
	// the 16 bit truncation seen in the firmware: 35 bars * 1920 clocks
	c.R[0] = 35
	c.SetReg16(1, 1920)
	run(t, c, "mulxu.w", r16(1), r32(0))
	run(t, c, "extu.l", r32(0), Opnd{})
	if c.R[0] != 1664 {
		t.Fatalf("35*1920 truncated: got %d want 1664", c.R[0])
	}
	// divxu.b: 0x0107 / 0x10 = 16 rem 7
	c.SetReg16(0, 0x0107)
	c.SetReg8(1, 0x10)
	run(t, c, "divxu.b", r8(1), r16(0))
	if c.GetReg16(0) != 0x0710 {
		t.Fatalf("divxu.b %x", c.GetReg16(0))
	}
	// divxu.w: 100000 / 7 = 14285 rem 5
	c.R[2] = 100000
	c.SetReg16(3, 7)
	run(t, c, "divxu.w", r16(3), r32(2))
	if c.R[2] != 5<<16|14285 {
		t.Fatalf("divxu.w %x", c.R[2])
	}
	// divide by zero leaves it and sets Z
	c.SetReg16(0, 0x1234)
	c.SetReg8(1, 0)
	run(t, c, "divxu.b", r8(1), r16(0))
	if c.GetReg16(0) != 0x1234 || !c.flag(fZ) {
		t.Fatal("div by zero")
	}
	// signed
	c.SetReg16(0, 0xfff6) // -10
	c.SetReg8(1, 3)
	run(t, c, "divxs.b", r8(1), r16(0)) // q -3 r -1
	if c.GetReg16(0) != 0xfffd || !c.flag(fN) {
		t.Fatalf("divxs.b %x", c.GetReg16(0))
	}
	c.SetReg16(0, 0x00fe) // low byte -2
	c.SetReg8(1, 5)
	run(t, c, "mulxs.b", r8(1), r16(0))
	if c.GetReg16(0) != 0xfff6 || !c.flag(fN) {
		t.Fatalf("mulxs.b %x", c.GetReg16(0))
	}
}

func TestMovAndStm(t *testing.T) {
	c := testCPU()
	c.R[7] = 0x400800
	c.R[1], c.R[2], c.R[3] = 0x11111111, 0x22222222, 0x33333333
	run(t, c, "stm.l", Opnd{Mode: mRegs, Reg: 1, Reg2: 3}, Opnd{Mode: mPush})
	if c.R[7] != 0x400800-12 || c.Bus.Read32(c.R[7]) != 0x33333333 {
		t.Fatalf("stm: sp=%x top=%x", c.R[7], c.Bus.Read32(c.R[7]))
	}
	c.R[1], c.R[2], c.R[3] = 0, 0, 0
	run(t, c, "ldm.l", Opnd{Mode: mPop}, Opnd{Mode: mRegs, Reg: 1, Reg2: 3})
	if c.R[1] != 0x11111111 || c.R[3] != 0x33333333 || c.R[7] != 0x400800 {
		t.Fatalf("ldm: %x %x sp=%x", c.R[1], c.R[3], c.R[7])
	}
	c.R[0] = 0x400100
	run(t, c, "mov.w", imm(0x8001), Opnd{Mode: mInd, Reg: 0})
	if c.Bus.Read16(0x400100) != 0x8001 || flags(c) != "N" {
		t.Fatalf("mov.w: %x %q", c.Bus.Read16(0x400100), flags(c))
	}
	run(t, c, "mov.b", Opnd{Mode: mInd, Reg: 0}, r8(9))
	if c.GetReg8(9) != 0x80 {
		t.Fatal("mov.b load")
	}
}
