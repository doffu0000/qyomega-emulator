// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
)

// T6963C style graphic LCD controller at 0x800000 (data) and 0x800001
// (command / status). The firmware init code at 0x49230 sends 0x80 (mode set),
// 0x40/0x41 (text home 0x1000, 22 columns), 0x42/0x43 (graphic home 0,
// 22 columns), 0x22 (offset), 0x24 (address pointer), then 0xB0 auto write of
// 8192 zero bytes, 0xB2, 0x9C (text and graphic on) and 0x81.
//
// Parameters are written to the data port BEFORE the command.

const (
	lcdData = 0x800000
	lcdCmd  = 0x800001
)

// LCD is the controller with its 64 KB display memory.
type LCD struct {
	VRAM                [65536]uint8
	adp                 uint16
	TextHome, TextArea  uint16
	GfxHome, GfxArea    uint16
	Offset              uint16
	Mode, Disp          uint8
	params              []uint8
	autoWrite, autoRead bool
	Commands            int      // commands received, to see activity
	Cmds                [256]int // how often each command byte was seen (not part of snapshots)
}

// AttachLCD maps the controller on the bus.
func AttachLCD(b *Bus) *LCD {
	l := &LCD{}
	b.Devs = append(b.Devs, &Dev{Name: "lcd", Base: lcdData, Sz: 2, R: l.read, W: l.write})
	return l
}

func (l *LCD) read(a uint32, size int) uint32 {
	var v uint32
	for i := 0; i < size; i++ {
		v = v<<8 | uint32(l.read8(a+uint32(i)))
	}
	return v
}

func (l *LCD) write(a uint32, size int, v uint32) {
	for i := 0; i < size; i++ {
		l.write8(a+uint32(i), uint8(v>>(8*uint(size-1-i))))
	}
}

func (l *LCD) read8(a uint32) uint8 {
	if a == lcdCmd {
		s := uint8(0x03) // command and data ready: the model is never busy
		if l.autoWrite {
			s |= 0x08
		}
		if l.autoRead {
			s |= 0x04
		}
		return s
	}
	if l.autoRead {
		v := l.VRAM[l.adp]
		l.adp++
		return v
	}
	return 0
}

func (l *LCD) write8(a uint32, v uint8) {
	if a == lcdData {
		if l.autoWrite {
			l.VRAM[l.adp] = v
			l.adp++
			return
		}
		l.params = append(l.params, v)
		if len(l.params) > 2 {
			l.params = l.params[len(l.params)-2:]
		}
		return
	}
	l.command(v)
}

// two returns the two parameter bytes (first, second) written before a command.
func (l *LCD) two() (uint8, uint8) {
	p := l.params
	switch len(p) {
	case 0:
		return 0, 0
	case 1:
		return p[0], 0
	}
	return p[len(p)-2], p[len(p)-1]
}

func (l *LCD) command(c uint8) {
	l.Commands++
	l.Cmds[c]++
	d1, d2 := l.two()
	word := uint16(d1) | uint16(d2)<<8
	n := len(l.params)
	l.params = l.params[:0]
	switch {
	case c == 0x22:
		l.Offset = word
	case c == 0x24:
		l.adp = word
	case c == 0x40:
		l.TextHome = word
	case c == 0x41:
		l.TextArea = word
	case c == 0x42:
		l.GfxHome = word
	case c == 0x43:
		l.GfxArea = word
	case c&0xF0 == 0x80:
		l.Mode = c
	case c&0xF0 == 0x90:
		l.Disp = c
	case c == 0xB0:
		l.autoWrite, l.autoRead = true, false
	case c == 0xB1:
		l.autoRead, l.autoWrite = true, false
	case c == 0xB2:
		l.autoWrite, l.autoRead = false, false
	case c >= 0xC0 && c <= 0xC5:
		l.dataCmd(c, d2, n)
	case c&0xF0 == 0xF0:
		bit := uint8(1) << (c & 7)
		if c&8 != 0 {
			l.VRAM[l.adp] |= bit
		} else {
			l.VRAM[l.adp] &^= bit
		}
	}
	// 0x21 cursor pointer, 0xA0-0xA7 cursor shape and 0xE0/0xE8 are accepted and ignored
}

// dataCmd handles the single byte write/read commands 0xC0..0xC5.
func (l *LCD) dataCmd(c, v uint8, n int) {
	switch c {
	case 0xC0, 0xC2, 0xC4:
		if n > 0 {
			// one data byte is sent before the command: it is the last parameter
			l.VRAM[l.adp] = v
			if n >= 2 {
				l.VRAM[l.adp] = v
			}
		}
	}
	switch c {
	case 0xC0, 0xC1:
		l.adp++
	case 0xC2, 0xC3:
		l.adp--
	}
}

// Pixel reports one pixel of the combined display: the graphic and text layers (each
// only when switched on in the display mode) are combined the way the mode set command
// says, OR (0x80), EXOR (0x81) or AND (0x83). The firmware ends its init in EXOR, which
// is what makes a selection show as light text on a dark box: the box is drawn in the
// graphic layer and the characters in the text layer. fontW is 6 or 8 pixels per byte.
func (l *LCD) Pixel(x, y, fontW int) bool {
	g := l.Disp&0x08 != 0 && l.gfxPixel(x, y, fontW)
	t := l.Disp&0x04 != 0 && l.textPixel(x, y, fontW)
	if l.Disp&0x0C != 0x0C { // one layer only
		return g || t
	}
	switch l.Mode & 7 {
	case 1:
		return g != t
	case 3:
		return g && t
	}
	return g || t
}

func (l *LCD) gfxPixel(x, y, fontW int) bool {
	if l.GfxArea == 0 {
		return false
	}
	a := int(l.GfxHome) + y*int(l.GfxArea) + x/fontW
	if a > 0xffff {
		return false
	}
	return l.VRAM[a]>>(uint(fontW-1-x%fontW))&1 != 0
}

// textPixel draws character cells of fontW x 8. Codes 0x00 to 0x5F are the
// internal ROM (ASCII minus 0x20, drawn with font5x7), codes 0x80 and up come
// from character RAM at offset*0x800 + code*8.
func (l *LCD) textPixel(x, y, fontW int) bool {
	if l.TextArea == 0 {
		return false
	}
	col, row := x/fontW, y/8
	a := int(l.TextHome) + row*int(l.TextArea) + col
	if a > 0xffff {
		return false
	}
	code := int(l.VRAM[a])
	gx, gy := x%fontW, y%8
	switch {
	case code < 0x5F:
		if gx < 5 && gy < 7 {
			return font5x7[code][gx]>>uint(gy)&1 != 0
		}
	case code >= 0x80:
		g := (int(l.Offset)<<11 + code*8 + gy) & 0xffff
		return l.VRAM[g]>>(uint(fontW-1-gx))&1 != 0
	}
	return false
}

// Width is the visible width in pixels for the font width.
func (l *LCD) Width(fontW int) int { return int(l.GfxArea) * fontW }

// Text renders the graphic layer with block characters, two pixel rows per line.
func (l *LCD) Text(fontW, rows int) string {
	w := l.Width(fontW)
	var sb strings.Builder
	for y := 0; y < rows; y += 2 {
		for x := 0; x < w; x++ {
			top, bot := l.Pixel(x, y, fontW), l.Pixel(x, y+1, fontW)
			switch {
			case top && bot:
				sb.WriteString("█")
			case top:
				sb.WriteString("▀")
			case bot:
				sb.WriteString("▄")
			default:
				sb.WriteString(" ")
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// Image renders the display scaled up by scale (black pixels on light grey).
func (l *LCD) Image(fontW, rows, scale int) *image.Gray {
	return l.ImageW(l.Width(fontW), fontW, rows, scale)
}

// lcdPanelWidth is the real panel width: the controller memory has 22 bytes of 6
// pixels per row (132) but the firmware only draws 128 columns and the last four
// are never lit.
const lcdPanelWidth = 128

// ImageW is Image cropped or padded to w pixels across.
func (l *LCD) ImageW(w, fontW, rows, scale int) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, w*scale, rows*scale))
	for y := 0; y < rows*scale; y++ {
		for x := 0; x < w*scale; x++ {
			if l.Pixel(x/scale, y/scale, fontW) {
				img.SetGray(x, y, color.Gray{0})
			} else {
				img.SetGray(x, y, color.Gray{230})
			}
		}
	}
	return img
}

// PNG writes the display scaled up by scale.
func (l *LCD) PNG(path string, fontW, rows, scale int) error {
	img := l.Image(fontW, rows, scale)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// TextLayer returns the text layer as characters (T6963C code = ASCII - 0x20),
// rows of TextArea columns.
func (l *LCD) TextLayer(rows int) string {
	var sb strings.Builder
	for r := 0; r < rows; r++ {
		for c := 0; c < int(l.TextArea); c++ {
			v := l.VRAM[(int(l.TextHome)+r*int(l.TextArea)+c)&0xffff]
			if v < 0x5f {
				sb.WriteByte(v + 0x20)
			} else {
				sb.WriteByte('.')
			}
		}
		sb.WriteString("|\n")
	}
	return sb.String()
}
