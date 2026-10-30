// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
)

// The program ships without Yamaha's firmware. gate is what the HTTP port serves: until the unit is
// up it shows a setup page where the user supplies their own firmware (Yamaha's v1.37 update .mid
// or a 2 MiB flash image), and once the unit is running it hands every request to the real server.

//go:embed setup.html
var setupHTML []byte

// maxFirmwareUpload bounds what /firmware reads. Yamaha's update file is about 2.4 MB.
const maxFirmwareUpload = 8 << 20

type gate struct {
	fresh bool

	mu    sync.Mutex
	state string // "needfirmware", "booting", "ready", "error"
	msg   string
	inner http.Handler
}

func newGate(fresh bool) *gate { return &gate{fresh: fresh, state: "needfirmware"} }

// start boots the unit in the background and switches the gate over when it is up.
func (g *gate) start() {
	g.mu.Lock()
	g.state, g.msg = "booting", ""
	g.mu.Unlock()
	go func() {
		m, err := loadMachine(g.fresh)
		g.mu.Lock()
		defer g.mu.Unlock()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			g.state, g.msg = "error", err.Error()
			return
		}
		s := NewServer(m)
		go s.Run()
		g.inner, g.state = s.Handler(), "ready"
	}()
}

func (g *gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	inner, state, msg := g.inner, g.state, g.msg
	g.mu.Unlock()
	switch r.URL.Path {
	case "/firmware/state":
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"state": state, "message": msg})
		return
	case "/firmware":
		g.upload(w, r, state)
		return
	}
	if inner != nil {
		inner.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(setupHTML)
		return
	}
	http.NotFound(w, r)
}

// upload takes the user's firmware, checks it, stores it as sequencer_flash.bin next to the program
// and starts the unit. It only works while no firmware is loaded.
func (g *gate) upload(w http.ResponseWriter, r *http.Request, state string) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST the firmware file", http.StatusMethodNotAllowed)
		return
	}
	// a page on another site must not be able to feed the program a file
	if o := r.Header.Get("Origin"); o != "" {
		if u, err := url.Parse(o); err != nil || u.Host != r.Host {
			http.Error(w, "cross site request refused", http.StatusForbidden)
			return
		}
	}
	if state != "needfirmware" && state != "error" {
		http.Error(w, "the unit is already starting or running", http.StatusConflict)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFirmwareUpload))
	if err != nil {
		http.Error(w, "the file is too large to be firmware", http.StatusRequestEntityTooLarge)
		return
	}
	img, err := NormalizeFirmware(data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	path := "sequencer_flash.bin"
	if p := os.Getenv("QYOMEGA_FIRMWARE"); p != "" {
		path = p
	}
	if err := os.WriteFile(path, img, 0o644); err != nil {
		http.Error(w, "could not save the image: "+err.Error(), http.StatusInternalServerError)
		return
	}
	os.Remove(snapshotFile) // a snapshot belongs to the firmware it was made from
	g.start()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"state":"booting"}`))
}
