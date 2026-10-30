// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// waitFor polls the machine under the server lock until cond is true.
func waitFor(t *testing.T, s *Server, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		s.mu.Lock()
		ok := cond()
		s.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A reset is a power cycle: the unit boots again (its cycle counter restarts), held keys are
// let go, and the memory is kept, so there is no Factory Set popup. ?factory=1 blanks the
// memory, which makes the firmware run Factory Set and wait for a key.
func TestResetEndpoint(t *testing.T) {
	if _, err := os.Stat(snapshotFile); err != nil {
		t.Skip("no boot.snap (make one with: emu serve --fresh)")
	}
	m := newTestMachine(t)
	if err := m.LoadSnapshot(snapshotFile); err != nil {
		t.Fatal(err)
	}
	s := NewServer(m)
	go s.Run()
	defer s.Stop()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	if g, _ := http.Get(ts.URL + "/reset"); g.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /reset returned %d", g.StatusCode)
	}
	post := func(path string) int {
		resp, err := http.Post(ts.URL+path, "text/plain", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// the unit has run for a while and a key is held
	time.Sleep(300 * time.Millisecond)
	s.mu.Lock()
	before := m.CPU.Cycles
	s.mu.Unlock()
	post("/key?name=shift&down=1")

	if code := post("/reset"); code != http.StatusNoContent {
		t.Fatalf("reset returned %d", code)
	}
	s.mu.Lock()
	restarted, rows := m.CPU.Cycles < before, m.Keys.rows
	s.mu.Unlock()
	if !restarted {
		t.Fatal("reset should restart the cycle counter")
	}
	if rows != [8]uint8{} {
		t.Fatalf("keys still held after reset: %v", rows)
	}
	// it boots to a normal screen with its memory, no Factory Set
	waitFor(t, s, "the unit to boot to a screen", func() bool {
		return m.CPU.Cycles > 14000000 && m.Bus.Read8(0x4084B5) != 0
	})
	s.mu.Lock()
	popup, err := m.Bus.Read8(0x413269), s.err
	s.mu.Unlock()
	if popup != 0 || err != nil {
		t.Fatalf("after a reset with memory kept: popup %02X, error %v", popup, err)
	}

	if code := post("/reset?factory=1"); code != http.StatusNoContent {
		t.Fatalf("factory reset returned %d", code)
	}
	waitFor(t, s, "the Factory Set popup", func() bool { return m.Bus.Read8(0x413269) == 0x0D })
}
