// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import "testing"

func testCPU() *CPU {
	b := NewBus()
	b.Add("ram", 0x400000, 0x1000, true)
	return NewCPU(b)
}

func TestRegViews(t *testing.T) {
	c := testCPU()
	c.R[2] = 0x11223344
	if c.GetReg8(2) != 0x33 || c.GetReg8(10) != 0x44 { // r2h, r2l
		t.Fatal("8 bit views wrong")
	}
	if c.GetReg16(2) != 0x3344 || c.GetReg16(10) != 0x1122 { // r2, e2
		t.Fatal("16 bit views wrong")
	}
	c.SetReg8(2, 0xAA)
	c.SetReg8(10, 0xBB)
	c.SetReg16(10, 0xCCDD)
	if c.R[2] != 0xCCDDAABB {
		t.Fatalf("got %08x", c.R[2])
	}
}

func TestAddressingModes(t *testing.T) {
	c := testCPU()
	c.R[1] = 0x400100
	c.Write(Opnd{Mode: mInd, Reg: 1}, 4, 0xDEADBEEF)
	if c.Bus.Read8(0x400100) != 0xDE || c.Bus.Read8(0x400103) != 0xEF {
		t.Fatal("not big endian")
	}
	c.Write(Opnd{Mode: mPre, Reg: 1}, 2, 0x1234) // er1 -= 2
	if c.R[1] != 0x4000FE || c.Bus.Read16(0x4000FE) != 0x1234 {
		t.Fatalf("predecrement: er1=%x", c.R[1])
	}
	v := c.Read(Opnd{Mode: mPost, Reg: 1}, 2)
	if v != 0x1234 || c.R[1] != 0x400100 {
		t.Fatalf("postincrement: v=%x er1=%x", v, c.R[1])
	}
	// 16 bit displacement is signed
	d := Opnd{Mode: mDisp, Reg: 1, Val: 0xFFFC, Kind: am_r32d16h}
	if c.EA(d, 4) != 0x4000FC {
		t.Fatalf("disp16 ea %x", c.EA(d, 4))
	}
	// byte access through the stack pointer moves by 2
	c.R[7] = 0x400200
	c.Write(Opnd{Mode: mPre, Reg: 7}, 1, 0x55)
	if c.R[7] != 0x4001FE {
		t.Fatalf("sp byte step %x", c.R[7])
	}
}

func TestFlagsNZ(t *testing.T) {
	c := testCPU()
	c.setNZ(1, 0x80)
	if !c.flag(fN) || c.flag(fZ) {
		t.Fatal("0x80 byte")
	}
	c.setNZ(2, 0x10000)
	if c.flag(fN) || !c.flag(fZ) {
		t.Fatal("masked to zero")
	}
}
