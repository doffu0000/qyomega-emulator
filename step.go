// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"fmt"
	"strings"
)

// opSize returns the operand size implied by the mnemonic suffix.
func opSize(name string) int {
	switch {
	case strings.HasSuffix(name, ".b"):
		return 1
	case strings.HasSuffix(name, ".w"):
		return 2
	case strings.HasSuffix(name, ".l"):
		return 4
	}
	return 0
}

func baseName(name string) string {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[:i]
	}
	return name
}

// ErrUnimpl is returned for instructions not executed yet.
type ErrUnimpl struct {
	PC   uint32
	Name string
}

func (e ErrUnimpl) Error() string { return fmt.Sprintf("%06X: %s not implemented", e.PC, e.Name) }

// Step executes one instruction.
func (c *CPU) Step() error {
	if c.irqInhibit {
		c.irqInhibit = false
	} else if c.checkIrq() {
		c.Cycles++
		c.tick(statesPerInstr)
		return nil
	}
	if c.Halted {
		c.Cycles++
		c.tick(64) // asleep: let time pass quickly until something wakes us
		return nil
	}
	c.PC &^= 1 // PC bit 0 is always 0; the kernel sets it in service call frames as a marker
	in := c.fetch()
	c.PC = (c.PC + uint32(in.Size)) & 0xffffff
	c.Cycles++
	err := c.exec(in)
	c.tick(statesPerInstr)
	return err
}

func (c *CPU) tick(n int) {
	if c.Ticker != nil {
		c.Ticker(n)
	}
}

// exec runs a decoded instruction. PC already points past it.
func (c *CPU) exec(in *Inst) error {
	if in.Base == "" { // built by hand (tests), not by fetch
		in.prepare()
	}
	sz := in.OpSize
	switch in.Base {
	case "nop":
	case "mov":
		v := c.Read(in.A, sz)
		c.Write(in.B, sz, v)
		c.setNZ(sz, v)
		c.setFlag(fV, false)
	case "stm":
		c.stm(in)
	case "ldm":
		c.ldm(in)
	case "add":
		c.arith(in, sz, false, true, false)
	case "sub":
		c.arith(in, sz, true, true, false)
	case "cmp":
		c.arith(in, sz, true, false, false)
	case "addx":
		c.arith(in, sz, false, true, true)
	case "subx":
		c.arith(in, sz, true, true, true)
	case "neg":
		c.neg(in, sz)
	case "inc", "dec":
		c.incdec(in, sz, in.Base == "inc")
	case "adds":
		c.Write(in.B, 4, c.Read(in.B, 4)+in.A.Val)
	case "subs":
		c.Write(in.B, 4, c.Read(in.B, 4)-in.A.Val)
	case "exts":
		c.ext(in, sz, true)
	case "extu":
		c.ext(in, sz, false)
	case "mulxu":
		c.mul(in, sz, false)
	case "mulxs":
		c.mul(in, sz, true)
	case "divxu":
		c.div(in, sz, false)
	case "divxs":
		c.div(in, sz, true)
	case "daa":
		c.daa(in)
	case "das":
		c.das(in)
	default:
		return c.exec2(in)
	}
	return nil
}

// stm pushes the register list lowest register first, so the highest ends up
// at the top of the stack; ldm pops the highest first. Verified against the
// firmware: its interrupt entry builds task frames by hand as er4, er5, er6 in
// push order and the task switcher restores them with ldm.l @sp+,er4-er6.
func (c *CPU) stm(in *Inst) {
	for r := in.A.Reg; r <= in.A.Reg2; r++ {
		c.Push32(c.R[r])
	}
}

func (c *CPU) ldm(in *Inst) {
	for r := in.B.Reg2; r >= in.B.Reg; r-- {
		c.R[r] = c.Pop32()
	}
}

// halfMask covers the bits below the half carry position for each size.
func halfMask(sz int) uint32 {
	switch sz {
	case 2:
		return 0xfff
	case 4:
		return 0xfffffff
	}
	return 0xf
}

// arith does add, sub, cmp, addx, subx. The destination is B, the source A.
func (c *CPU) arith(in *Inst, sz int, sub, store, withCarry bool) {
	m := sizeMask(sz)
	a := c.Read(in.B, sz) & m
	b := c.Read(in.A, sz) & m
	cin := uint32(0)
	if withCarry && c.flag(fC) {
		cin = 1
	}
	var r uint32
	var half, carry bool
	hm := halfMask(sz)
	sb := signBit(sz)
	if !sub {
		full := uint64(a) + uint64(b) + uint64(cin)
		r = uint32(full) & m
		carry = full > uint64(m)
		half = (a&hm)+(b&hm)+cin > hm
		c.setFlag(fV, (a^r)&(b^r)&sb != 0)
	} else {
		r = (a - b - cin) & m
		carry = uint64(a) < uint64(b)+uint64(cin)
		half = (a & hm) < (b&hm)+cin
		c.setFlag(fV, (a^b)&(a^r)&sb != 0)
	}
	c.setFlag(fH, half)
	c.setFlag(fC, carry)
	c.setFlag(fN, r&sb != 0)
	if withCarry {
		if r != 0 { // addx and subx only ever clear Z
			c.setFlag(fZ, false)
		}
	} else {
		c.setFlag(fZ, r == 0)
	}
	if store {
		c.Write(in.B, sz, r)
	}
}

func (c *CPU) neg(in *Inst, sz int) {
	m := sizeMask(sz)
	a := c.Read(in.A, sz) & m
	r := (0 - a) & m
	c.setFlag(fH, a&halfMask(sz) != 0)
	c.setFlag(fC, a != 0)
	c.setFlag(fV, a == signBit(sz))
	c.setNZ(sz, r)
	c.Write(in.A, sz, r)
}

// incdec sets N, Z, V only (no carry, H unchanged). A is the constant, B the register.
func (c *CPU) incdec(in *Inst, sz int, inc bool) {
	m := sizeMask(sz)
	a := c.Read(in.B, sz) & m
	k := in.A.Val
	var r uint32
	var v bool
	sb := signBit(sz)
	if inc {
		r = (a + k) & m
		v = a&sb == 0 && r&sb != 0
	} else {
		r = (a - k) & m
		v = a&sb != 0 && r&sb == 0
	}
	c.setNZ(sz, r)
	c.setFlag(fV, v)
	c.Write(in.B, sz, r)
}

// ext: exts.w sign extends the low byte of a 16 bit register, exts.l the low word
// of a 32 bit register; extu clears the upper half instead.
func (c *CPU) ext(in *Inst, sz int, signed bool) {
	v := c.Read(in.A, sz)
	half := uint(sz) * 4
	lowMask := uint32(1)<<half - 1
	v &= lowMask
	if signed && v&(1<<(half-1)) != 0 {
		v |= sizeMask(sz) &^ lowMask
	}
	c.Write(in.A, sz, v)
	c.setNZ(sz, v)
	c.setFlag(fV, false)
}

// mul: mulxu/mulxs.b Rs, Rd  : Rd(16) = Rd.L * Rs
//
//	mulxu/mulxs.w Rs, ERd : ERd(32) = Rd * Rs
func (c *CPU) mul(in *Inst, sz int, signed bool) {
	src := c.Read(in.A, sz)
	dst := c.Read(in.B, sz*2) & sizeMask(sz)
	var r uint32
	if signed {
		bits := uint(sz) * 8
		r = uint32(sext(dst, bits) * sext(src, bits))
	} else {
		r = dst * src
	}
	rs := sz * 2
	r &= sizeMask(rs)
	c.Write(in.B, rs, r)
	if signed {
		c.setNZ(rs, r)
	}
}

func sext(v uint32, bits uint) int64 {
	if v&(1<<(bits-1)) != 0 {
		return int64(v) - (1 << bits)
	}
	return int64(v)
}

// div: divxu/divxs.b Rs, Rd  : Rd(16) / Rs(8): quotient in Rd.L, remainder in Rd.H
//
//	divxu/divxs.w Rs, ERd : ERd(32) / Rs(16): quotient in Rd, remainder in Ed
//
// A zero divisor leaves the destination alone and sets Z.
func (c *CPU) div(in *Inst, sz int, signed bool) {
	bits := uint(sz) * 8
	src := c.Read(in.A, sz)
	dst := c.Read(in.B, sz*2)
	if signed {
		c.setFlag(fN, false)
	} else {
		c.setFlag(fN, src&(1<<(bits-1)) != 0)
	}
	c.setFlag(fZ, src == 0)
	if src == 0 {
		return
	}
	var q, r uint32
	if signed {
		d := sext(dst, bits*2)
		s := sext(src, bits)
		q64, r64 := d/s, d%s
		q, r = uint32(q64), uint32(r64)
		c.setFlag(fN, q64 < 0)
	} else {
		q, r = dst/src, dst%src
	}
	m := sizeMask(sz)
	c.Write(in.B, sz*2, (r&m)<<bits|q&m)
}

func (c *CPU) daa(in *Inst) {
	a := c.Read(in.A, 1)
	adj := uint32(0)
	carry := c.flag(fC)
	if c.flag(fH) || a&0xf > 9 {
		adj |= 6
	}
	if carry || a > 0x99 {
		adj |= 0x60
		carry = true
	}
	r := (a + adj) & 0xff
	c.Write(in.A, 1, r)
	c.setNZ(1, r)
	c.setFlag(fC, carry)
}

func (c *CPU) das(in *Inst) {
	a := c.Read(in.A, 1)
	adj := uint32(0)
	if c.flag(fH) {
		adj |= 6
	}
	if c.flag(fC) {
		adj |= 0x60
	}
	r := (a - adj) & 0xff
	c.Write(in.A, 1, r)
	c.setNZ(1, r)
}

// fetch decodes the instruction at PC. Flash is read only, so its decoded
// instructions are cached; code running from RAM is decoded every time.
func (c *CPU) fetch() *Inst {
	pc := c.PC
	if pc < flashSize {
		if c.cache == nil {
			c.cache = make([]*Inst, flashSize)
		}
		if p := c.cache[pc]; p != nil {
			return p
		}
		in := Decode(c.Bus, pc)
		in.prepare()
		c.cache[pc] = &in
		return &in
	}
	in := Decode(c.Bus, pc)
	in.prepare()
	return &in
}

const flashSize = 0x200000
