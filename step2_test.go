// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import "testing"

func TestLogic(t *testing.T) {
	c := testCPU()
	c.SetReg8(8, 0xf0)
	c.CCR = fV | fC
	run(t, c, "and.b", imm(0x8f), r8(8))
	if c.GetReg8(8) != 0x80 || flags(c) != "NC" {
		t.Fatalf("and.b %x %q", c.GetReg8(8), flags(c))
	}
	run(t, c, "or.b", imm(0x01), r8(8))
	run(t, c, "xor.b", imm(0x81), r8(8))
	if c.GetReg8(8) != 0 || !c.flag(fZ) {
		t.Fatalf("or/xor %x %q", c.GetReg8(8), flags(c))
	}
	c.R[0] = 0x0000ffff
	run(t, c, "xor.l", imm(0xffffffff), r32(0))
	if c.R[0] != 0xffff0000 || !c.flag(fN) {
		t.Fatalf("xor.l %x", c.R[0])
	}
	run(t, c, "not.l", r32(0), Opnd{})
	if c.R[0] != 0x0000ffff || c.flag(fN) {
		t.Fatalf("not.l %x", c.R[0])
	}
}

func TestShifts(t *testing.T) {
	c := testCPU()
	c.SetReg8(8, 0x81)
	run(t, c, "shll.b", r8(8), Opnd{}) // 0x02, C=1
	if c.GetReg8(8) != 0x02 || flags(c) != "C" {
		t.Fatalf("shll %x %q", c.GetReg8(8), flags(c))
	}
	c.SetReg8(8, 0x40)
	run(t, c, "shal.b", r8(8), Opnd{}) // 0x80 sign change -> V
	if c.GetReg8(8) != 0x80 || flags(c) != "NV" {
		t.Fatalf("shal %x %q", c.GetReg8(8), flags(c))
	}
	c.SetReg16(0, 0x8001)
	run(t, c, "shar.w", r16(0), Opnd{}) // 0xc000, C=1
	if c.GetReg16(0) != 0xc000 || flags(c) != "NC" {
		t.Fatalf("shar %x %q", c.GetReg16(0), flags(c))
	}
	c.R[1] = 0x00000005
	run(t, c, "shlr.l", imm(2), r32(1)) // by two: 5 -> 2 -> 1, C=0 on last step
	if c.R[1] != 1 || c.flag(fC) {
		t.Fatalf("shlr by 2: %x %q", c.R[1], flags(c))
	}
	c.SetReg8(8, 0x80)
	c.CCR = 0
	run(t, c, "rotl.b", r8(8), Opnd{})
	if c.GetReg8(8) != 0x01 || !c.flag(fC) {
		t.Fatalf("rotl %x", c.GetReg8(8))
	}
	c.SetReg8(8, 0x80)
	c.CCR = fC
	run(t, c, "rotxl.b", r8(8), Opnd{}) // carry in at bit 0, msb out
	if c.GetReg8(8) != 0x01 || !c.flag(fC) {
		t.Fatalf("rotxl %x", c.GetReg8(8))
	}
	c.SetReg8(8, 0x01)
	c.CCR = fC
	run(t, c, "rotxr.b", r8(8), Opnd{})
	if c.GetReg8(8) != 0x80 || !c.flag(fC) {
		t.Fatalf("rotxr %x", c.GetReg8(8))
	}
	c.SetReg8(8, 0x01)
	run(t, c, "rotr.b", r8(8), Opnd{})
	if c.GetReg8(8) != 0x80 || !c.flag(fC) {
		t.Fatalf("rotr %x", c.GetReg8(8))
	}
}

func TestBitOps(t *testing.T) {
	c := testCPU()
	c.Bus.Write8(0x400010, 0x00)
	mem := Opnd{Mode: mAbs, Val: 0x400010}
	run(t, c, "bset", imm(3), mem)
	if c.Bus.Read8(0x400010) != 0x08 {
		t.Fatal("bset")
	}
	run(t, c, "btst", imm(3), mem)
	if c.flag(fZ) {
		t.Fatal("btst on a set bit must clear Z")
	}
	run(t, c, "btst", imm(4), mem)
	if !c.flag(fZ) {
		t.Fatal("btst on a clear bit must set Z")
	}
	run(t, c, "bnot", imm(3), mem)
	if c.Bus.Read8(0x400010) != 0 {
		t.Fatal("bnot")
	}
	c.SetReg8(9, 0x0b) // bit number register: 0x0b & 7 = 3
	run(t, c, "bset", r8(9), mem)
	run(t, c, "bclr", imm(3), mem)
	if c.Bus.Read8(0x400010) != 0 {
		t.Fatal("bclr")
	}
	c.CCR = fC
	run(t, c, "bst", imm(7), mem)
	if c.Bus.Read8(0x400010) != 0x80 {
		t.Fatal("bst")
	}
	run(t, c, "bist", imm(7), mem) // C still 1 so bit becomes 0
	if c.Bus.Read8(0x400010) != 0 {
		t.Fatal("bist")
	}
	c.Bus.Write8(0x400010, 0x01)
	c.CCR = 0
	run(t, c, "bld", imm(0), mem)
	if !c.flag(fC) {
		t.Fatal("bld")
	}
	run(t, c, "bild", imm(0), mem)
	if c.flag(fC) {
		t.Fatal("bild")
	}
	run(t, c, "bor", imm(0), mem)
	run(t, c, "bxor", imm(0), mem) // C=1 then xor 1 -> 0
	if c.flag(fC) {
		t.Fatal("bor/bxor")
	}
}

func TestBranches(t *testing.T) {
	c := testCPU()
	cases := []struct {
		name  string
		flags uint8
		taken bool
	}{
		{"bt", 0, true}, {"bf", 0, false},
		{"beq", fZ, true}, {"bne", fZ, false},
		{"bcc", 0, true}, {"bcs", fC, true},
		{"bhi", 0, true}, {"bhi", fC, false}, {"bls", fZ, true},
		{"bpl", 0, true}, {"bmi", fN, true},
		{"bvc", 0, true}, {"bvs", fV, true},
		{"bge", fN | fV, true}, {"bge", fN, false},
		{"blt", fN, true}, {"blt", fN | fV, false},
		{"bgt", 0, true}, {"bgt", fZ, false}, {"bgt", fN, false},
		{"ble", fZ, true}, {"ble", fN, true}, {"ble", 0, false},
	}
	for _, k := range cases {
		c.PC = 0x1000
		c.CCR = k.flags
		run(t, c, k.name, Opnd{Mode: mJump, Val: 0x2000}, Opnd{})
		if (c.PC == 0x2000) != k.taken {
			t.Errorf("%s with flags %q: taken=%v want %v", k.name, flags(c), c.PC == 0x2000, k.taken)
		}
	}
}

func TestCallReturn(t *testing.T) {
	c := testCPU()
	c.R[7] = 0x400800
	c.PC = 0x1234
	run(t, c, "bsr", Opnd{Mode: mJump, Val: 0x5000}, Opnd{})
	if c.PC != 0x5000 || c.R[7] != 0x4007fc || c.Bus.Read32(c.R[7]) != 0x1234 {
		t.Fatalf("bsr: pc=%x sp=%x", c.PC, c.R[7])
	}
	run(t, c, "rts", Opnd{}, Opnd{})
	if c.PC != 0x1234 || c.R[7] != 0x400800 {
		t.Fatalf("rts: pc=%x sp=%x", c.PC, c.R[7])
	}
	c.R[3] = 0xff00abcd
	run(t, c, "jsr", r32(3), Opnd{})
	if c.PC != 0x00abcd {
		t.Fatalf("jsr @er3: %x", c.PC)
	}
}

func TestCCRMoves(t *testing.T) {
	c := testCPU()
	c.CCR = 0
	run(t, c, "orc", imm(0x80), Opnd{Mode: mCCR})
	if c.CCR != 0x80 {
		t.Fatal("orc")
	}
	run(t, c, "andc", imm(0x7f), Opnd{Mode: mCCR})
	run(t, c, "xorc", imm(0x05), Opnd{Mode: mCCR})
	if c.CCR != 0x05 {
		t.Fatalf("andc/xorc %x", c.CCR)
	}
	run(t, c, "stc", Opnd{Mode: mCCR}, r8(8))
	if c.GetReg8(8) != 0x05 {
		t.Fatal("stc to register")
	}
	c.SetReg8(9, 0xc0)
	run(t, c, "ldc", r8(9), Opnd{Mode: mEXR})
	if c.EXR != 0xc0 {
		t.Fatal("ldc to exr")
	}
	c.R[0] = 0x400100
	run(t, c, "stc.w", Opnd{Mode: mCCR}, Opnd{Mode: mInd, Reg: 0})
	if c.Bus.Read16(0x400100) != 0x0505 {
		t.Fatalf("stc.w %x", c.Bus.Read16(0x400100))
	}
	c.Bus.Write16(0x400100, 0xa500)
	run(t, c, "ldc.w", Opnd{Mode: mInd, Reg: 0}, Opnd{Mode: mCCR})
	if c.CCR != 0xa5 {
		t.Fatalf("ldc.w %x", c.CCR)
	}
}

func TestEepmovTasSleep(t *testing.T) {
	c := testCPU()
	for i := uint32(0); i < 4; i++ {
		c.Bus.Write8(0x400200+i, uint8(0x10+i))
	}
	c.R[5], c.R[6] = 0x400200, 0x400300
	c.SetReg8(12, 4) // r4l
	run(t, c, "eepmov.b", Opnd{}, Opnd{})
	if c.Bus.Read8(0x400303) != 0x13 || c.R[6] != 0x400304 || c.GetReg8(12) != 0 {
		t.Fatal("eepmov.b")
	}
	c.Bus.Write8(0x400400, 0x00)
	run(t, c, "tas.b", Opnd{Mode: mAbs, Val: 0x400400}, Opnd{})
	if c.Bus.Read8(0x400400) != 0x80 || !c.flag(fZ) {
		t.Fatal("tas")
	}
	run(t, c, "sleep", Opnd{}, Opnd{})
	if !c.Halted {
		t.Fatal("sleep")
	}
}

// Run the firmware from the reset handler (0xDCEEA) for a few million
// instructions. This only proves that every instruction reached so far is
// implemented; what the code is waiting for is the job of the later steps.
func TestRunsFromReset(t *testing.T) {
	b, err := NewUnitBus(testFirmware(t))
	if err != nil {
		t.Fatal(err)
	}
	c := NewCPU(b)
	c.PC = 0xDCEEA
	n := 0
	for ; n < 3000000; n++ {
		if in := Decode(b, c.PC); in.Name == "illegal" {
			t.Fatalf("illegal opcode at %06x after %d instructions", c.PC, n)
		}
		if err := c.Step(); err != nil {
			t.Fatalf("after %d instructions: %v", n, err)
		}
		if c.Halted {
			break
		}
	}
	t.Logf("ran %d instructions, pc=%06x sp=%08x halted=%v", n, c.PC, c.R[7], c.Halted)
}

func TestMac(t *testing.T) {
	c := testCPU()
	c.R[0] = 0x400000
	c.R[1] = 0x400010
	c.Bus.Write16(0x400000, 0xfffe) // -2
	c.Bus.Write16(0x400010, 0x0003)
	run(t, c, "clrmac", Opnd{}, Opnd{})
	run(t, c, "mac", Opnd{Mode: mPost, Reg: 0}, Opnd{Mode: mPost, Reg: 1})
	if c.MAC != -6 || c.R[0] != 0x400002 || c.R[1] != 0x400012 {
		t.Fatalf("mac: %d er0=%x er1=%x", c.MAC, c.R[0], c.R[1])
	}
	run(t, c, "stmac", Opnd{Mode: mMACL}, r32(2))
	run(t, c, "stmac", Opnd{Mode: mMACH}, r32(3))
	if c.R[2] != 0xfffffffa || c.R[3] != 0xffffffff {
		t.Fatalf("stmac: %x %x", c.R[2], c.R[3])
	}
	c.R[4] = 0x12345678
	run(t, c, "ldmac", r32(4), Opnd{Mode: mMACL})
	c.R[4] = 1
	run(t, c, "ldmac", r32(4), Opnd{Mode: mMACH})
	if c.MAC != 1<<32|0x12345678 {
		t.Fatalf("ldmac: %x", c.MAC)
	}
}

func TestPCBitZeroIgnored(t *testing.T) {
	c := testCPU()
	c.PC = 0x400001
	c.Bus.Write16(0x400000, 0x0000) // nop
	if err := c.Step(); err != nil {
		t.Fatal(err)
	}
	if c.PC != 0x400002 {
		t.Fatalf("pc = %x", c.PC)
	}
}
