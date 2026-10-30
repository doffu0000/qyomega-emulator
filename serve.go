// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"image/png"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Live front end: the machine runs in a goroutine at about real time and an HTTP
// server on the loopback interface lets a browser watch and drive it. Everything
// touching the Machine happens under Server.mu. Nothing here opens a MIDI port.

const (
	defaultServeAddr = "127.0.0.1:8100"
	snapshotFile     = "boot.snap"
	// instructions per second at real time: 16.9344 MHz at 3 clocks per instruction
	realTimeRate = 16934400.0 / statesPerInstr
	runSlice     = 20000
)

// Server owns a machine and runs it.
type Server struct {
	mu      sync.Mutex
	m       *Machine
	err     error // set when the CPU stops with an error
	stop    chan struct{}
	stopped chan struct{}

	keyDown  map[string]time.Time   // when each key was last pressed
	keyTimer map[string]*time.Timer // delayed releases for keys let go too soon
	rebase   bool                   // set by a reset: Run restarts its real time pacing
	epoch    int                    // bumped by a reset: the sound byte stream starts over

	// live MIDI: what may leave the unit, and what comes in from a keyboard or DAW (midisafe.go)
	outF, inF *MIDIFilter
	inMu      sync.Mutex
	inSeq     int

	sysexBusy atomic.Bool
	// progress of the transfer in progress, for the page
	sysexDone, sysexTotal atomic.Int64
	// wall clock limits for one SysEx exchange: how long the output must stay
	// quiet after the unit started answering, how long to wait for a first reply,
	// and the cap for the whole message (the emulator runs slower than the unit)
	sysexQuiet, sysexFirst, sysexLimit time.Duration
}

func NewServer(m *Machine) *Server {
	return &Server{m: m, outF: NewMIDIOutFilter(), inF: NewMIDIInFilter(), stop: make(chan struct{}), stopped: make(chan struct{}),
		keyDown: map[string]time.Time{}, keyTimer: map[string]*time.Timer{},
		sysexQuiet: 400 * time.Millisecond, sysexFirst: 2 * time.Second, sysexLimit: 15 * time.Second}
}

// Run executes the machine until Stop is called or the CPU fails. Slices are short
// so that HTTP handlers get the lock often.
func (s *Server) Run() {
	defer close(s.stopped)
	start := time.Now()
	s.mu.Lock()
	base := s.m.CPU.Cycles
	s.mu.Unlock()
	for {
		select {
		case <-s.stop:
			return
		default:
		}
		s.mu.Lock()
		if s.rebase { // a reset moved the cycle counter, restart the pacing from here
			start, base = time.Now(), s.m.CPU.Cycles
			s.rebase = false
		}
		if s.err == nil {
			s.err = s.m.Run(runSlice)
		}
		if sc := s.m.Chip.sci[1]; len(sc.TxFresh) > 0 { // the unit's MIDI output goes through the safety filter
			s.outF.Feed(sc.TxFresh, time.Now().UnixMilli())
			sc.TxFresh = sc.TxFresh[:0]
		}
		cyc := s.m.CPU.Cycles
		failed := s.err != nil
		s.mu.Unlock()
		if failed { // stay alive so that a reset can bring the machine back
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if s.sysexBusy.Load() {
			// A SysEx transfer is in progress and nobody is watching the clock: run flat out, and
			// start the real time pacing again from here when it is over (no time debt to repay).
			start, base = time.Now(), cyc
			runtime.Gosched()
			continue
		}
		ahead := time.Duration(float64(cyc-base)/realTimeRate*1e9) - time.Since(start)
		switch {
		case ahead > 0:
			time.Sleep(ahead)
		case ahead < -time.Second:
			// far behind real time (the host is slower than the unit): do not try to catch up
			start, base = time.Now(), cyc
		default:
			time.Sleep(100 * time.Microsecond)
		}
	}
}

// Stop ends Run and waits for it.
func (s *Server) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	<-s.stopped
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/leds", s.handleLEDs)
	mux.HandleFunc("/screen.png", s.handleScreen)
	mux.HandleFunc("/keys", s.handleKeys)
	mux.HandleFunc("/key", s.handleKey)
	mux.HandleFunc("/midi/out", s.handleMIDIOut)
	mux.HandleFunc("/midi/in", s.handleMIDIIn)
	mux.HandleFunc("/midi/policy", s.handleMIDIPolicy)
	mux.HandleFunc("/sysex", s.handleSysex)
	mux.HandleFunc("/sysex/progress", s.handleSysexProgress)
	mux.HandleFunc("/reset", s.handleReset)
	mux.HandleFunc("/tg", s.handleTG)
	mux.HandleFunc("/synth.js", s.handleSynth)
	mux.HandleFunc("/sfengine.js", s.handleSFEngine)
	mux.HandleFunc("/soundfonts.json", s.handleSoundfonts)
	mux.Handle("/vendor/", staticHandler("/vendor/", vendorDir))
	mux.Handle("/soundfonts/", staticHandler("/soundfonts/", fontDir))
	mux.Handle("/logos/", http.FileServer(http.FS(logosFS)))
	mux.HandleFunc("/", s.handleIndex)
	return mux
}

//go:embed index.html
var indexHTML []byte

//go:embed synth.js
var synthJS []byte


func (s *Server) handleSynth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(synthJS)
}

// handleTG serves the bytes the main CPU sent to the sound CPU (MIDI and XG, see subport.go)
// from absolute offset `from` on. X-QY-Next is the offset to ask for next. X-QY-Epoch changes
// when the unit has been power cycled; a client holding another epoch is sent the stream from
// the start and must reset its synthesizer.
func (s *Server) handleTG(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	epoch := s.epoch
	from, _ := strconv.ParseUint(q.Get("from"), 10, 64)
	if q.Get("epoch") != strconv.Itoa(epoch) {
		from = 0
	}
	data, next := s.m.Sub.Since(from)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-QY-Epoch", strconv.Itoa(epoch))
	w.Header().Set("X-QY-Next", strconv.FormatUint(next, 10))
	w.Write(data)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

// handleScreen draws the display at 4x. The image is rendered under the lock and
// encoded after releasing it.
func (s *Server) handleScreen(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	img := s.m.LCD.ImageW(lcdPanelWidth, 6, 64, 4)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	png.Encode(w, img)
}

// minKeyHold is how long a key stays down in wall time even when the browser
// releases it sooner: the firmware scans the matrix every few milliseconds of
// emulated time and the emulator runs slower than real time, so a very short
// click would otherwise fall between two scans.
const minKeyHold = 120 * time.Millisecond

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	names := s.m.Keys.Names()
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(names)
}

func (s *Server) handleKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	name := strings.ToLower(r.URL.Query().Get("name"))
	down := r.URL.Query().Get("down") == "1"
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.m.Keys.Has(name) {
		http.Error(w, fmt.Sprintf("unknown key %q", name), http.StatusBadRequest)
		return
	}
	if t := s.keyTimer[name]; t != nil { // a release is pending: the key is pressed again or released for good now
		t.Stop()
		delete(s.keyTimer, name)
	}
	if down {
		s.keyDown[name] = time.Now()
		s.m.Keys.Press(name, true)
		return
	}
	held := time.Since(s.keyDown[name])
	if held >= minKeyHold {
		s.m.Keys.Press(name, false)
		return
	}
	s.keyTimer[name] = time.AfterFunc(minKeyHold-held, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.keyTimer, name)
		s.m.Keys.Press(name, false)
	})
}

// handleReset power cycles the unit: it boots again from the reset vector, so the start up
// pictures play, and keeps the battery backed memory (songs, patterns, settings) like the
// real thing. With ?factory=1 the memory is blank too, so the firmware runs Factory Set and
// waits for a key (EXIT). Held keys are released.
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var ram []byte
	if r.URL.Query().Get("factory") != "1" {
		ram = s.m.BatteryRAM()
	}
	if err := s.m.PowerCycle(ram); err != nil {
		http.Error(w, "reset failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for name, t := range s.keyTimer {
		t.Stop()
		delete(s.keyTimer, name)
	}
	s.keyDown = map[string]time.Time{}
	s.err = nil
	s.rebase = true
	s.epoch++
	s.outF.Reset()
	w.WriteHeader(http.StatusNoContent)
}

type statusJSON struct {
	PC       string `json:"pc"`
	Cycles   uint64 `json:"cycles"`
	LCDCmds  int    `json:"lcdCommands"`
	Halted   bool   `json:"halted"`
	Error    string `json:"error,omitempty"`
	LED      uint8  `json:"led"`
	GfxArea  uint16 `json:"gfxArea"`
	TextArea uint16 `json:"textArea"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	st := statusJSON{
		PC:       fmt.Sprintf("%06X", s.m.CPU.PC),
		Cycles:   s.m.CPU.Cycles,
		LCDCmds:  s.m.LCD.Commands,
		Halted:   s.m.CPU.Halted,
		LED:      s.m.Keys.LED,
		GfxArea:  s.m.LCD.GfxArea,
		TextArea: s.m.LCD.TextArea,
	}
	if s.err != nil {
		st.Error = s.err.Error()
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(st)
}

// handleLEDs returns the changes of the indicator latch since a sequence number, each with how long
// ago it happened, so the page can replay the blinking (the play light follows the beat) on its own clock.
func (s *Server) handleLEDs(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	s.mu.Lock()
	ev, next := s.m.Keys.LEDEventsSince(since)
	cur := s.m.Keys.LED
	s.mu.Unlock()
	now := time.Now()
	out := make([][2]int64, len(ev))
	for i, e := range ev {
		out[i] = [2]int64{now.Sub(e.At).Milliseconds(), int64(e.V)} // [milliseconds ago, latch value]
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Next int        `json:"next"`
		Cur  uint8      `json:"cur"`
		Ev   [][2]int64 `json:"ev"`
	}{next, cur, out})
}

// loopbackAddr refuses anything that is not a loopback address: the machine has no
// authentication and must never be reachable from the network.
func loopbackAddr(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("%q is not a loopback address; the front end only listens on localhost", addr)
	}
	return net.JoinHostPort(host, port), nil
}

// parseServeArgs reads `emu serve [--fresh] [ADDR]`.
func parseServeArgs(args []string) (addr string, fresh bool, err error) {
	addr = defaultServeAddr
	for _, a := range args {
		if a == "--fresh" {
			fresh = true
			continue
		}
		addr = a
	}
	addr, err = loopbackAddr(addr)
	return
}

// bootInstructions is how long the firmware takes to reach the first screen.
const bootInstructions = 130000000

// bootAndDismissPopup runs the firmware from reset and presses EXIT, which
// dismisses the "Factory Set" popup (it has no timeout), so the machine ends on
// the SONG screen.
func bootAndDismissPopup(m *Machine) error {
	if err := m.Run(bootInstructions); err != nil {
		return err
	}
	m.Keys.Press("EXIT", true)
	if err := m.Run(3000000); err != nil {
		return err
	}
	m.Keys.Press("EXIT", false)
	if err := m.Run(3000000); err != nil {
		return err
	}
	m.MIDIOut() // forget what the boot sent
	return nil
}

// loadMachine builds the machine from the firmware image and brings it to the SONG screen: from
// boot.snap when there is one (a second or so), else by booting from blank memory (about 35 s) and
// writing boot.snap.
func loadMachine(fresh bool) (*Machine, error) {
	m, err := NewMachine(firmwarePath())
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(snapshotFile); err == nil && !fresh {
		if err := m.LoadSnapshot(snapshotFile); err != nil {
			fmt.Fprintln(os.Stderr, "snapshot:", err, "(booting from reset instead)")
			if m, err = NewMachine(firmwarePath()); err != nil {
				return nil, err
			}
		} else {
			// the snapshot supplies the memory, then the unit powers on so the start up
			// pictures play live
			if err := m.PowerCycle(m.BatteryRAM()); err != nil {
				return nil, fmt.Errorf("power on: %w", err)
			}
			fmt.Println("memory restored from", snapshotFile, "and powered on")
			return m, nil
		}
	}
	fmt.Println("booting the firmware, this takes about 35 seconds (once, the result is saved in " + snapshotFile + ") ...")
	if err := bootAndDismissPopup(m); err != nil {
		return nil, fmt.Errorf("boot stopped: %w", err)
	}
	if err := m.SaveSnapshot(snapshotFile); err != nil {
		fmt.Fprintln(os.Stderr, "could not write the snapshot:", err)
	} else {
		fmt.Println("saved", snapshotFile)
	}
	return m, nil
}

// serveMain implements `emu serve [--fresh] [ADDR]`. With the firmware image in place it boots
// (see loadMachine) and serves the unit. Without it the page is a setup screen where the user
// supplies their own firmware (the program does not include it) and the unit boots from there.
func serveMain(args []string) {
	addr, fresh, err := parseServeArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	g := newGate(fresh)
	if _, err := os.Stat(firmwarePath()); err == nil {
		g.start()
	} else {
		fmt.Println("no firmware image found (" + firmwarePath() + "): open the page to supply your own")
	}
	fmt.Println("serving on http://" + addr + "/ (localhost only, Ctrl+C to stop)")
	if err := http.ListenAndServe(addr, g); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Artwork on the faceplate. The Doffu cloud is the maker's own logo and is not GPL, see logos/LICENSE.txt.
//
//go:embed logos
var logosFS embed.FS
