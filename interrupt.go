// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

// Interrupt handling. The mode follows SYSCR.INTM (CPU.IntMode):
//
// Mode 0 (reset default, and what the firmware runs: it never writes SYSCR and
// its task switcher skips the EXR word, see EMU_STATUS.md): the I bit in CCR
// masks every interrupt, the frame is CCR plus the 24 bit PC, and accepting an
// interrupt sets I.
//
//	sp -> CCR  PC23-16  PC15-8  PC7-0
//
// Mode 2: priority levels in EXR.I2-I0, and the frame gets an EXR word below:
//
//	sp -> EXR  reserved  CCR  PC23-16  PC15-8  PC7-0
//
// Peripherals call Raise/Lower; a request stays pending (level triggered) until
// the device lowers it, which is what clearing the flag in firmware does.

// vectorBase is where the firmware keeps its copy of the vector table.
// The protected boot block forwards exceptions here.
const vectorBase = 0x10000

const maxVec = 128

// Raise marks vector vec pending at priority level 1..7.
func (c *CPU) Raise(vec int, level uint8) {
	if c.pending[vec] == 0 && level != 0 {
		c.npend++
	} else if c.pending[vec] != 0 && level == 0 {
		c.npend--
	}
	c.pending[vec] = level
}

// Lower clears a pending request.
func (c *CPU) Lower(vec int) { c.Raise(vec, 0) }

// recountPending rebuilds npend after pending was written directly (a snapshot load).
func (c *CPU) recountPending() {
	c.npend = 0
	for _, l := range c.pending {
		if l != 0 {
			c.npend++
		}
	}
}

// exception stacks PC, CCR and EXR and jumps through the vector table.
func (c *CPU) exception(vec int, newMask int) {
	c.Push32(uint32(c.CCR)<<24 | c.PC&0xffffff)
	if c.IntMode == 2 {
		c.Push16(uint32(c.EXR) << 8)
		c.EXR &^= 0x80 // T is cleared by every exception
		if newMask >= 0 {
			c.EXR = c.EXR&^7 | uint8(newMask) // interrupts take their own level; trapa leaves the mask alone
		}
	} else {
		c.CCR |= fI
	}
	c.PC = c.Bus.Read32(vectorBase+uint32(vec)*4) & 0xffffff
	c.Halted = false
}

// checkIrq accepts the best pending interrupt if its level beats EXR.I2-I0.
// Equal levels are served lowest vector first.
func (c *CPU) checkIrq() bool {
	if c.npend == 0 {
		return false
	}
	if c.IntMode != 2 && c.flag(fI) { // mode 0: the I bit masks everything, nothing to look for
		return false
	}
	best, bestLevel := -1, uint8(0)
	for v, l := range c.pending {
		if l > bestLevel {
			best, bestLevel = v, l
		}
	}
	if best < 0 {
		return false
	}
	if c.IntMode == 2 {
		if bestLevel <= c.EXR&7 {
			return false
		}
	} else if c.flag(fI) {
		return false
	}
	c.exception(best, int(bestLevel))
	return true
}

func (c *CPU) rte() {
	if c.IntMode == 2 {
		c.EXR = uint8(c.Pop16() >> 8)
	}
	v := c.Pop32()
	c.CCR = uint8(v >> 24)
	c.PC = v & 0xffffff
}

func (c *CPU) trapa(n uint32) {
	c.exception(8+int(n), -1)
}
