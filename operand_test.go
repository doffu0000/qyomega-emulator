// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The text of every instruction must equal the reference listing, which
// proves register numbers, displacements, immediates and branch targets.
func TestOperandTextMatchesListing(t *testing.T) {
	b, err := NewUnitBus(testFirmware(t))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open("../re/qy100_code.asm")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	re := regexp.MustCompile(`^([0-9A-F]{6})  ([0-9a-f]+)\s+(.*)$`)
	sc := bufio.NewScanner(f)
	n, bad := 0, 0
	for sc.Scan() {
		m := re.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		pc, _ := strconv.ParseUint(m[1], 16, 32)
		want := strings.TrimRight(m[3], " ")
		got := Decode(b, uint32(pc)).String()
		n++
		if got != want {
			bad++
			if bad <= 15 {
				t.Errorf("%s: want %q got %q", m[1], want, got)
			}
		}
	}
	t.Logf("%d instructions, %d text mismatches", n, bad)
	if bad > 0 {
		t.Fail()
	}
}
