// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"fmt"
	"os"
)

// Region is a plain memory block.
type Region struct {
	Name  string
	Base  uint32
	Data  []byte
	Write bool
}

// Bus is the 24 bit address space of the H8S/2000 as wired in the unit
// (see FINDINGS.md section 5/11 and the Ghidra project).
type Bus struct {
	Regions []*Region
	// Dev maps an address window to a device handler; checked before regions.
	Devs []*Dev
	// OnWrite, when set, sees every CPU write (debug watch points).
	OnWrite func(a uint32, size int, v uint32)

	// Unmapped logs accesses that hit no region or device, keyed by address and direction.
	Unmapped map[string]*Access
	PCFunc   func() uint32
}

type Dev struct {
	Name     string
	Base, Sz uint32
	R        func(a uint32, size int) uint32
	W        func(a uint32, size int, v uint32)
}

func NewBus() *Bus { return &Bus{} }

func (b *Bus) Add(name string, base uint32, size int, write bool) *Region {
	r := &Region{name, base, make([]byte, size), write}
	b.Regions = append(b.Regions, r)
	return r
}

func (b *Bus) noteUnmapped(a uint32, write bool, v uint32) {
	if b.Unmapped == nil {
		b.Unmapped = map[string]*Access{}
	}
	key := fmt.Sprintf("%06X/%v", a, write)
	if x, ok := b.Unmapped[key]; ok {
		x.Count++
		return
	}
	pc := uint32(0)
	if b.PCFunc != nil {
		pc = b.PCFunc()
	}
	b.Unmapped[key] = &Access{Addr: a, Write: write, Val: uint8(v), PC: pc, Count: 1}
}

func (b *Bus) find(a uint32) *Region {
	for _, r := range b.Regions {
		if a >= r.Base && a-r.Base < uint32(len(r.Data)) {
			return r
		}
	}
	return nil
}

func (b *Bus) Read8(a uint32) uint8 {
	a &= 0xFFFFFF
	for _, d := range b.Devs {
		if a >= d.Base && a-d.Base < d.Sz {
			return uint8(d.R(a, 1))
		}
	}
	if r := b.find(a); r != nil {
		return r.Data[a-r.Base]
	}
	b.noteUnmapped(a, false, 0)
	return 0xFF
}

func (b *Bus) Read16(a uint32) uint16 {
	a &= 0xFFFFFE
	for _, d := range b.Devs {
		if a >= d.Base && a-d.Base < d.Sz {
			return uint16(d.R(a, 2))
		}
	}
	if r := b.find(a); r != nil {
		o := a - r.Base
		return uint16(r.Data[o])<<8 | uint16(r.Data[o+1])
	}
	b.noteUnmapped(a, false, 0)
	return 0xFFFF
}

func (b *Bus) Read32(a uint32) uint32 {
	return uint32(b.Read16(a))<<16 | uint32(b.Read16(a+2))
}

func (b *Bus) Write8(a uint32, v uint8) {
	a &= 0xFFFFFF
	if b.OnWrite != nil {
		b.OnWrite(a, 1, uint32(v))
	}
	for _, d := range b.Devs {
		if a >= d.Base && a-d.Base < d.Sz {
			d.W(a, 1, uint32(v))
			return
		}
	}
	if r := b.find(a); r != nil {
		if r.Write {
			r.Data[a-r.Base] = v
		}
		return
	}
	b.noteUnmapped(a, true, uint32(v))
}

func (b *Bus) Write16(a uint32, v uint16) {
	a &= 0xFFFFFE
	if b.OnWrite != nil {
		b.OnWrite(a, 2, uint32(v))
	}
	for _, d := range b.Devs {
		if a >= d.Base && a-d.Base < d.Sz {
			d.W(a, 2, uint32(v))
			return
		}
	}
	if r := b.find(a); r != nil {
		if r.Write {
			o := a - r.Base
			r.Data[o] = byte(v >> 8)
			r.Data[o+1] = byte(v)
		}
		return
	}
	b.noteUnmapped(a, true, uint32(v))
}

func (b *Bus) Write32(a uint32, v uint32) {
	b.Write16(a, uint16(v>>16))
	b.Write16(a+2, uint16(v))
}

// NewUnitBus builds the memory map. flashPath is the 2 MB image from re/build_image.py.
func NewUnitBus(flashPath string) (*Bus, error) {
	fl, err := os.ReadFile(flashPath)
	if err != nil {
		return nil, err
	}
	if len(fl) != 0x200000 {
		return nil, fmt.Errorf("flash image is %d bytes, want 2097152", len(fl))
	}
	b := NewBus()
	r := b.Add("flash", 0, 0x200000, false)
	copy(r.Data, fl)
	b.Add("style_rom", 0x200000, 0x100000, false)
	b.Add("sram_sys_user", 0x400000, 0x20000, true)
	b.Add("ram2_buffer", 0x600000, 0x20000, true)
	b.Add("onchip", 0xFF0000, 0x10000, true) // placeholder: internal RAM and register space, device handlers are layered on top in step 7
	return b, nil
}

// The pattern loader (0x0672B8) reads the 546 byte header of preset style n (ids 0x80 and
// up) from 0x1E0000 + n*0x222. That block is not in the firmware image, so it reads 0xFF
// and pattern mode shows tempo 553 and a 32/4 signature. When the slots are blank they are
// filled with the firmware's own empty pattern header (120 BPM, 4/4, no tracks), the same
// template it copies into a new user pattern. The image file is not touched.
const (
	presetHeaderBase = 0x1E0000
	presetHeaderSize = 0x222
	presetStyles     = 128
	emptyHeaderAt    = 0x016DC4
)

func stubPresetStyleHeaders(flash []byte) {
	end := presetHeaderBase + presetStyles*presetHeaderSize
	for _, v := range flash[presetHeaderBase:end] {
		if v != 0xFF {
			return // real data is there
		}
	}
	tmpl := flash[emptyHeaderAt : emptyHeaderAt+presetHeaderSize]
	for i := 0; i < presetStyles; i++ {
		copy(flash[presetHeaderBase+i*presetHeaderSize:], tmpl)
	}
}
