// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import "testing"

func TestSCITransmit(t *testing.T) {
	c, ch := chipCPU()
	s := ch.sci[1]               // SCI1 at 0xFFFF80
	c.Bus.Write8(0xFFFF81, 16)   // BRR: about 31250 baud at 16.9344 MHz
	c.Bus.Write8(0xFFFF82, 0xA0) // TIE | TE
	if c.Bus.Read8(0xFFFF84) != 0x84 {
		t.Fatalf("SSR reset value %02x", c.Bus.Read8(0xFFFF84))
	}
	if c.pending[86] == 0 {
		t.Fatal("TXI should be pending while TDRE is set and TIE on")
	}
	c.Bus.Write8(0xFFFF83, 0x90)                       // TDR
	c.Bus.Write8(0xFFFF84, c.Bus.Read8(0xFFFF84)&0x7F) // clear TDRE: start
	if len(s.TxLog) != 1 || s.TxLog[0] != 0x90 {
		t.Fatalf("tx log %v", s.TxLog)
	}
	if c.Bus.Read8(0xFFFF84)&0x04 != 0 {
		t.Fatal("TEND must be 0 while the byte shifts out")
	}
	ch.Advance(10 * 17 * 32) // one byte time at BRR=16
	ch.flushTPU()
	if c.Bus.Read8(0xFFFF84)&0x04 == 0 {
		t.Fatal("TEND should be set after a byte time")
	}
}

func TestSCIReceive(t *testing.T) {
	c, ch := chipCPU()
	s := ch.sci[0]
	c.Bus.Write8(0xFFFF79, 16)
	c.Bus.Write8(0xFFFF7A, 0x50) // RIE | RE
	s.Inject(0xF0)
	s.Inject(0x43)
	if c.Bus.Read8(0xFFFF7C)&0x40 != 0 {
		t.Fatal("nothing has arrived yet")
	}
	ch.Advance(10 * 17 * 32)
	ch.flushTPU()
	if c.Bus.Read8(0xFFFF7C)&0x40 == 0 || c.pending[81] == 0 {
		t.Fatal("RDRF and RXI expected after one byte time")
	}
	if c.Bus.Read8(0xFFFF7D) != 0xF0 {
		t.Fatal("RDR")
	}
	c.Bus.Write8(0xFFFF7C, c.Bus.Read8(0xFFFF7C)&^0x40) // clear RDRF
	if c.pending[81] != 0 {
		t.Fatal("clearing RDRF lowers RXI")
	}
	ch.Advance(10 * 17 * 32)
	ch.flushTPU()
	if c.Bus.Read8(0xFFFF7D) != 0x43 {
		t.Fatal("second byte")
	}
	// an unread byte followed by another is an overrun
	s.Inject(0x01)
	ch.Advance(10 * 17 * 32)
	ch.flushTPU()
	if c.Bus.Read8(0xFFFF7C)&0x20 == 0 {
		t.Fatal("ORER expected")
	}
}

func TestSCIReceiverOffHoldsBytes(t *testing.T) {
	c, ch := chipCPU()
	s := ch.sci[2]
	s.Inject(0x55)
	ch.Advance(100000)
	ch.flushTPU()
	if c.Bus.Read8(0xFFFF8C)&0x40 != 0 {
		t.Fatal("receiver is off")
	}
	c.Bus.Write8(0xFFFF8A, 0x10) // RE
	ch.Advance(100000)
	ch.flushTPU()
	if c.Bus.Read8(0xFFFF8C)&0x40 == 0 || c.Bus.Read8(0xFFFF8D) != 0x55 {
		t.Fatal("byte should arrive once RE is set")
	}
}
