// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"bytes"
	"encoding/json"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoopbackOnly(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:8100", "localhost:9000", "[::1]:8100"} {
		if _, err := loopbackAddr(ok); err != nil {
			t.Errorf("%s must be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{"0.0.0.0:8100", "192.168.1.5:8100", ":8100", "example.com:80", "8100"} {
		if _, err := loopbackAddr(bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}

func TestParseServeArgs(t *testing.T) {
	addr, fresh, err := parseServeArgs(nil)
	if err != nil || addr != defaultServeAddr || fresh {
		t.Fatalf("defaults: %q %v %v", addr, fresh, err)
	}
	addr, fresh, err = parseServeArgs([]string{"--fresh", "localhost:9001"})
	if err != nil || addr != "127.0.0.1:9001" || !fresh {
		t.Fatalf("%q %v %v", addr, fresh, err)
	}
	if _, _, err := parseServeArgs([]string{"--fresh", "0.0.0.0:8100"}); err == nil {
		t.Fatal("a non loopback address must be refused")
	}
}

func TestScreenPNGAndIndex(t *testing.T) {
	m := newTestMachine(t)
	// minimal display set up, as the firmware init does: graphic home 0, 22 columns, graphic on
	m.LCD.GfxArea, m.LCD.Disp = 22, 0x98
	m.LCD.VRAM[0] = 0x3F // first 6 pixels of the top row lit
	s := NewServer(m)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/screen.png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content type %q", ct)
	}
	img, err := png.Decode(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 512 || b.Dy() != 256 {
		t.Fatalf("size %v, want 512x256 (128 x 64 pixels at 4x)", b)
	}
	dark := func(x, y int) bool { r, _, _, _ := img.At(x, y).RGBA(); return r < 0x8000 }
	if !dark(0, 0) || !dark(23, 3) || dark(24, 0) || dark(0, 4) {
		t.Fatal("pixels are not where the video memory says")
	}

	page, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !bytes.Contains(body, []byte("/screen.png")) {
		t.Fatal("the page does not load the screen image")
	}
	if nf, _ := http.Get(ts.URL + "/nothing"); nf.StatusCode != 404 {
		t.Fatalf("unknown path returned %d", nf.StatusCode)
	}
}

func TestStatusReportsLED(t *testing.T) {
	m := newTestMachine(t)
	m.Keys.LED = 0xA5
	ts := httptest.NewServer(NewServer(m).Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st statusJSON
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if st.LED != 0xA5 {
		t.Fatalf("led %02X, want A5", st.LED)
	}
}

func TestKeyEndpoint(t *testing.T) {
	m := newTestMachine(t)
	s := NewServer(m)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	post := func(q string) int {
		resp, err := http.Post(ts.URL+"/key?"+q, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	pressed := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, r := range m.Keys.rows {
			if r != 0 {
				return true
			}
		}
		return false
	}

	if g, _ := http.Get(ts.URL + "/key?name=EXIT&down=1"); g.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET returned %d", g.StatusCode)
	}
	if c := post("name=NOSUCH&down=1"); c != http.StatusBadRequest {
		t.Fatalf("unknown key returned %d", c)
	}
	if pressed() {
		t.Fatal("a refused request pressed something")
	}
	if c := post("name=EXIT&down=1"); c != 200 || !pressed() {
		t.Fatalf("press returned %d, pressed %v", c, pressed())
	}
	// a release sent at once is held back until minKeyHold has passed
	if c := post("name=EXIT&down=0"); c != 200 || !pressed() {
		t.Fatal("the key was released before the minimum hold time")
	}
	time.Sleep(minKeyHold + 100*time.Millisecond)
	if pressed() {
		t.Fatal("the key was never released")
	}
	// pressing again while a release is pending cancels the release
	post("name=EXIT&down=1")
	post("name=EXIT&down=0")
	post("name=EXIT&down=1")
	time.Sleep(minKeyHold + 100*time.Millisecond)
	if !pressed() {
		t.Fatal("the pending release was not cancelled")
	}
	// a long hold is released straight away
	time.Sleep(minKeyHold)
	post("name=EXIT&down=0")
	if pressed() {
		t.Fatal("a key held for a long time must be released at once")
	}

	resp, err := http.Get(ts.URL + "/keys")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	json.NewDecoder(resp.Body).Decode(&names)
	resp.Body.Close()
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	if len(names) < 40 || !have["EXIT"] || !have["PLAY"] || !have["k:C3"] {
		t.Fatalf("key list looks wrong: %v", names)
	}
}

func TestServerRunsMachineAndReportsStatus(t *testing.T) {
	s := NewServer(newTestMachine(t))
	go s.Run()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	get := func() statusJSON {
		resp, err := http.Get(ts.URL + "/status")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var st statusJSON
		if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	a := get()
	time.Sleep(400 * time.Millisecond)
	b := get()
	s.Stop()
	if b.Cycles <= a.Cycles {
		t.Fatalf("machine did not advance: %d then %d", a.Cycles, b.Cycles)
	}
	if b.Error != "" {
		t.Fatal(b.Error)
	}
	// 0.4 s of real time must not run more than about 0.4 s worth of instructions
	if b.Cycles-a.Cycles > uint64(0.8*realTimeRate) {
		t.Fatalf("not throttled: %d instructions in 0.4 s", b.Cycles-a.Cycles)
	}
}
