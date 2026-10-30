// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"fmt"
	"sort"
)

// On chip peripheral registers of the SWX00B. The vector layout is the H8S/2655
// one (see EMU_STATUS.md), so the standard register map is used. Registers that
// are not modelled yet read back what was written and are logged once per
// address, which is the work list for the boot.

const (
	chipBase = 0xFFFE00
	chipSize = 0x200

	regSYSCR = 0xFFFF39
	regADDRA = 0xFFFF90 // ADDRA..ADDRD, 16 bit each, 10 bit result left justified
	regADCSR = 0xFFFF98
	regADCR  = 0xFFFF99
)

// Access is one logged register access.
type Access struct {
	Addr  uint32
	Write bool
	Val   uint8
	PC    uint32
	Count int
}

// Chip holds the on chip peripherals.
type Chip struct {
	cpu  *CPU
	mem  [chipSize]uint8
	hr   map[uint32]func() uint8
	hw   map[uint32]func(v uint8)
	seen map[string]*Access

	// AD holds the analog input values (10 bit) the converter returns for AN0..AN7.
	AD [8]uint16

	tpu *TPU
	sci [3]*sciChan
}

// AttachChip creates the peripherals and maps them over the register space.
func AttachChip(c *CPU) *Chip {
	ch := &Chip{cpu: c, hr: map[uint32]func() uint8{}, hw: map[uint32]func(uint8){}, seen: map[string]*Access{}}
	for i := range ch.AD {
		ch.AD[i] = 0x3C0 // assumption: a healthy supply or centred control; revisit when boot shows what these mean
	}
	ch.initAD()
	ch.initTPU()
	ch.initSCI()
	c.Ticker = ch.Advance
	ch.hr[regSYSCR] = func() uint8 { return ch.mem[regSYSCR-chipBase] }
	ch.hw[regSYSCR] = func(v uint8) {
		ch.mem[regSYSCR-chipBase] = v
		c.IntMode = int(v>>4) & 3 // INTM1:INTM0, 0 or 2 are the defined modes
	}
	c.Bus.Devs = append(c.Bus.Devs, &Dev{Name: "onchip", Base: chipBase, Sz: chipSize, R: ch.read, W: ch.write})
	return ch
}

func (ch *Chip) read(a uint32, size int) uint32 {
	var v uint32
	for i := 0; i < size; i++ {
		v = v<<8 | uint32(ch.read8(a+uint32(i)))
	}
	return v
}

func (ch *Chip) write(a uint32, size int, v uint32) {
	for i := 0; i < size; i++ {
		ch.write8(a+uint32(i), uint8(v>>(8*uint(size-1-i))))
	}
}

func (ch *Chip) note(a uint32, write bool, v uint8) {
	key := fmt.Sprintf("%06X/%v", a, write)
	if x, ok := ch.seen[key]; ok {
		x.Count++
		return
	}
	ch.seen[key] = &Access{Addr: a, Write: write, Val: v, PC: ch.cpu.PC, Count: 1}
}

func (ch *Chip) read8(a uint32) uint8 {
	if f, ok := ch.hr[a]; ok {
		return f()
	}
	v := ch.mem[a-chipBase]
	ch.note(a, false, v)
	return v
}

func (ch *Chip) write8(a uint32, v uint8) {
	if f, ok := ch.hw[a]; ok {
		f(v)
		return
	}
	ch.note(a, true, v)
	ch.mem[a-chipBase] = v
}

// Unmodelled returns the logged accesses to registers without a model, by address.
func (ch *Chip) Unmodelled() []Access {
	var out []Access
	for _, x := range ch.seen {
		out = append(out, *x)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Addr != out[j].Addr {
			return out[i].Addr < out[j].Addr
		}
		return !out[i].Write && out[j].Write
	})
	return out
}

// ---- A/D converter ----
//
// ADCSR: b7 ADF, b6 ADIE, b5 ADST, b4 SCAN, b3 CKS, b2..0 CH.
// Conversion completes at once: ADF is set, ADST is cleared in single mode, and
// the 10 bit result lands in ADDR(A..D) = AN(0..3) of the selected group.

func (ch *Chip) initAD() {
	for i := uint32(0); i < 8; i++ {
		a := regADDRA + i
		ch.hr[a] = func() uint8 { return ch.mem[a-chipBase] }
		ch.hw[a] = func(uint8) {} // read only
	}
	ch.hr[regADCSR] = func() uint8 { return ch.mem[regADCSR-chipBase] }
	ch.hw[regADCSR] = ch.writeADCSR
	ch.hr[regADCR] = func() uint8 { return ch.mem[regADCR-chipBase] }
	ch.hw[regADCR] = func(v uint8) { ch.mem[regADCR-chipBase] = v }
}

func (ch *Chip) setADDR(n int, v uint16) {
	a := regADDRA + uint32(n)*2 - chipBase
	w := v << 6
	ch.mem[a], ch.mem[a+1] = uint8(w>>8), uint8(w)
}

func (ch *Chip) writeADCSR(v uint8) {
	old := ch.mem[regADCSR-chipBase]
	nv := v&0x7f | old&v&0x80 // ADF can only be cleared by software, never set by a write
	if v&0x20 != 0 {          // ADST
		group := int(v&4) >> 2 // CH2 selects AN0..3 or AN4..7
		if v&0x10 != 0 {       // scan: all channels up to CH
			for i := 0; i <= int(v&3); i++ {
				ch.setADDR(i, ch.AD[group*4+i])
			}
		} else {
			ch.setADDR(int(v&3), ch.AD[int(v&7)])
		}
		nv |= 0x80
		if v&0x10 == 0 {
			nv &^= 0x20 // single mode stops by itself
		}
		if v&0x40 != 0 { // ADIE
			ch.cpu.Raise(28, 3)
		}
	}
	ch.mem[regADCSR-chipBase] = nv
	if nv&0x80 == 0 {
		ch.cpu.Lower(28)
	}
}
