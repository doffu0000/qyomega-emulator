// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// POST /sysex feeds SysEx to the emulated MIDI IN (serial port 1 of the emulated
// chip) and returns what the emulated unit sent back. There is no MIDI port
// anywhere in this program.

const maxSysexBody = 4 << 20

// parseSysexBody accepts a raw .syx file (any byte with the high bit set means
// raw) or hex text such as "F0 43 10 5F 10 00 00 01 F7", and returns the
// complete F0..F7 messages in it.
func parseSysexBody(body []byte) ([][]byte, error) {
	data := body
	raw := false
	for _, b := range body {
		if b >= 0x80 {
			raw = true
			break
		}
	}
	if !raw {
		text := strings.NewReplacer(",", " ", "\r", " ", "\n", " ", "\t", " ", "0x", " ", "0X", " ").Replace(string(body))
		data = nil
		for _, f := range strings.Fields(text) {
			v, err := strconv.ParseUint(f, 16, 8)
			if err != nil {
				return nil, fmt.Errorf("%q is not a hex byte", f)
			}
			data = append(data, byte(v))
		}
	}
	msgs := SplitSysEx(data)
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no complete F0 .. F7 message found")
	}
	return msgs, nil
}

func hexString(b []byte) string {
	var sb strings.Builder
	for i, x := range b {
		if i > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "%02X", x)
	}
	return sb.String()
}

type sysexReply struct {
	Sent    int      `json:"sent"`
	Replies []string `json:"replies"`
	Other   string   `json:"other,omitempty"` // MIDI that was not SysEx (notes, for instance), apart from active sensing
	Error   string   `json:"error,omitempty"`
}

// expectsReply says whether the unit answers a message. Bulk data blocks (device byte 0x0n)
// are never answered; dump requests (0x2n), parameter changes and bulk mode switches (0x1n,
// which are echoed) are, and anything else is treated like a request.
func expectsReply(msg []byte) bool {
	return len(msg) < 3 || msg[2]>>4 != 0
}

// isBulkModeSwitch reports whether msg is the bulk mode ON or OFF message (43 1n 5F 10 ...). The
// unit answers it at once or not at all, so there is no need to wait long to find out which.
func isBulkModeSwitch(msg []byte) bool {
	return len(msg) >= 8 && msg[1] == 0x43 && msg[2]>>4 == 1 && msg[3] == 0x5F && msg[4] == 0x10
}

// bulkSwitchFirst is how long to wait for the first reply to a bulk mode switch.
const bulkSwitchFirst = 600 * time.Millisecond

// blockSettle is how long, in emulated instructions, the unit gets to store a data block
// before the next one is sent: the toolkit pauses 0.1 s between blocks, which is about this.
const blockSettle = 600000

// exchange sends one message and waits for the unit to finish answering: the
// receiver must have taken every byte, and once something came back the output
// must then stay quiet for quiet; if nothing comes back, give up after first.
// A message that gets no answer (bulk data) only waits until the receiver has taken it
// and the unit has had blockSettle instructions to store it.
func (s *Server) exchange(msg []byte) []byte {
	s.mu.Lock()
	s.m.MIDIIn(msg)
	s.mu.Unlock()
	start := time.Now()
	if !expectsReply(msg) {
		var takenAt uint64
		for time.Since(start) < s.sysexLimit {
			time.Sleep(10 * time.Millisecond)
			s.mu.Lock()
			taken, cyc := len(s.m.Chip.sci[1].rxq) == 0, s.m.CPU.Cycles
			s.mu.Unlock()
			if !taken {
				continue
			}
			if takenAt == 0 {
				takenAt = cyc
			}
			if cyc-takenAt >= blockSettle {
				break
			}
		}
		s.mu.Lock()
		out := s.m.MIDIOut()
		s.mu.Unlock()
		return out
	}
	first := s.sysexFirst
	if isBulkModeSwitch(msg) && first > bulkSwitchFirst {
		first = bulkSwitchFirst
	}
	lastChange := start
	last := -1
	for time.Since(start) < s.sysexLimit {
		time.Sleep(25 * time.Millisecond)
		s.mu.Lock()
		taken := len(s.m.Chip.sci[1].rxq) == 0
		n := 0
		for _, b := range s.m.Chip.sci[1].TxLog {
			if b != 0xFE {
				n++
			}
		}
		s.mu.Unlock()
		if n != last {
			last, lastChange = n, time.Now()
		}
		if !taken {
			continue
		}
		if n > 0 && time.Since(lastChange) >= s.sysexQuiet {
			break
		}
		if n == 0 && time.Since(start) >= first {
			break
		}
	}
	s.mu.Lock()
	out := s.m.MIDIOut()
	s.mu.Unlock()
	return out
}

// handleSysexProgress reports how far the transfer in progress has got, for the page.
func (s *Server) handleSysexProgress(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{"sent": s.sysexDone.Load(), "total": s.sysexTotal.Load(), "busy": s.sysexBusy.Load()})
}

func (s *Server) handleSysex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxSysexBody+1))
	if err != nil || len(body) > maxSysexBody {
		http.Error(w, "body unreadable or larger than 4 MB", http.StatusBadRequest)
		return
	}
	msgs, err := parseSysexBody(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.sysexBusy.CompareAndSwap(false, true) {
		http.Error(w, "another SysEx transfer is in progress", http.StatusConflict)
		return
	}
	defer s.sysexBusy.Store(false)

	s.mu.Lock()
	s.m.MIDIOut() // forget what the unit sent before this request
	s.mu.Unlock()
	res := sysexReply{Replies: []string{}}
	var other []byte
	s.sysexTotal.Store(int64(len(msgs)))
	s.sysexDone.Store(0)
	defer s.sysexTotal.Store(0)
	for _, msg := range msgs {
		if r.Context().Err() != nil {
			res.Error = "request cancelled"
			break
		}
		out := s.exchange(msg)
		res.Sent++
		s.sysexDone.Store(int64(res.Sent))
		var stream []byte
		for _, b := range out {
			if b != 0xFE {
				stream = append(stream, b)
			}
		}
		for _, reply := range SplitSysEx(stream) {
			res.Replies = append(res.Replies, hexString(reply))
		}
		// bytes outside any F0..F7 message
		in := false
		for _, b := range stream {
			switch {
			case b == 0xF0:
				in = true
			case b == 0xF7 && in:
				in = false
			case !in:
				other = append(other, b)
			}
		}
	}
	if len(other) > 0 {
		res.Other = hexString(other)
	}
	s.mu.Lock()
	if s.err != nil {
		res.Error = s.err.Error()
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
