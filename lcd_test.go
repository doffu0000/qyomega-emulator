// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import "testing"

func lcdSend(l *LCD, cmd uint8, data ...uint8) {
	for _, d := range data {
		l.write8(lcdData, d)
	}
	l.write8(lcdCmd, cmd)
}

func TestLCDRegistersAndAutoWrite(t *testing.T) {
	l := &LCD{}
	lcdSend(l, 0x40, 0x00, 0x10)
	lcdSend(l, 0x41, 0x16, 0x00)
	lcdSend(l, 0x42, 0x00, 0x00)
	lcdSend(l, 0x43, 0x16, 0x00)
	lcdSend(l, 0x22, 0x03, 0x00)
	lcdSend(l, 0x24, 0x34, 0x12)
	if l.TextHome != 0x1000 || l.TextArea != 22 || l.GfxHome != 0 || l.GfxArea != 22 || l.Offset != 3 || l.adp != 0x1234 {
		t.Fatalf("%+v", l)
	}
	if l.read8(lcdCmd) != 0x03 {
		t.Fatal("idle status must be 3")
	}
	lcdSend(l, 0xB0)
	if l.read8(lcdCmd)&0x0B != 0x0B {
		t.Fatal("auto write ready bit expected")
	}
	for i := 0; i < 4; i++ {
		l.write8(lcdData, uint8(0xA0+i))
	}
	lcdSend(l, 0xB2)
	if l.VRAM[0x1234] != 0xA0 || l.VRAM[0x1237] != 0xA3 || l.adp != 0x1238 {
		t.Fatalf("auto write: %x %x adp=%x", l.VRAM[0x1234], l.VRAM[0x1237], l.adp)
	}
	if l.read8(lcdCmd) != 0x03 {
		t.Fatal("auto mode must end with B2")
	}
}

func TestLCDPixelsAndBitCommands(t *testing.T) {
	l := &LCD{GfxArea: 22, Disp: 0x9C}
	l.VRAM[0] = 0x21 // 6 pixel font: bits 5..0 are pixels 0..5, bit 5 leftmost
	if !l.Pixel(0, 0, 6) || l.Pixel(1, 0, 6) || !l.Pixel(5, 0, 6) {
		t.Fatal("6 pixel mapping")
	}
	if l.Pixel(0, 1, 6) {
		t.Fatal("second row is the next 22 bytes")
	}
	l.VRAM[22] = 0x80
	if !l.Pixel(0, 1, 8) {
		t.Fatal("8 pixel mapping")
	}
	lcdSend(l, 0x24, 0x05, 0x00)
	lcdSend(l, 0xF8|3) // set bit 3 of the byte at ADP
	if l.VRAM[5] != 0x08 {
		t.Fatalf("bit set: %x", l.VRAM[5])
	}
	lcdSend(l, 0xF0|3) // reset it
	if l.VRAM[5] != 0 {
		t.Fatal("bit reset")
	}
}

func TestLCDInitTableAsSentByFirmware(t *testing.T) {
	// the byte stream of the init table at 0x12534E replayed through the same protocol
	l := &LCD{}
	steps := []struct {
		cmd  uint8
		data []uint8
	}{
		{0x80, nil}, {0x40, []uint8{0, 0x10}}, {0x41, []uint8{0x16, 0}},
		{0x42, []uint8{0, 0}}, {0x43, []uint8{0x16, 0}}, {0x22, []uint8{3, 0}}, {0x24, []uint8{0, 0}},
	}
	for _, s := range steps {
		lcdSend(l, s.cmd, s.data...)
	}
	if l.GfxArea != 22 || l.TextHome != 0x1000 || l.Mode != 0x80 {
		t.Fatalf("%+v", l)
	}
}

func TestLCDTextLayerAndCGRAM(t *testing.T) {
	l := &LCD{TextHome: 0x1000, TextArea: 22, Disp: 0x94, Offset: 3} // text on, graphic off
	l.VRAM[0x1000] = 'A' - 0x20
	// the letter A: first column of the glyph is 0x7C, so rows 2..6 are lit in column 0
	if l.Pixel(0, 0, 6) || !l.Pixel(0, 2, 6) {
		t.Fatalf("glyph A column 0: %v %v", l.Pixel(0, 0, 6), l.Pixel(0, 2, 6))
	}
	// custom character from CG RAM: code 0x80 lives at offset*0x800 + 0x80*8
	l.VRAM[0x1001] = 0x80
	l.VRAM[3<<11+0x80*8+1] = 0x21
	if !l.Pixel(6, 1, 6) || l.Pixel(7, 1, 6) || !l.Pixel(11, 1, 6) {
		t.Fatal("CG RAM glyph")
	}
	l.Disp = 0x98 // graphic only: text must vanish
	if l.Pixel(0, 2, 6) {
		t.Fatal("text layer must be off")
	}
}
