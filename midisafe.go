// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import "sync"

// Live MIDI between the page and the outside world passes through MIDIFilter, which decides what
// may leave (or enter) the emulated unit. The rule for output is an allow list: a message goes out
// only if it is plainly harmless to equipment it reaches. Everything the unit's firmware sends that
// could reset, reconfigure or overwrite another device is dropped here, on the server, before the
// page ever sees it:
//
//   - all System Exclusive (bulk dumps, XG and GM resets, parameter changes, update traffic)
//   - System Reset (FF), tune request, song position and song select
//   - Bank Select, RPN and NRPN controllers, and controllers 120 to 127 (all sound off, reset all
//     controllers, local control, mode changes)
//   - Program Change, unless the user switches it on
//   - clock, start, stop and continue, unless the user switches them on
//
// What passes: notes, pitch bend, aftertouch, and the ordinary performance controllers.
// The page may also apply its own checks, but this is the one that counts.

// MIDIPolicy is what the user has opted into on top of the always allowed messages.
type MIDIPolicy struct {
	Transport     bool // clock, start, stop, continue
	ProgramChange bool
}

// midiBlockedKinds names the groups of dropped messages for the page's counter.
const (
	kindSysEx   = "SysEx"
	kindReset   = "Reset and system"
	kindBank    = "Bank select"
	kindData    = "RPN and NRPN"
	kindMode    = "Mode and reset controllers"
	kindProgram = "Program change"
	kindClock   = "Clock and transport"
)

const midiLogMax = 4096

// MIDIMsg is one message that was let through.
type MIDIMsg struct {
	Seq  int
	At   int64 // unix milliseconds
	Data []byte
}

// MIDIFilter parses a raw MIDI byte stream (running status, interleaved real time bytes, SysEx
// that never ends) and keeps the messages that the policy allows.
type MIDIFilter struct {
	mu     sync.Mutex
	pol    MIDIPolicy
	open   bool // input mode: every channel message passes, SysEx and system messages still do not
	status byte
	data   [2]byte
	n      int
	need   int
	sysex  bool
	ignore int // data bytes of a dropped system common message still to skip

	seq     int
	log     []MIDIMsg
	blocked map[string]int
}

// NewMIDIOutFilter is the filter for what the unit sends out: the allow list above.
func NewMIDIOutFilter() *MIDIFilter { return &MIDIFilter{blocked: map[string]int{}} }

// NewMIDIInFilter is the filter for what comes in from a keyboard or DAW: all channel messages and
// the clock and transport bytes pass, SysEx and system messages do not (SysEx has its own path).
func NewMIDIInFilter() *MIDIFilter {
	return &MIDIFilter{open: true, pol: MIDIPolicy{Transport: true, ProgramChange: true}, blocked: map[string]int{}}
}

func (f *MIDIFilter) SetPolicy(p MIDIPolicy) { f.mu.Lock(); f.pol = p; f.mu.Unlock() }
func (f *MIDIFilter) Policy() MIDIPolicy     { f.mu.Lock(); defer f.mu.Unlock(); return f.pol }

// Reset forgets a half received message (after a power cycle of the unit).
func (f *MIDIFilter) Reset() {
	f.mu.Lock()
	f.status, f.n, f.need, f.sysex, f.ignore = 0, 0, 0, false, 0
	f.mu.Unlock()
}

func (f *MIDIFilter) block(kind string) { f.blocked[kind]++ }

func (f *MIDIFilter) emit(now int64, d ...byte) {
	f.seq++
	f.log = append(f.log, MIDIMsg{Seq: f.seq, At: now, Data: append([]byte(nil), d...)})
	if len(f.log) > midiLogMax {
		f.log = append(f.log[:0], f.log[len(f.log)-midiLogMax/2:]...)
	}
}

// Feed takes raw bytes and keeps the allowed messages. now is the time in unix milliseconds.
func (f *MIDIFilter) Feed(raw []byte, now int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range raw {
		switch {
		case b >= 0xF8: // real time: may appear anywhere and never disturbs the message in progress
			switch b {
			case 0xF8, 0xFA, 0xFB, 0xFC:
				if f.pol.Transport {
					f.emit(now, b)
				} else {
					f.block(kindClock)
				}
			case 0xFE: // active sensing: nothing to forward, nothing to report
			default: // FF system reset, F9 and FD undefined
				f.block(kindReset)
			}
		case b >= 0x80:
			f.sysex, f.ignore, f.n = false, 0, 0
			switch {
			case b == 0xF0:
				f.status, f.sysex = 0, true
				f.block(kindSysEx)
			case b == 0xF7:
				f.status = 0
			case b >= 0xF1: // system common: dropped, and its data bytes skipped
				f.status = 0
				f.block(kindReset)
				switch b {
				case 0xF1, 0xF3:
					f.ignore = 1
				case 0xF2:
					f.ignore = 2
				}
			default:
				f.status = b
				f.need = 2
				if t := b & 0xF0; t == 0xC0 || t == 0xD0 {
					f.need = 1
				}
			}
		default: // data byte
			if f.sysex {
				continue
			}
			if f.ignore > 0 {
				f.ignore--
				continue
			}
			if f.status == 0 {
				continue
			}
			f.data[f.n] = b
			f.n++
			if f.n == f.need {
				f.n = 0
				f.decide(f.status, f.data[0], f.data[1], f.need, now)
			}
		}
	}
}

// decide applies the allow list to one complete channel message.
func (f *MIDIFilter) decide(st, d1, d2 byte, need int, now int64) {
	pass := func() {
		if need == 1 {
			f.emit(now, st, d1)
		} else {
			f.emit(now, st, d1, d2)
		}
	}
	switch st & 0xF0 {
	case 0x80, 0x90, 0xA0, 0xD0, 0xE0: // notes, polyphonic and channel pressure, pitch bend
		pass()
	case 0xC0:
		if f.pol.ProgramChange {
			pass()
		} else {
			f.block(kindProgram)
		}
	case 0xB0:
		if f.open {
			pass()
			return
		}
		switch {
		case d1 == 0 || d1 == 32:
			f.block(kindBank)
		case d1 == 6 || d1 == 38 || (d1 >= 96 && d1 <= 101):
			f.block(kindData)
		case d1 >= 120:
			f.block(kindMode)
		default:
			pass()
		}
	}
}

// Since returns the messages after sequence number seq, the number to ask for next time, and the
// counts of what was dropped. A caller that fell behind the ring simply gets the oldest kept ones.
func (f *MIDIFilter) Since(seq int) (msgs []MIDIMsg, next int, blocked map[string]int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.log {
		if m.Seq > seq {
			msgs = append(msgs, m)
		}
	}
	blocked = make(map[string]int, len(f.blocked))
	for k, v := range f.blocked {
		blocked[k] = v
	}
	return msgs, f.seq, blocked
}
