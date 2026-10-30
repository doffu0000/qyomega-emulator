// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Live MIDI routes. The server never opens a MIDI port: it only hands filtered bytes to the page
// (/midi/out) and takes bytes from it (/midi/in). Which real ports are used, if any, is the user's
// choice in the page, through the browser's Web MIDI API without System Exclusive access.

const maxMIDIIn = 1024

// sameOrigin is false for a request made by a page on another site.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// handleMIDIOut returns what the unit has sent on its MIDI output since a sequence number, after the
// filter. Each message carries how long ago it happened so the page can keep the timing.
func (s *Server) handleMIDIOut(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	msgs, next, blocked := s.outF.Since(since)
	now := time.Now().UnixMilli()
	out := make([][2]any, len(msgs))
	for i, m := range msgs {
		out[i] = [2]any{now - m.At, hex.EncodeToString(m.Data)}
	}
	s.mu.Lock()
	epoch := s.epoch
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{"next": next, "msgs": out, "blocked": blocked, "epoch": epoch, "policy": s.outF.Policy()})
}

// handleMIDIPolicy sets the two optional message groups for the output. Both start off.
func (s *Server) handleMIDIPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "cross site request refused", http.StatusForbidden)
		return
	}
	var p MIDIPolicy
	if err := json.NewDecoder(io.LimitReader(r.Body, 256)).Decode(&p); err != nil {
		http.Error(w, "expected {\"Transport\":bool,\"ProgramChange\":bool}", http.StatusBadRequest)
		return
	}
	s.outF.SetPolicy(p)
	w.WriteHeader(http.StatusNoContent)
}

// handleMIDIIn feeds live MIDI from a keyboard or DAW into the emulated MIDI IN. SysEx and system
// messages are dropped here (the SysEx box has its own path); channel messages and the clock pass.
func (s *Server) handleMIDIIn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "cross site request refused", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxMIDIIn+1))
	if err != nil || len(body) > maxMIDIIn {
		http.Error(w, "too much MIDI in one request", http.StatusRequestEntityTooLarge)
		return
	}
	// the in filter parses and keeps allowed messages; take what it kept since the last call
	s.inMu.Lock()
	s.inF.Feed(body, time.Now().UnixMilli())
	msgs, next, _ := s.inF.Since(s.inSeq)
	s.inSeq = next
	s.inMu.Unlock()
	s.mu.Lock()
	for _, m := range msgs {
		s.m.MIDIIn(m.Data)
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
