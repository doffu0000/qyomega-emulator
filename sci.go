// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

// Serial communication interfaces SCI0..SCI2 in asynchronous mode, H8S/2655
// register layout. The MIDI ports of the unit hang off these (31250 baud).
//
// Transmit: software writes TDR and then clears TDRE. The byte moves to the
// shift register at once (TDRE returns to 1, so TXI can fire again) and TEND
// stays 0 for one byte time. Every transmitted byte goes to Tx and TxLog.
// Receive: Inject queues bytes that arrive one byte time apart into RDR.

const (
	sciSMR = iota
	sciBRR
	sciSCR
	sciTDR
	sciSSR
	sciRDR
	sciSCMR
)

type sciChan struct {
	name   string
	base   uint32
	vec    int // ERI; RXI, TXI, TEI follow
	chip   *Chip
	regs   [8]uint8
	tdr    uint8
	rdr    uint8
	ssr    uint8
	txLeft uint64 // clocks until TEND
	rxLeft uint64 // clocks until the next queued byte lands
	rxq    []uint8

	// Tx is called with each transmitted byte; TxLog keeps them all.
	Tx    func(b uint8)
	TxLog []uint8
	// TxFresh is a second copy of the transmitted bytes that only the server drains, for the live
	// MIDI output. It is capped so a program that never drains it cannot grow it without limit.
	TxFresh []uint8
}

const sciBits = 10 // start, 8 data, stop

// externalBitClocks is one bit at 31250 baud in system clocks (16.9344 MHz / 31250).
const externalBitClocks = 542

func (c *sciChan) bitClocks() uint64 {
	if c.regs[sciSCR]&3 == 2 {
		// external clock (SCR CKE1): 16 x the bit rate from the board. The firmware picks
		// the source through 0xFFE019; both are assumed to be 31250 baud for now.
		return externalBitClocks
	}
	n := uint(c.regs[sciSMR] & 3)
	return 32 << (2 * n) * (uint64(c.regs[sciBRR]) + 1)
}

func (c *sciChan) byteClocks() uint64 { return sciBits * c.bitClocks() }

func (ch *Chip) initSCI() {
	for i, base := range []uint32{0xFFFF78, 0xFFFF80, 0xFFFF88} {
		c := &sciChan{name: "SCI" + string(rune('0'+i)), base: base, vec: 80 + 4*i, chip: ch, ssr: 0x84}
		c.regs[sciBRR] = 0xFF
		c.regs[sciSCMR] = 0xF2
		ch.sci[i] = c
		for off := uint32(0); off < 7; off++ {
			off := off
			a := base + off
			ch.hr[a] = func() uint8 { ch.flushTPU(); return c.read(off) }
			ch.hw[a] = func(v uint8) { ch.flushTPU(); c.write(off, v) }
		}
	}
}

func (c *sciChan) read(off uint32) uint8 {
	switch off {
	case sciTDR:
		return c.tdr
	case sciRDR:
		return c.rdr
	case sciSSR:
		return c.ssr
	}
	return c.regs[off]
}

func (c *sciChan) write(off uint32, v uint8) {
	switch off {
	case sciTDR:
		c.tdr = v
	case sciRDR:
		// read only
	case sciSCR:
		c.regs[sciSCR] = v
		c.refresh()
	case sciSSR:
		old := c.ssr
		flags := uint8(0xF8)                      // TDRE RDRF ORER FER PER clear by writing 0
		c.ssr = old&^flags | old&flags&v | v&0x01 // MPBT is writable
		if old&0x80 != 0 && v&0x80 == 0 && c.regs[sciSCR]&0x20 != 0 {
			c.transmit()
		}
		c.refresh()
	default:
		c.regs[off] = v
	}
}

func (c *sciChan) transmit() {
	c.ssr |= 0x80 // TDRE: the shift register took the byte
	c.ssr &^= 0x04
	c.txLeft = c.byteClocks()
	c.TxLog = append(c.TxLog, c.tdr)
	if c.name == "SCI1" {
		if len(c.TxFresh) >= 1<<16 {
			c.TxFresh = c.TxFresh[:0]
		}
		c.TxFresh = append(c.TxFresh, c.tdr)
	}
	if c.Tx != nil {
		c.Tx(c.tdr)
	}
}

// Inject queues a received byte (RE must be set for it to be taken).
func (c *sciChan) Inject(b uint8) {
	if len(c.rxq) == 0 && c.rxLeft == 0 {
		c.rxLeft = c.byteClocks()
	}
	c.rxq = append(c.rxq, b)
}

func (c *sciChan) advance(n uint64) {
	if c.txLeft > 0 {
		if n >= c.txLeft {
			c.txLeft = 0
			c.ssr |= 0x04 // TEND
		} else {
			c.txLeft -= n
		}
	}
	for len(c.rxq) > 0 && n > 0 {
		if c.regs[sciSCR]&0x10 == 0 { // receiver off: hold the byte
			break
		}
		if n < c.rxLeft {
			c.rxLeft -= n
			break
		}
		n -= c.rxLeft
		if c.ssr&0x40 != 0 {
			c.ssr |= 0x20 // overrun
		} else {
			c.rdr = c.rxq[0]
			c.ssr |= 0x40
		}
		c.rxq = c.rxq[1:]
		c.rxLeft = c.byteClocks()
	}
	c.refresh()
}

func (c *sciChan) refresh() {
	scr := c.regs[sciSCR]
	set := func(vec int, on bool) {
		if on {
			c.chip.cpu.Raise(vec, 1)
		} else {
			c.chip.cpu.Lower(vec)
		}
	}
	set(c.vec, scr&0x40 != 0 && c.ssr&0x38 != 0)   // ERI: RIE and (ORER|FER|PER)
	set(c.vec+1, scr&0x40 != 0 && c.ssr&0x40 != 0) // RXI
	set(c.vec+2, scr&0x80 != 0 && c.ssr&0x80 != 0) // TXI
	set(c.vec+3, scr&0x04 != 0 && c.ssr&0x04 != 0) // TEI
}
