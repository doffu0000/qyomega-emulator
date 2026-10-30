// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

// 16 bit timer pulse unit (TPU), H8S/2655 register layout. Only the counter,
// the general registers, compare match, overflow and the interrupt flags are
// modelled; the output pins and input capture do nothing.
//
// Clock: the firmware programs TPU0 with TCR=0x21 (phi/4) and TGRA=0x2112, a
// 2.000 ms period exactly when phi = 16.9344 MHz (384 x 44.1 kHz), so that is
// the assumed system clock. One instruction is counted as statesPerInstr clocks.

const (
	statesPerInstr = 3 // average clocks per instruction; only the ratio to the timers matters
	tpuBatch       = 48

	regTSTR = 0xFFFFC0
	regTSYR = 0xFFFFC1
)

type tpuChan struct {
	name  string
	base  uint32
	div   [8]uint64 // prescaler per TPSC value, 0 = external clock, not counted
	vec   [8]int    // interrupt vector per TSR/TIER bit, 0 = none
	bit   uint      // bit in TSTR
	regs  [16]uint8 // TCR, TMDR, TIOR(H/L), TIER, TSR, TCNT, TGR... as bytes
	cnt   uint16
	tgr   [4]uint16
	frac  uint64
	chip  *Chip
	index int

	lastActive uint16 // TSR & TIER at the last refresh; lastUnknown after a snapshot load
}

const lastUnknown = 0xFFFF

// TPU holds the six channels and the shared start register.
type TPU struct {
	ch      [3]*tpuChan
	tstr    uint8
	pending uint64
}

func (ch *Chip) initTPU() {
	t := &TPU{}
	ch.tpu = t
	t.ch[0] = &tpuChan{name: "TPU0", base: 0xFFFFD0, bit: 0,
		div: [8]uint64{1, 4, 16, 64},
		vec: [8]int{32, 33, 34, 35, 36}}
	t.ch[1] = &tpuChan{name: "TPU1", base: 0xFFFFE0, bit: 1,
		div: [8]uint64{1, 4, 16, 64, 256},
		vec: [8]int{40, 41, 0, 0, 42, 43}}
	t.ch[2] = &tpuChan{name: "TPU2", base: 0xFFFFF0, bit: 2,
		div: [8]uint64{1, 4, 16, 64, 0, 0, 0, 1024},
		vec: [8]int{44, 45, 0, 0, 46, 47}}
	for i, c := range t.ch {
		c.chip, c.index = ch, i
		c.install()
	}
	ch.hr[regTSTR] = func() uint8 { ch.flushTPU(); return t.tstr }
	ch.hw[regTSTR] = func(v uint8) { ch.flushTPU(); t.tstr = v & 0x3f }
	ch.hr[regTSYR] = func() uint8 { return ch.mem[regTSYR-chipBase] }
	ch.hw[regTSYR] = func(v uint8) { ch.mem[regTSYR-chipBase] = v }
}

func (c *tpuChan) install() {
	ch := c.chip
	for off := uint32(0); off < 16; off++ {
		off := off
		a := c.base + off
		ch.hr[a] = func() uint8 { ch.flushTPU(); return c.read(off) }
		ch.hw[a] = func(v uint8) { ch.flushTPU(); c.write(off, v) }
	}
}

// register offsets inside a channel
const (
	tTCR  = 0
	tTMDR = 1
	tTIER = 4
	tTSR  = 5
	tTCNT = 6 // 6 and 7
	tTGRA = 8 // 8..15, two bytes each
)

func (c *tpuChan) read(off uint32) uint8 {
	switch {
	case off == tTCNT:
		return uint8(c.cnt >> 8)
	case off == tTCNT+1:
		return uint8(c.cnt)
	case off >= tTGRA:
		g := c.tgr[(off-tTGRA)/2]
		if off&1 == 0 {
			return uint8(g >> 8)
		}
		return uint8(g)
	}
	return c.regs[off]
}

func (c *tpuChan) write(off uint32, v uint8) {
	switch {
	case off == tTCNT:
		c.cnt = c.cnt&0xff | uint16(v)<<8
	case off == tTCNT+1:
		c.cnt = c.cnt&0xff00 | uint16(v)
	case off >= tTGRA:
		i := (off - tTGRA) / 2
		if off&1 == 0 {
			c.tgr[i] = c.tgr[i]&0xff | uint16(v)<<8
		} else {
			c.tgr[i] = c.tgr[i]&0xff00 | uint16(v)
		}
	case off == tTSR:
		c.regs[tTSR] &= v // flags clear when written as 0, a 1 never sets
		c.refresh()
	case off == tTIER:
		c.regs[tTIER] = v
		c.refresh()
	default:
		c.regs[off] = v
	}
}

// refresh raises or lowers each interrupt request from its flag and enable bit. It does
// nothing when the set of flags that are both set and enabled is the same as last time.
func (c *tpuChan) refresh() {
	active := uint16(c.regs[tTSR] & c.regs[tTIER])
	if active == c.lastActive {
		return
	}
	c.lastActive = active
	for b := uint(0); b < 8; b++ {
		v := c.vec[b]
		if v == 0 {
			continue
		}
		if c.regs[tTSR]&c.regs[tTIER]&(1<<b) != 0 {
			c.chip.cpu.Raise(v, 1)
		} else {
			c.chip.cpu.Lower(v)
		}
	}
}

// clearSource returns the TGR index that clears the counter, or -1.
func (c *tpuChan) clearSource() int {
	switch c.regs[tTCR] >> 5 & 7 {
	case 1:
		return 0
	case 2:
		return 1
	case 5:
		return 2
	case 6:
		return 3
	}
	return -1
}

// advance runs the counter for states system clocks.
func (c *tpuChan) advance(states uint64) {
	div := c.div[c.regs[tTCR]&7]
	if div == 0 {
		return
	}
	c.frac += states
	n := c.frac / div
	c.frac %= div
	clr := c.clearSource()
	for n > 0 {
		// distance in ticks to the next compare match or overflow
		step := uint64(65536 - uint32(c.cnt)) // overflow
		for i := range c.tgr {
			if i >= 2 && c.vec[i] == 0 {
				continue
			}
			var d uint64
			if i == clr {
				d = uint64(c.tgr[i]) + 1 - uint64(c.cnt) // counter runs 0..TGR then clears
				if c.cnt > c.tgr[i] {
					d = 65536 - uint64(c.cnt) + uint64(c.tgr[i]) + 1
				}
			} else {
				d = uint64(c.tgr[i]-c.cnt) & 0xffff
				if d == 0 {
					d = 65536
				}
			}
			if d < step {
				step = d
			}
		}
		if step > n {
			c.cnt += uint16(n)
			return
		}
		n -= step
		cnt := uint32(c.cnt) + uint32(step)
		c.cnt = uint16(cnt)
		if cnt >= 65536 {
			c.regs[tTSR] |= 0x10 // TCFV
		}
		for i := range c.tgr {
			if i >= 2 && c.vec[i] == 0 {
				continue
			}
			if i == clr {
				if uint32(c.tgr[i])+1 == cnt || cnt >= 65536 && c.tgr[i] == 0xffff {
					c.regs[tTSR] |= 1 << uint(i)
					c.cnt = 0
				}
			} else if c.cnt == c.tgr[i] {
				c.regs[tTSR] |= 1 << uint(i)
			}
		}
	}
	c.refresh()
}

// tpu2TickGRA is the compare value of the second periodic tick, TPU2 (vector 44).
// ASSUMPTION: the main program never writes TPU2 or sets its TSTR bit, yet it
// installs a handler for vector 44 (0xE0A44, which calls 0xB7D30 and drains the
// event counter at RAM 0x409B35 that the code waits on after a bulk mode OFF), so
// the protected boot block, which is not in the image, must start it. The period
// is a guess: 1 ms (phi/4, 4233 counts) where TPU0 runs at 2 ms.
const tpu2TickGRA = 0x1089

// tpu1TickGRA is the initial compare value of the sequencer tick, TPU1 (vector 40, handler
// 0xB7FDC). The handler itself re-arms TGRA1 whenever the tempo word at RAM 0x407310
// changes, as 5292000 / tempo - 1 (tempo in tenths of a BPM), which is 4409 at 120 BPM:
// with phi/4 that is 1.0417 ms, 480 ticks per quarter note, and the handler sends one MIDI
// clock every 20 ticks (24 per quarter). That confirms phi = 16.9344 MHz. ASSUMPTION, as
// for TPU2: the boot block (not in the image) starts this timer; the firmware never sets
// its TSTR bit, yet its handler and its RMW masking of TIER1 (0xFFFFE4) are all over the
// sequencer code, so the interrupt must be enabled from the start.
const tpu1TickGRA = 4409

// startBootTimers starts the two timers the main program relies on but never starts itself,
// TPU1 (sequencer tick) and TPU2 (housekeeping tick), as free running compare match A
// timers with their interrupts enabled, the way the boot block is assumed to leave them.
func (ch *Chip) startBootTimers() {
	t := ch.tpu
	for i, gra := range map[int]uint16{1: tpu1TickGRA, 2: tpu2TickGRA} {
		c := t.ch[i]
		c.regs[tTCR] = 0x21 // clear on TGRA, phi/4
		c.regs[tTIER] = 0x01
		c.tgr[0] = gra
		t.tstr |= 1 << uint(i)
	}
}

// Advance moves time forward by the given number of system clocks.
func (ch *Chip) Advance(states int) {
	if ch.tpu == nil {
		return
	}
	ch.tpu.pending += uint64(states)
	if ch.tpu.pending >= tpuBatch {
		ch.flushTPU()
	}
}

func (ch *Chip) flushTPU() {
	t := ch.tpu
	if t.pending == 0 {
		return
	}
	n := t.pending
	t.pending = 0
	for _, c := range t.ch {
		if t.tstr&(1<<c.bit) != 0 {
			c.advance(n)
			c.refresh()
		}
	}
	for _, s := range ch.sci {
		s.advance(n)
	}
}
