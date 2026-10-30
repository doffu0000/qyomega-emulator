// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import "testing"

// BenchmarkRun measures the interpreter on the real firmware idle loop and key scan, from the
// boot snapshot: instructions per second is 1e9 / (ns per op / 1e6).
func BenchmarkRun(b *testing.B) {
	m, err := NewMachine(testFirmware(b))
	if err != nil {
		b.Fatal(err)
	}
	if err := m.LoadSnapshot(snapshotFile); err != nil {
		b.Skip("no boot.snap")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.Run(1000000); err != nil {
			b.Fatal(err)
		}
	}
}
