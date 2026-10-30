// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

// Second half of the instruction set: logic, shifts, bit operations,
// branches, calls, condition code moves and block move.

// exec2 handles everything exec does not.
func (c *CPU) exec2(in *Inst) error {
	sz := in.OpSize
	switch in.Base {
	case "and", "or", "xor":
		c.logic(in, sz)
	case "not":
		v := ^c.Read(in.A, sz) & sizeMask(sz)
		c.Write(in.A, sz, v)
		c.setNZ(sz, v)
		c.setFlag(fV, false)
	case "shll", "shal", "shlr", "shar", "rotl", "rotr", "rotxl", "rotxr":
		c.shift(in, sz)
	case "bset", "bclr", "bnot", "btst", "bst", "bist", "bld", "bild",
		"band", "biand", "bor", "bior", "bxor", "bixor":
		c.bitop(in)
	case "bt", "bf", "bhi", "bls", "bcc", "bcs", "bne", "beq",
		"bvc", "bvs", "bpl", "bmi", "bge", "blt", "bgt", "ble":
		if c.cond(in.Base) {
			c.PC = in.A.Val
		}
	case "bsr":
		c.Push32(c.PC)
		c.PC = in.A.Val
	case "jmp":
		c.PC = c.jumpTarget(in.A)
	case "jsr":
		t := c.jumpTarget(in.A)
		c.Push32(c.PC)
		c.PC = t
	case "rts":
		c.PC = c.Pop32() & 0xffffff
	case "rte":
		c.rte()
	case "trapa":
		c.trapa(in.A.Val)
	case "ldc":
		c.irqInhibit = true
		v := c.Read(in.A, 1)
		if in.Name == "ldc.w" {
			v = c.Read(in.A, 2) >> 8
		}
		c.Write(in.B, 1, v)
	case "stc":
		v := c.Read(in.A, 1)
		if in.Name == "stc.w" {
			c.Write(in.B, 2, v<<8|v)
		} else {
			c.Write(in.B, 1, v)
		}
	case "andc":
		c.irqInhibit = true
		c.Write(in.B, 1, c.Read(in.B, 1)&in.A.Val)
	case "orc":
		c.irqInhibit = true
		c.Write(in.B, 1, c.Read(in.B, 1)|in.A.Val)
	case "xorc":
		c.irqInhibit = true
		c.Write(in.B, 1, c.Read(in.B, 1)^in.A.Val)
	case "clrmac":
		c.MAC = 0
	case "ldmac":
		v := c.R[in.A.Reg]
		if in.B.Mode == mMACH {
			c.MAC = c.MAC&0xffffffff | sext(v&0x3ff, 10)<<32
		} else {
			c.MAC = c.MAC&^0xffffffff | int64(v)
		}
	case "stmac":
		if in.A.Mode == mMACH {
			c.R[in.B.Reg] = uint32(sext(uint32(c.MAC>>32)&0x3ff, 10))
		} else {
			c.R[in.B.Reg] = uint32(c.MAC)
		}
	case "mac":
		// mac @ERn+, @ERm+ : signed 16 x 16 added into the 42 bit accumulator
		a := sext(c.Read(in.A, 2), 16)
		b := sext(c.Read(in.B, 2), 16)
		c.MAC = sext42(c.MAC + a*b)
	case "sleep":
		c.Halted = true
	case "tas":
		v := c.Read(in.A, 1)
		c.setNZ(1, v)
		c.setFlag(fV, false)
		c.Write(in.A, 1, v|0x80)
	case "eepmov":
		c.eepmov(sz)
	case "movfpe":
		v := c.Read(in.A, 1)
		c.Write(in.B, 1, v)
		c.setNZ(1, v)
		c.setFlag(fV, false)
	case "movtpe":
		v := c.Read(in.A, 1)
		c.Write(in.B, 1, v)
		c.setNZ(1, v)
		c.setFlag(fV, false)
	default:
		return ErrUnimpl{in.PC, in.Name}
	}
	return nil
}

func (c *CPU) logic(in *Inst, sz int) {
	a := c.Read(in.B, sz)
	b := c.Read(in.A, sz)
	var r uint32
	switch in.Base {
	case "and":
		r = a & b
	case "or":
		r = a | b
	default:
		r = a ^ b
	}
	r &= sizeMask(sz)
	c.Write(in.B, sz, r)
	c.setNZ(sz, r)
	c.setFlag(fV, false)
}

func (c *CPU) cond(name string) bool {
	C, Z, N, V := c.flag(fC), c.flag(fZ), c.flag(fN), c.flag(fV)
	switch name {
	case "bt":
		return true
	case "bf":
		return false
	case "bhi":
		return !(C || Z)
	case "bls":
		return C || Z
	case "bcc":
		return !C
	case "bcs":
		return C
	case "bne":
		return !Z
	case "beq":
		return Z
	case "bvc":
		return !V
	case "bvs":
		return V
	case "bpl":
		return !N
	case "bmi":
		return N
	case "bge":
		return N == V
	case "blt":
		return N != V
	case "bgt":
		return !Z && N == V
	case "ble":
		return Z || N != V
	}
	panic("bad condition " + name)
}

// jumpTarget resolves jmp and jsr operands: @ERn, absolute 24 bit, or the
// memory indirect form @@aa:8 whose table entry sits at address aa.
func (c *CPU) jumpTarget(o Opnd) uint32 {
	switch o.Mode {
	case mReg32:
		return c.R[o.Reg] & 0xffffff
	case mJump:
		return o.Val
	case mAbsI:
		return c.Bus.Read32(o.Val) & 0xffffff
	}
	panic("bad jump operand")
}

// shift covers shll shal shlr shar and the four rotates, by 1 or 2 places.
func (c *CPU) shift(in *Inst, sz int) {
	reg, n := in.B, int(in.A.Val)
	if in.B.Mode == mNone {
		reg, n = in.A, 1
	}
	name := in.Base
	m := sizeMask(sz)
	sb := signBit(sz)
	v := c.Read(reg, sz) & m
	ovf := false
	var carry bool
	for i := 0; i < n; i++ {
		switch name {
		case "shll", "shal":
			carry = v&sb != 0
			nv := v << 1 & m
			if name == "shal" && (v^nv)&sb != 0 {
				ovf = true
			}
			v = nv
		case "shlr":
			carry = v&1 != 0
			v >>= 1
		case "shar":
			carry = v&1 != 0
			v = v>>1 | v&sb
		case "rotl":
			carry = v&sb != 0
			v = v<<1&m | b2u(carry)
		case "rotr":
			carry = v&1 != 0
			v = v>>1 | b2u(carry)*sb
		case "rotxl":
			carry = v&sb != 0
			v = v<<1&m | b2u(c.flag(fC))
		case "rotxr":
			carry = v&1 != 0
			v = v>>1 | b2u(c.flag(fC))*sb
		}
		c.setFlag(fC, carry)
	}
	c.Write(reg, sz, v)
	c.setNZ(sz, v)
	c.setFlag(fV, ovf)
}

func b2u(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

// bitop handles all single bit instructions. A is the bit number (an
// immediate or the low three bits of a register), B the byte operand.
func (c *CPU) bitop(in *Inst) {
	bit := uint(in.A.Val)
	if in.A.Mode == mReg8 {
		bit = uint(c.Read(in.A, 1) & 7)
	}
	mask := uint32(1) << bit
	v := c.Read(in.B, 1)
	set := v&mask != 0
	switch in.Base {
	case "bset":
		c.Write(in.B, 1, v|mask)
	case "bclr":
		c.Write(in.B, 1, v&^mask)
	case "bnot":
		c.Write(in.B, 1, v^mask)
	case "btst":
		c.setFlag(fZ, !set)
	case "bld":
		c.setFlag(fC, set)
	case "bild":
		c.setFlag(fC, !set)
	case "bst", "bist":
		on := c.flag(fC)
		if in.Base == "bist" {
			on = !on
		}
		if on {
			v |= mask
		} else {
			v &^= mask
		}
		c.Write(in.B, 1, v)
	case "band":
		c.setFlag(fC, c.flag(fC) && set)
	case "biand":
		c.setFlag(fC, c.flag(fC) && !set)
	case "bor":
		c.setFlag(fC, c.flag(fC) || set)
	case "bior":
		c.setFlag(fC, c.flag(fC) || !set)
	case "bxor":
		c.setFlag(fC, c.flag(fC) != set)
	case "bixor":
		c.setFlag(fC, c.flag(fC) != !set)
	}
}

// eepmov copies bytes from @ER5 to @ER6 while the counter (R4L for .b, R4 for
// .w) is not zero, then leaves the counter at zero.
func (c *CPU) eepmov(sz int) {
	for {
		var n uint32
		if sz == 1 {
			n = c.GetReg8(12)
		} else {
			n = c.GetReg16(4)
		}
		if n == 0 {
			return
		}
		c.Bus.Write8(c.R[6], c.Bus.Read8(c.R[5]))
		c.R[5]++
		c.R[6]++
		if sz == 1 {
			c.SetReg8(12, n-1)
		} else {
			c.SetReg16(4, n-1)
		}
	}
}

// sext42 wraps v to the 42 bit width of MACH:MACL.
func sext42(v int64) int64 {
	v &= 1<<42 - 1
	if v&(1<<41) != 0 {
		v -= 1 << 42
	}
	return v
}
