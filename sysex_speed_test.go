// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"fmt"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIsBulkModeSwitch(t *testing.T) {
	yes := []string{"F0 43 10 5F 10 00 00 01 F7", "F0 43 10 5F 10 00 00 00 F7"}
	no := []string{"F0 43 20 5F 13 00 00 F7", "F0 43 00 5F 01 13 12 7E 00 21 F7", "F0 43 10 4C 02 01 40 01 00 F7", "F0 F7"}
	for _, m := range yes {
		b, _ := hex.DecodeString(strings.ReplaceAll(m, " ", ""))
		if !isBulkModeSwitch(b) {
			t.Errorf("%s should be a bulk mode switch", m)
		}
	}
	for _, m := range no {
		b, _ := hex.DecodeString(strings.ReplaceAll(m, " ", ""))
		if isBulkModeSwitch(b) {
			t.Errorf("%s is not a bulk mode switch", m)
		}
	}
}

func TestExpectsReply(t *testing.T) {
	for _, c := range []struct {
		msg  string
		want bool
	}{
		{"F0 43 10 5F 10 00 00 01 F7", true},        // bulk mode switch, echoed
		{"F0 43 20 5F 13 00 00 F7", true},           // dump request
		{"F0 43 00 5F 01 13 12 7E 00 21 F7", false}, // bulk data block
		{"F0 7E 7F 09 01 F7", true},                 // anything else is treated like a request
		{"F0 F7", true},
	} {
		b, err := hex.DecodeString(strings.ReplaceAll(c.msg, " ", ""))
		if err != nil {
			t.Fatal(err)
		}
		if got := expectsReply(b); got != c.want {
			t.Errorf("%s: expectsReply = %v, want %v", c.msg, got, c.want)
		}
	}
}

// A pattern data block with no content (all zero data, a valid checksum), addressed to the selected
// slot (12 7E 00). The unit never answers data blocks, so a transfer must not wait for a reply
// after each one: 30 blocks used to take over a minute at 2 s each.
func dataBlockHex() string {
	const count = 147 // packed data bytes in a full block
	b := []byte{0x43, 0x00, 0x5F, count >> 7, count & 0x7F, 0x12, 0x7E, 0x00}
	b = append(b, make([]byte, count)...)
	sum := 0
	for _, v := range b[3:] {
		sum += int(v)
	}
	b = append(b, byte((-sum)&0x7F))
	out := "F0"
	for _, v := range b {
		out += fmt.Sprintf(" %02X", v)
	}
	return out + " F7"
}

func TestBulkBlocksAreNotWaitedOn(t *testing.T) {
	if _, err := os.Stat(snapshotFile); err != nil {
		t.Skip("no boot.snap (make one with: emu serve --fresh)")
	}
	if testing.Short() {
		t.Skip("takes about 10 seconds")
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

	body := "F0 43 10 5F 10 00 00 01 F7"
	blocks := 30
	for i := 0; i < blocks; i++ {
		body += " " + dataBlockHex()
	}
	body += " F0 43 10 5F 10 00 00 00 F7"

	// poll the progress while the transfer runs
	seen := make(chan int64, 1)
	done := make(chan struct{})
	go func() {
		var most int64
		for i := 0; i < 600; i++ {
			select {
			case <-done:
				seen <- most
				return
			default:
			}
			resp, err := http.Get(ts.URL + "/sysex/progress")
			if err == nil {
				var p struct{ Sent, Total int64 }
				json.NewDecoder(resp.Body).Decode(&p)
				resp.Body.Close()
				if p.Sent > most {
					most = p.Sent
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		seen <- most
	}()
	start := time.Now()
	resp, err := http.Post(ts.URL+"/sysex", "text/plain", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	took := time.Since(start)
	close(done)
	var res sysexReply
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.Sent != blocks+2 || res.Error != "" {
		t.Fatalf("sent %d of %d, error %q", res.Sent, blocks+2, res.Error)
	}
	t.Logf("%d messages took %v", res.Sent, took.Round(time.Millisecond))
	if took > 15*time.Second {
		t.Fatalf("%d data blocks took %v, the transfer is waiting for replies that never come", blocks, took)
	}
	if most := <-seen; most < 2 {
		t.Errorf("progress never moved (highest %d)", most)
	}
	if resp, err := http.Get(ts.URL + "/sysex/progress"); err == nil {
		var p struct {
			Total int64
			Busy  bool
		}
		json.NewDecoder(resp.Body).Decode(&p)
		resp.Body.Close()
		if p.Total != 0 || p.Busy {
			t.Errorf("progress after the transfer: %+v", p)
		}
	}
}
