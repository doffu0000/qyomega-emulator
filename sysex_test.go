// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestParseSysexBody(t *testing.T) {
	want := []byte{0xF0, 0x43, 0x10, 0x5F, 0x10, 0x00, 0x00, 0x01, 0xF7}
	for _, in := range []string{
		"F0 43 10 5F 10 00 00 01 F7",
		"f0,43,10,5f,10,00,00,01,f7\r\n",
		"0xF0 0x43 0x10 0x5F 0x10 0x00 0x00 0x01 0xF7",
	} {
		msgs, err := parseSysexBody([]byte(in))
		if err != nil || len(msgs) != 1 || !bytes.Equal(msgs[0], want) {
			t.Errorf("%q: %x %v", in, msgs, err)
		}
	}
	// raw file, two messages, junk between them
	raw := append(append(append([]byte{}, want...), 0xFE), want...)
	msgs, err := parseSysexBody(raw)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("raw: %x %v", msgs, err)
	}
	for _, bad := range []string{"", "hello", "F0 43 10", "F0 GG F7", "43 10 5F"} {
		if _, err := parseSysexBody([]byte(bad)); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestSysexEndpointRefusals(t *testing.T) {
	s := NewServer(newTestMachine(t))
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	if g, _ := http.Get(ts.URL + "/sysex"); g.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET returned %d", g.StatusCode)
	}
	resp, _ := http.Post(ts.URL+"/sysex", "text/plain", strings.NewReader("not sysex"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("junk returned %d", resp.StatusCode)
	}
	s.sysexBusy.Store(true)
	resp, _ = http.Post(ts.URL+"/sysex", "text/plain", strings.NewReader("F0 43 20 5F 13 00 00 F7"))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a second transfer returned %d, want 409", resp.StatusCode)
	}
}

// Bulk mode OFF used to leave the firmware in a loop waiting for the event counter at
// 0x409B35, which only the TPU2 tick handler drains, and the unit never answered again.
func TestTransferAfterBulkModeOff(t *testing.T) {
	if _, err := os.Stat(snapshotFile); err != nil {
		t.Skip("no boot.snap (make one with: emu serve --fresh)")
	}
	m := newTestMachine(t)
	if err := m.LoadSnapshot(snapshotFile); err != nil {
		t.Fatal(err)
	}
	on := []byte{0xF0, 0x43, 0x10, 0x5F, 0x10, 0x00, 0x00, 0x01, 0xF7}
	off := []byte{0xF0, 0x43, 0x10, 0x5F, 0x10, 0x00, 0x00, 0x00, 0xF7}
	req := []byte{0xF0, 0x43, 0x20, 0x5F, 0x13, 0x00, 0x00, 0xF7}
	dumps := 0
	for round := 0; round < 2; round++ {
		for _, msg := range [][]byte{on, req, off} {
			out, err := m.SendSysEx(msg, 600000)
			if err != nil {
				t.Fatal(err)
			}
			for _, x := range parseSysEx(out) {
				if len(x) > 8 && x[2] == 0x00 && x[3] == 0x5F && x[6] == 0x13 {
					dumps++
				}
			}
		}
		if err := m.Run(5000000); err != nil {
			t.Fatal(err)
		}
	}
	if dumps != 2 {
		t.Fatalf("got %d setup dumps in two rounds of ON, request, OFF; want 2", dumps)
	}
	if v := m.Bus.Read8(0x409B35); v > 0x23 {
		t.Fatalf("event counter stuck at %02X", v)
	}
}

// The restored boot snapshot answers bulk mode ON (echo) and a SETUP dump request
// through the HTTP endpoint, as the toolkit would. Needs boot.snap; skipped without it.
func TestSysexEndpointSetupDump(t *testing.T) {
	if _, err := os.Stat(snapshotFile); err != nil {
		t.Skip("no boot.snap (make one with: emu snap 130000000 boot.snap)")
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

	body := "F0 43 10 5F 10 00 00 01 F7  F0 43 20 5F 13 00 00 F7"
	resp, err := http.Post(ts.URL+"/sysex", "text/plain", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res sysexReply
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", res)
	if res.Error != "" || res.Sent != 2 {
		t.Fatalf("sent %d, error %q", res.Sent, res.Error)
	}
	var dump string
	for _, r := range res.Replies {
		if strings.HasPrefix(r, "F0 43 00 5F 00 25 13") {
			dump = r
		}
	}
	if dump == "" {
		t.Fatalf("no setup dump among %v", res.Replies)
	}
}
