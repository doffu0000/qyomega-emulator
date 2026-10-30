// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import "fmt"

// Operand addressing modes after decoding.
const (
	mNone  = iota
	mReg8  // Reg 0..15: 0..7 = r0h..r7h, 8..15 = r0l..r7l
	mReg16 // Reg 0..15: 0..7 = r0..r7, 8..15 = e0..e7
	mReg32 // Reg 0..7 = er0..er7 (er7 = sp)
	mInd   // @ERn
	mPre   // @-ERn
	mPost  // @ERn+
	mDisp  // @(Val, ERn); Val is the raw displacement (16 or 32 bits)
	mAbs   // @Val (absolute address, already resolved to 24 bits)
	mAbsI  // @Val as 8 bit immediate address (vector index for jsr @@aa)
	mImm   // #Val
	mJump  // plain target (abs16e / abs24e / rel), Val = address
	mCCR
	mEXR
	mMACH
	mMACL
	mPush // @-sp (stm)
	mPop  // @sp+ (ldm)
	mRegs // register list Reg..Reg2
)

// Opnd is one decoded operand.
type Opnd struct {
	Mode int
	Reg  int
	Reg2 int
	Val  uint32
	Wide int // for mImm / mDisp: digits to print (2, 4, 8) or 0 for "%x"
	Kind int // the am_* it came from
}

var r8names = [16]string{"r0h", "r1h", "r2h", "r3h", "r4h", "r5h", "r6h", "r7h",
	"r0l", "r1l", "r2l", "r3l", "r4l", "r5l", "r6l", "r7l"}
var r16names = [16]string{"r0", "r1", "r2", "r3", "r4", "r5", "r6", "r7",
	"e0", "e1", "e2", "e3", "e4", "e5", "e6", "e7"}
var r32names = [8]string{"er0", "er1", "er2", "er3", "er4", "er5", "er6", "sp"}

func s8(v uint32) int32  { return int32(int8(v)) }
func s16(v uint32) int32 { return int32(int16(v)) }

// fetch helpers read big endian code bytes without alignment.
func (b *Bus) U8(a uint32) uint32  { return uint32(b.Read8(a)) }
func (b *Bus) U16(a uint32) uint32 { return b.U8(a)<<8 | b.U8(a+1) }
func (b *Bus) U32(a uint32) uint32 { return b.U16(a)<<16 | b.U16(a+2) }

// decodeOperand ports h8_disassembler::disassemble_am (advanced mode) as
// re/h8dis.py does, but returns a structure instead of text.
func decodeOperand(b *Bus, am int, pc, op uint32, slot, size int) Opnd {
	epc := pc + uint32(size)
	o := Opnd{Kind: am}
	switch am {
	case am_r8l:
		o.Mode, o.Reg = mReg8, int(op&15)
	case am_r8h:
		o.Mode, o.Reg = mReg8, int(op>>4&15)
	case am_r8u:
		o.Mode, o.Reg = mReg8, int(op>>8&15)
	case am_r16l:
		o.Mode, o.Reg = mReg16, int(op&15)
	case am_r16h:
		o.Mode, o.Reg = mReg16, int(op>>4&15)
	case am_r32l:
		o.Mode, o.Reg = mReg32, int(op&7)
	case am_r32h:
		o.Mode, o.Reg = mReg32, int(op>>4&7)
	case am_r32ih:
		o.Mode, o.Reg = mInd, int(op>>4&7)
	case am_r32ihh:
		o.Mode, o.Reg = mInd, int(op>>20&7)
	case am_pr32h:
		o.Mode, o.Reg = mPre, int(op>>4&7)
	case am_r32pl:
		o.Mode, o.Reg = mPost, int(op&7)
	case am_r32ph:
		o.Mode, o.Reg = mPost, int(op>>4&7)
	case am_r32d16h:
		o.Mode, o.Reg, o.Val = mDisp, int(op>>4&7), b.U16(epc-2)
	case am_r32d32hh:
		o.Mode, o.Reg, o.Val = mDisp, int(op>>20&7), b.U32(epc-4)
	case am_psp:
		o.Mode = mPush
	case am_spp:
		o.Mode = mPop
	case am_r32n2l:
		o.Mode, o.Reg, o.Reg2 = mRegs, int(op&6), int(op&6)+1
	case am_r32n3l:
		o.Mode, o.Reg, o.Reg2 = mRegs, int(op&4), int(op&4)+2
	case am_r32n4l:
		o.Mode, o.Reg, o.Reg2 = mRegs, int(op&4), int(op&4)+3
	case am_abs8:
		o.Mode, o.Val = mAbs, 0xffff00|b.U8(pc+1)
	case am_abs16:
		a := epc - 2
		if slot == 2 {
			a = epc - 4
		}
		o.Mode, o.Val = mAbs, uint32(s16(b.U16(a)))&0xffffff
	case am_abs32:
		a := epc - 4
		if slot == 3 {
			a = epc - 6
		}
		o.Mode, o.Val = mAbs, b.U32(a)
	case am_abs8i:
		o.Mode, o.Val, o.Wide = mAbsI, b.U8(pc+1), 2
	case am_abs16e:
		o.Mode, o.Val, o.Wide = mJump, b.U16(pc+2), 4
	case am_abs24e:
		o.Mode, o.Val, o.Wide = mJump, b.U32(pc)&0xffffff, 6
	case am_rel8:
		o.Mode, o.Val, o.Wide = mJump, uint32(int32(pc)+2+s8(b.U8(pc+1)))&0xffffff, 6
	case am_rel16:
		o.Mode, o.Val, o.Wide = mJump, uint32(int32(pc)+4+s16(b.U16(pc+2)))&0xffffff, 6
	case am_one:
		o.Mode, o.Val, o.Wide = mImm, 1, -1
	case am_two:
		o.Mode, o.Val, o.Wide = mImm, 2, -1
	case am_four:
		o.Mode, o.Val, o.Wide = mImm, 4, -1
	case am_imm2:
		o.Mode, o.Val = mImm, op>>4&3
	case am_imm3:
		o.Mode, o.Val = mImm, op>>4&7
	case am_imm8:
		o.Mode, o.Val, o.Wide = mImm, b.U8(epc-1), 2
	case am_imm16:
		o.Mode, o.Val, o.Wide = mImm, b.U16(pc+2), 4
	case am_imm32:
		o.Mode, o.Val, o.Wide = mImm, b.U32(pc+2), 8
	case am_ccr:
		o.Mode = mCCR
	case am_exr:
		o.Mode = mEXR
	case am_mach:
		o.Mode = mMACH
	case am_macl:
		o.Mode = mMACL
	case am_none:
		o.Mode = mNone
	default:
		panic(fmt.Sprintf("operand kind %s not handled", amNames[am]))
	}
	return o
}

// String formats like re/h8dis.py so the two can be diffed.
func (o Opnd) String() string {
	switch o.Mode {
	case mReg8:
		return r8names[o.Reg]
	case mReg16:
		return r16names[o.Reg]
	case mReg32:
		return r32names[o.Reg]
	case mInd:
		return "@" + r32names[o.Reg]
	case mPre:
		return "@-" + r32names[o.Reg]
	case mPost:
		return "@" + r32names[o.Reg] + "+"
	case mDisp:
		return fmt.Sprintf("@(h'%x, %s)", o.Val, r32names[o.Reg])
	case mAbs:
		return fmt.Sprintf("@h'%06x", o.Val)
	case mAbsI:
		return fmt.Sprintf("@h'%02x", o.Val)
	case mImm:
		switch o.Wide {
		case -1:
			return fmt.Sprintf("#%d", o.Val)
		case 0:
			return fmt.Sprintf("#%x", o.Val)
		default:
			return fmt.Sprintf("#h'%0*x", o.Wide, o.Val)
		}
	case mJump:
		return fmt.Sprintf("h'%0*x", o.Wide, o.Val)
	case mCCR:
		return "ccr"
	case mEXR:
		return "exr"
	case mMACH:
		return "mach"
	case mMACL:
		return "macl"
	case mPush:
		return "@-sp"
	case mPop:
		return "@sp+"
	case mRegs:
		return r32names[o.Reg] + "-" + r32names[o.Reg2]
	}
	return "?"
}

// Inst is a fully decoded instruction.
type Inst struct {
	PC   uint32
	Size int
	Name string // "illegal" when nothing matched
	A, B Opnd   // first and second operand as in the listing (source first)

	// filled in once by prepare, so the executor does not parse the mnemonic every time
	Base   string // the mnemonic without its size suffix
	OpSize int    // 1, 2 or 4 from the suffix, 0 when there is none
}

// prepare fills in Base and OpSize.
func (i *Inst) prepare() {
	i.Base = baseName(i.Name)
	i.OpSize = opSize(i.Name)
}

func (i Inst) String() string {
	if i.A.Mode == mNone {
		return i.Name
	}
	s := fmt.Sprintf("%-8s%s", i.Name, i.A)
	if i.B.Mode != mNone {
		s += ", " + i.B.String()
	}
	return s
}

// Decode returns the instruction at pc with both operands resolved.
func Decode(b *Bus, pc uint32) Inst {
	d := decode(b, pc)
	if d.E == nil {
		return Inst{PC: pc, Size: 2, Name: "illegal"}
	}
	e := d.E
	return Inst{PC: pc, Size: e.size, Name: e.name,
		A: decodeOperand(b, e.am1, pc, d.Op, e.slot, e.size),
		B: decodeOperand(b, e.am2, pc, d.Op, e.slot, e.size)}
}
