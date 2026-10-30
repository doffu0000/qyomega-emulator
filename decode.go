// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

// insEntry is one row of MAME's H8S/2000 opcode table.
type insEntry struct {
	slot  int
	val   uint32
	mask  uint32
	val0  uint32
	mask0 uint32
	name  string
	am1   int
	am2   int
	size  int
}

// Decoded is an instruction matched at some address.
type Decoded struct {
	E    *insEntry
	Op   uint32 // the opcode slot word the entry matched against
	Size int
}

// decode matches the instruction at pc using the bus for code fetch.
func decode(b *Bus, pc uint32) Decoded {
	f16 := func(a uint32) uint32 { return uint32(b.Read8(a))<<8 | uint32(b.Read8(a+1)) } // unaligned like the listing
	w0 := f16(pc)
	slots := [5]uint32{
		w0,
		f16(pc)<<16 | f16(pc+2),
		w0<<16 | f16(pc+4),
		w0<<16 | f16(pc+6),
		f16(pc+2)<<16 | f16(pc+4),
	}
	for i := range decodeTable {
		e := &decodeTable[i]
		if slots[e.slot]&e.mask == e.val && w0&e.mask0 == e.val0 {
			return Decoded{e, slots[e.slot], e.size}
		}
	}
	return Decoded{nil, w0, 2}
}
