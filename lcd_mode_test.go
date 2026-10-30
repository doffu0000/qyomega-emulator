// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import "testing"

// A selection is a black box in the graphic layer with the characters in the text layer.
// In EXOR mode (0x81, what the firmware leaves the controller in) the characters show
// light on the box; in OR mode (0x80) they would vanish into it.
func TestLCDModeCombinesLayers(t *testing.T) {
	l := &LCD{}
	l.TextHome, l.TextArea = 0x1000, 22
	l.GfxHome, l.GfxArea = 0, 22
	l.Disp = 0x9C
	for y := 0; y < 8; y++ {
		l.VRAM[y*22] = 0x3F // the whole 6 pixel cell is dark in the graphic layer
	}
	l.VRAM[0x1000] = 'A' - 0x20

	lit := 0
	for y := 0; y < 8; y++ {
		for x := 0; x < 6; x++ {
			if l.textPixel(x, y, 6) {
				lit++
			}
		}
	}
	if lit == 0 {
		t.Fatal("the test character has no lit pixels")
	}

	l.Mode = 0x81
	for y := 0; y < 8; y++ {
		for x := 0; x < 6; x++ {
			if got, want := l.Pixel(x, y, 6), !l.textPixel(x, y, 6); got != want {
				t.Fatalf("EXOR pixel %d,%d is %v, want %v", x, y, got, want)
			}
		}
	}

	l.Mode = 0x80
	for y := 0; y < 8; y++ {
		for x := 0; x < 6; x++ {
			if !l.Pixel(x, y, 6) {
				t.Fatalf("OR pixel %d,%d should be dark", x, y)
			}
		}
	}

	// with the graphic layer off only the text shows, whatever the mode
	l.Mode, l.Disp = 0x81, 0x94
	if got, want := l.Pixel(2, 3, 6), l.textPixel(2, 3, 6); got != want {
		t.Fatalf("text only pixel %v, want %v", got, want)
	}
}
