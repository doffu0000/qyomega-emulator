// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import "fmt"

// CCR bits.
const (
	fI  = 0x80
	fUI = 0x40
	fH  = 0x20
	fU  = 0x10
	fN  = 0x08
	fZ  = 0x04
	fV  = 0x02
	fC  = 0x01
)

// CPU is the H8S/2000 in advanced mode (24 bit addresses).
type CPU struct {
	R       [8]uint32 // er0..er7, er7 is the stack pointer
	PC      uint32
	CCR     uint8
	EXR     uint8
	Bus     *Bus
	Cycles  uint64
	Halted  bool             // sleep
	Ticker  func(states int) // called with the system clocks each step took
	IntMode int              // interrupt control mode from SYSCR.INTM: 0 (default) or 2
	MAC     int64            // multiply accumulate register (H8S/2600 core): MACH is bits 32..41, MACL bits 0..31
	cache   []*Inst

	pending    [maxVec]uint8 // pending interrupt level per vector, 0 = none
	npend      int           // how many vectors are pending, so the common case (none) costs nothing
	irqInhibit bool          // set after ldc/andc/orc/xorc: one instruction without interrupts
}

func NewCPU(b *Bus) *CPU { return &CPU{Bus: b, CCR: fI | fUI} }

// ---- register views ----

func (c *CPU) GetReg8(n int) uint32 {
	if n < 8 {
		return c.R[n] >> 8 & 0xff
	}
	return c.R[n-8] & 0xff
}

func (c *CPU) SetReg8(n int, v uint32) {
	v &= 0xff
	if n < 8 {
		c.R[n] = c.R[n]&^0xff00 | v<<8
	} else {
		c.R[n-8] = c.R[n-8]&^0xff | v
	}
}

func (c *CPU) GetReg16(n int) uint32 {
	if n < 8 {
		return c.R[n] & 0xffff
	}
	return c.R[n-8] >> 16
}

func (c *CPU) SetReg16(n int, v uint32) {
	v &= 0xffff
	if n < 8 {
		c.R[n] = c.R[n]&^0xffff | v
	} else {
		c.R[n-8] = c.R[n-8]&0xffff | v<<16
	}
}

// ---- flags ----

func (c *CPU) flag(f uint8) bool { return c.CCR&f != 0 }

func (c *CPU) setFlag(f uint8, on bool) {
	if on {
		c.CCR |= f
	} else {
		c.CCR &^= f
	}
}

func sizeMask(size int) uint32 {
	switch size {
	case 1:
		return 0xff
	case 2:
		return 0xffff
	}
	return 0xffffffff
}

func signBit(size int) uint32 { return 1 << (uint(size)*8 - 1) }

// setNZ sets N and Z from v at the given operand size.
func (c *CPU) setNZ(size int, v uint32) {
	v &= sizeMask(size)
	c.setFlag(fN, v&signBit(size) != 0)
	c.setFlag(fZ, v == 0)
}

// ---- operand access ----

// EA resolves the effective address of a memory operand, applying the
// pre decrement or post increment side effect for size bytes.
func (c *CPU) EA(o Opnd, size int) uint32 {
	switch o.Mode {
	case mInd:
		return c.R[o.Reg] & 0xffffff
	case mPre:
		c.R[o.Reg] -= c.step(o.Reg, size)
		return c.R[o.Reg] & 0xffffff
	case mPost:
		a := c.R[o.Reg] & 0xffffff
		c.R[o.Reg] += c.step(o.Reg, size)
		return a
	case mDisp:
		d := o.Val
		if o.Kind == am_r32d16h {
			d = uint32(s16(d))
		}
		return (c.R[o.Reg] + d) & 0xffffff
	case mAbs:
		return o.Val & 0xffffff
	}
	panic(fmt.Sprintf("EA of non memory operand %v", o))
}

// step is the pointer adjustment. Byte accesses through er7 keep the stack
// word aligned (H8S manual), everything else moves by the access size.
func (c *CPU) step(reg, size int) uint32 {
	if reg == 7 && size == 1 {
		return 2
	}
	return uint32(size)
}

func (c *CPU) memRead(a uint32, size int) uint32 {
	switch size {
	case 1:
		return uint32(c.Bus.Read8(a))
	case 2:
		return uint32(c.Bus.Read16(a))
	}
	return c.Bus.Read32(a)
}

func (c *CPU) memWrite(a uint32, size int, v uint32) {
	switch size {
	case 1:
		c.Bus.Write8(a, uint8(v))
	case 2:
		c.Bus.Write16(a, uint16(v))
	default:
		c.Bus.Write32(a, v)
	}
}

// Read returns the operand value at size 1, 2 or 4 bytes.
func (c *CPU) Read(o Opnd, size int) uint32 {
	switch o.Mode {
	case mReg8:
		return c.GetReg8(o.Reg)
	case mReg16:
		return c.GetReg16(o.Reg)
	case mReg32:
		return c.R[o.Reg]
	case mImm, mAbsI, mJump:
		return o.Val & sizeMask(size)
	case mCCR:
		return uint32(c.CCR)
	case mEXR:
		return uint32(c.EXR)
	case mInd, mPre, mPost, mDisp, mAbs:
		return c.memRead(c.EA(o, size), size)
	}
	panic(fmt.Sprintf("cannot read operand %v", o))
}

// Write stores v into the operand.
func (c *CPU) Write(o Opnd, size int, v uint32) {
	switch o.Mode {
	case mReg8:
		c.SetReg8(o.Reg, v)
	case mReg16:
		c.SetReg16(o.Reg, v)
	case mReg32:
		c.R[o.Reg] = v
	case mCCR:
		c.CCR = uint8(v)
	case mEXR:
		c.EXR = uint8(v)
	case mInd, mPre, mPost, mDisp, mAbs:
		c.memWrite(c.EA(o, size), size, v)
	default:
		panic(fmt.Sprintf("cannot write operand %v", o))
	}
}

// Push32 and Pop32 use er7 as a full descending stack.
func (c *CPU) Push16(v uint32) {
	c.R[7] -= 2
	c.Bus.Write16(c.R[7], uint16(v))
}

func (c *CPU) Pop16() uint32 {
	v := uint32(c.Bus.Read16(c.R[7]))
	c.R[7] += 2
	return v
}

func (c *CPU) Push32(v uint32) {
	c.R[7] -= 4
	c.Bus.Write32(c.R[7], v)
}

func (c *CPU) Pop32() uint32 {
	v := c.Bus.Read32(c.R[7])
	c.R[7] += 4
	return v
}
