// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"bytes"
	"os"
	"testing"
)

// testFirmware returns the firmware image for a test that boots the unit, or skips the test
// when there is none (the image is not part of the source).
func testFirmware(t testing.TB) string {
	t.Helper()
	p := firmwarePath()
	if _, err := os.Stat(p); err != nil {
		t.Skip("no firmware image: set QYOMEGA_FIRMWARE or put sequencer_flash.bin next to the program")
	}
	return p
}

// The converter must reproduce, byte for byte, the image the Python tool made from Yamaha's update.
func TestImageFromUpdateMatchesReference(t *testing.T) {
	mid, err := os.ReadFile("../_QY100_v137.mid")
	if err != nil {
		t.Skip("update file not present")
	}
	want, err := os.ReadFile("../qy100_flash_v137.bin")
	if err != nil {
		t.Skip("reference image not present")
	}
	got, err := ImageFromUpdate(mid)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("converted image differs from the reference image")
	}
	if _, err := NormalizeFirmware(mid[:len(mid)/2]); err == nil {
		t.Fatal("a cut update file was accepted")
	}
	if _, err := NormalizeFirmware(make([]byte, imageSize)); err == nil {
		t.Fatal("a zero filled image was accepted")
	}
}
