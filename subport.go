// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

// The byte port to the sound CPU at 0xE00000. The main CPU does not drive a tone generator
// directly: it sends the second SWX00B ordinary MIDI and XG messages here, a byte at a
// time. `F5 nn` selects the port (nn = 1 is parts 1 to 16, 2 is parts 17 to 32) and what
// follows is MIDI with running status and SysEx, for example `F5 01 90 3C 70` for a note on
// C3. There is no sound CPU in this model, so the bytes are only collected here and the
// front end turns them into sound.

const (
	subPortBase = 0xE00000
	subPortKeep = 1 << 20 // newest bytes kept
)

// SubPort collects the bytes written to the sound CPU port.
type SubPort struct {
	buf  []byte
	base uint64 // number of bytes dropped from the front of buf
}

// AttachSubPort maps the port on the bus. Reads give 0xFF (no sound CPU to answer, which
// is what an unmapped read gave before).
func AttachSubPort(b *Bus) *SubPort {
	sp := &SubPort{}
	b.Devs = append(b.Devs, &Dev{Name: "subcpu", Base: subPortBase, Sz: 2,
		R: func(a uint32, size int) uint32 { return 0xFFFFFFFF & sizeMask(size) },
		W: func(a uint32, size int, v uint32) { sp.write(uint8(v >> (8 * uint(size-1)))) },
	})
	return sp
}

func (sp *SubPort) write(v uint8) {
	sp.buf = append(sp.buf, v)
	if len(sp.buf) > 2*subPortKeep {
		drop := len(sp.buf) - subPortKeep
		sp.buf = append([]byte(nil), sp.buf[drop:]...)
		sp.base += uint64(drop)
	}
}

// Since returns the bytes from absolute offset from on, and the offset after them. An offset
// older than what is kept starts at the oldest byte kept, one past the end at the end.
func (sp *SubPort) Since(from uint64) ([]byte, uint64) {
	end := sp.base + uint64(len(sp.buf))
	if from < sp.base {
		from = sp.base
	}
	if from > end {
		from = end
	}
	return append([]byte(nil), sp.buf[from-sp.base:]...), end
}
