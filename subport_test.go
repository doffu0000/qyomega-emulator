// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"testing"
)

func TestSubPortSince(t *testing.T) {
	sp := &SubPort{}
	for _, b := range []byte{1, 2, 3, 4} {
		sp.write(b)
	}
	if d, next := sp.Since(0); !bytes.Equal(d, []byte{1, 2, 3, 4}) || next != 4 {
		t.Fatalf("Since(0) = %v, %d", d, next)
	}
	if d, next := sp.Since(3); !bytes.Equal(d, []byte{4}) || next != 4 {
		t.Fatalf("Since(3) = %v, %d", d, next)
	}
	if d, next := sp.Since(99); len(d) != 0 || next != 4 {
		t.Fatalf("Since(99) = %v, %d", d, next)
	}
	// old bytes are dropped but offsets stay absolute
	big := &SubPort{}
	for i := 0; i < 2*subPortKeep+10; i++ {
		big.write(byte(i))
	}
	if big.base == 0 || len(big.buf) > 2*subPortKeep {
		t.Fatalf("buffer not trimmed: base %d len %d", big.base, len(big.buf))
	}
	d, next := big.Since(0)
	if next != uint64(2*subPortKeep+10) || uint64(len(d))+big.base != next {
		t.Fatalf("offsets after trimming: len %d base %d next %d", len(d), big.base, next)
	}
}

// A keyboard note reaches the sound CPU port as MIDI, the way the real main CPU sends it.
func TestKeyboardNoteReachesSoundPort(t *testing.T) {
	if _, err := os.Stat(snapshotFile); err != nil {
		t.Skip("no boot.snap (make one with: emu serve --fresh)")
	}
	m := newTestMachine(t)
	if err := m.LoadSnapshot(snapshotFile); err != nil {
		t.Fatal(err)
	}
	m.Run(2000000)
	_, from := m.Sub.Since(0)
	m.Keys.Press("k:C3", true)
	m.Run(3000000)
	m.Keys.Press("k:C3", false)
	m.Run(3000000)
	got, _ := m.Sub.Since(from)
	want := []byte{0xF5, 0x01, 0x90, 0x3C, 0x70, 0x90, 0x3C, 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("sound port got % X, want % X", got, want)
	}
}

func TestTGEndpoint(t *testing.T) {
	m := newTestMachine(t)
	s := NewServer(m)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	for _, b := range []byte{0xF5, 0x01, 0x90, 0x3C, 0x70} {
		m.Sub.write(b)
	}
	get := func(q string) ([]byte, http.Header) {
		resp, err := http.Get(ts.URL + "/tg?" + q)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return b, resp.Header
	}
	// a client without an epoch gets everything
	d, h := get("epoch=&from=3")
	if len(d) != 5 || h.Get("X-QY-Next") != "5" || h.Get("X-QY-Epoch") != "0" {
		t.Fatalf("first poll: % X %v", d, h)
	}
	// with the right epoch only the new bytes
	m.Sub.write(0x90)
	d, h = get("epoch=0&from=5")
	if !bytes.Equal(d, []byte{0x90}) || h.Get("X-QY-Next") != "6" {
		t.Fatalf("second poll: % X %v", d, h)
	}
	// a reset changes the epoch and the stream starts over
	if resp, err := http.Post(ts.URL+"/reset", "text/plain", nil); err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reset: %v %v", resp, err)
	}
	d, h = get("epoch=0&from=6")
	if h.Get("X-QY-Epoch") != "1" || h.Get("X-QY-Next") != strconv.Itoa(len(d)) {
		t.Fatalf("after reset: epoch %s next %s len %d", h.Get("X-QY-Epoch"), h.Get("X-QY-Next"), len(d))
	}
	// the synth script is served
	resp, _ := http.Get(ts.URL + "/synth.js")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte("StreamParser")) {
		t.Fatalf("synth.js: %d", resp.StatusCode)
	}
}

// The page's MIDI and XG parser is plain JavaScript; run its checks when node is installed.
func TestSynthParserInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	out, err := exec.Command(node, "synth_test.js").CombinedOutput()
	if err != nil {
		t.Fatalf("synth_test.js failed:\n%s", out)
	}
}
