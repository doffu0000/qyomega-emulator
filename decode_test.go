// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"testing"
)

// Every line of re/qy100_code.asm ("ADDR  hexbytes  text") must decode to the
// same length as the reference disassembler produced.
func TestDecodeLengthsMatchListing(t *testing.T) {
	b, err := NewUnitBus(testFirmware(t))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open("../re/qy100_code.asm")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	re := regexp.MustCompile(`^([0-9A-F]{6})\s+([0-9a-f]+)\s+(\S+)`)
	sc := bufio.NewScanner(f)
	n, bad := 0, 0
	for sc.Scan() {
		m := re.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		pc, _ := strconv.ParseUint(m[1], 16, 32)
		want := len(m[2]) / 2
		got := decode(b, uint32(pc))
		n++
		if got.Size != want || (m[3] == "illegal") != (got.E == nil) {
			bad++
			if bad < 10 {
				t.Errorf("%s: want %d bytes %q, got size %d", m[1], want, m[3], got.Size)
			}
		}
	}
	t.Logf("%d instructions checked, %d mismatches", n, bad)
	if bad > 0 {
		t.Fail()
	}
}
