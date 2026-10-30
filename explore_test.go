// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"fmt"
	"os"
	"testing"
)

func tapExit(m *Machine) {
	m.Keys.Press("EXIT", true)
	m.Run(3000000)
	m.Keys.Press("EXIT", false)
	m.Run(3000000)
}

// Investigation harness for the hang after bulk mode OFF (see FRONTEND_STATUS.md, step 9).
// Runs only with EXPLORE=1 and needs boot.snap.
func TestExploreCompleted(t *testing.T) {
	if os.Getenv("EXPLORE") == "" {
		t.Skip()
	}
	on := []byte{0xF0, 0x43, 0x10, 0x5F, 0x10, 0x00, 0x00, 0x01, 0xF7}
	off := []byte{0xF0, 0x43, 0x10, 0x5F, 0x10, 0x00, 0x00, 0x00, 0xF7}
	req := []byte{0xF0, 0x43, 0x20, 0x5F, 0x13, 0x00, 0x00, 0xF7}
	type step struct {
		what string
		msg  []byte
	}
	seqs := [][]step{
		{{"on", on}, {"req", req}, {"EXIT", nil}, {"off", off}, {"on", on}, {"req", req}},
	}
	for si, seq := range seqs {
		m := newTestMachine(t)
		if err := m.LoadSnapshot(snapshotFile); err != nil {
			t.Fatal(err)
		}
		show := func(tag string) {
			sc := m.Chip.sci[1]
			t0 := m.Chip.tpu.ch[0]
			fmt.Printf("      tstr=%02X tpu0: tcr=%02X tier=%02X tsr=%02X cnt=%04X tgra=%04X pend32..36=%v\n", m.Chip.tpu.tstr, t0.regs[tTCR], t0.regs[tTIER], t0.regs[tTSR], t0.cnt, t0.tgr[0], m.CPU.pending[32:37])
			fmt.Printf("    %-10s popup=%02X bulk=%02X sci1: scr=%02X ssr=%02X rxq=%d pend=%v ccr=%02X pc=%06X\n", tag, m.Bus.Read8(0x413269), m.Bus.Read8(0x407DAE), sc.regs[sciSCR], sc.ssr, len(sc.rxq), m.CPU.pending[84:88], m.CPU.CCR, m.CPU.PC)
		}
		fmt.Println("sequence", si)
		for _, st := range seq {
			switch st.what {
			case "EXIT":
				tapExit(m)
			case "wait":
				m.Run(30000000)
			default:
				out, err := m.SendSysEx(st.msg, 600000)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Printf("  %s -> %d bytes back\n", st.what, len(out))
			}
			show("after " + st.what)
			if st.what == "off" {
				writes := 0
				m.Bus.OnWrite = func(a uint32, size int, v uint32) {
					if a == 0x409B35 && writes < 12 {
						writes++
						fmt.Printf("      write 409B35 = %02X pc=%06X\n", v, m.CPU.PC)
					}
				}
				m.Bus.Unmapped = nil
				m.Run(3000000)
				m.Bus.OnWrite = nil
				fmt.Printf("      409B35 now %02X, 409B34 %02X\n", m.Bus.Read8(0x409B35), m.Bus.Read8(0x409B34))
				for _, a := range m.Bus.Unmapped {
					fmt.Printf("      unmapped %06X write=%v val=%02X x%d pc=%06X\n", a.Addr, a.Write, a.Val, a.Count, a.PC)
				}
			}
		}
	}
}

// Presses every matrix key code from the boot snapshot and records the screen it ends up on.
// Runs only with EXPLORE=2.
func TestExploreKeyCodes(t *testing.T) {
	if os.Getenv("EXPLORE") != "2" {
		t.Skip()
	}
	os.MkdirAll("keyshots", 0o755)
	for code := 0; code < 0x38; code++ {
		m := newTestMachine(t)
		if err := m.LoadSnapshot(snapshotFile); err != nil {
			t.Fatal(err)
		}
		if err := m.Keys.PressCode(uint8(code), true); err != nil {
			fmt.Printf("code %02X: %v\n", code, err)
			continue
		}
		m.Run(3000000)
		m.Keys.PressCode(uint8(code), false)
		m.Run(6000000)
		fmt.Printf("code %02X screen id %02X%02X\n", code, m.Bus.Read8(0x4084B4), m.Bus.Read8(0x4084B5))
		m.LCD.PNG(fmt.Sprintf("keyshots/c%02X.png", code), 6, 64, 2)
	}
}

// Long run after a single key, to see whether a slow handler is the reason nothing shows.
// Runs only with EXPLORE=3.
func TestExploreSlowKeys(t *testing.T) {
	if os.Getenv("EXPLORE") != "3" {
		t.Skip()
	}
	for _, code := range []uint8{0x33, 0x13, 0x0A, 0x09, 0x0B} {
		m := newTestMachine(t)
		if err := m.LoadSnapshot(snapshotFile); err != nil {
			t.Fatal(err)
		}
		before := m.LCD.Cmds
		m.Keys.PressCode(code, true)
		m.Run(3000000)
		m.Keys.PressCode(code, false)
		for i := 0; i < 6; i++ {
			m.Run(10000000)
			n := 0
			for k := range m.LCD.Cmds {
				n += m.LCD.Cmds[k] - before[k]
			}
			fmt.Printf("code %02X +%dM screen %02X%02X lcd cmds since press %d pc=%06X popup=%02X\n", code, (i+1)*10, m.Bus.Read8(0x4084B4), m.Bus.Read8(0x4084B5), n, m.CPU.PC, m.Bus.Read8(0x413269))
		}
		m.LCD.PNG(fmt.Sprintf("keyshots/slow%02X.png", code), 6, 64, 2)
	}
}

func pcSet(m *Machine, n int, into map[uint32]bool, order *[]uint32, skip map[uint32]bool) {
	for i := 0; i < n; i++ {
		pc := m.CPU.PC
		if !into[pc] {
			into[pc] = true
			if skip == nil || !skip[pc] {
				*order = append(*order, pc)
			}
		}
		if err := m.CPU.Step(); err != nil {
			return
		}
	}
}

// New code reached after a key, compared with an idle run. Runs only with EXPLORE=4.
func TestExploreKeyPaths(t *testing.T) {
	if os.Getenv("EXPLORE") != "4" {
		t.Skip()
	}
	load := func() *Machine {
		m := newTestMachine(t)
		if err := m.LoadSnapshot(snapshotFile); err != nil {
			t.Fatal(err)
		}
		return m
	}
	idle := map[uint32]bool{}
	var junk []uint32
	pcSet(load(), 12000000, idle, &junk, nil)
	for _, code := range []uint8{0x07, 0x33, 0x13, 0x0A} {
		m := load()
		seen := map[uint32]bool{}
		var order []uint32
		m.Keys.PressCode(code, true)
		pcSet(m, 3000000, seen, &order, idle)
		m.Keys.PressCode(code, false)
		pcSet(m, 9000000, seen, &order, idle)
		fmt.Printf("code %02X: %d new pcs outside idle:", code, len(order))
		for i, p := range order {
			if os.Getenv("TAIL") == "" && i >= 60 {
				break
			}
			if os.Getenv("TAIL") != "" && i < len(order)-70 {
				continue
			}
			fmt.Printf(" %06X", p)
		}
		fmt.Println()
	}
}

// Opens the menu (code 09), then presses each other code and saves the screen when it changed.
// Runs only with EXPLORE=5.
func TestExploreInMenu(t *testing.T) {
	if os.Getenv("EXPLORE") != "5" {
		t.Skip()
	}
	os.MkdirAll("keyshots", 0o755)
	tap := func(m *Machine, code uint8) {
		m.Keys.PressCode(code, true)
		m.Run(3000000)
		m.Keys.PressCode(code, false)
		m.Run(6000000)
	}
	var base string
	for code := 0; code < 0x38; code++ {
		m := newTestMachine(t)
		if err := m.LoadSnapshot(snapshotFile); err != nil {
			t.Fatal(err)
		}
		tap(m, 0x09)
		if code == 0 {
			base = m.LCD.TextLayer(8)
		}
		if err := m.Keys.PressCode(uint8(code), true); err != nil {
			continue
		}
		m.Keys.PressCode(uint8(code), false)
		tap(m, uint8(code))
		changed := m.LCD.TextLayer(8) != base
		fmt.Printf("menu then code %02X changed=%v screen=%02X%02X\n", code, changed, m.Bus.Read8(0x4084B4), m.Bus.Read8(0x4084B5))
		m.LCD.PNG(fmt.Sprintf("keyshots/m%02X.png", code), 6, 64, 2)
	}
}

// Boots without dismissing the Factory Set popup and watches it. Runs only with EXPLORE=6.
func TestExploreFactorySet(t *testing.T) {
	if os.Getenv("EXPLORE") != "6" {
		t.Skip()
	}
	m := newTestMachine(t)
	m.Run(130000000)
	for i := 0; i < 8; i++ {
		fmt.Printf("after %dM popup=%02X screen=%02X%02X\n", 130+i*20, m.Bus.Read8(0x413269), m.Bus.Read8(0x4084B4), m.Bus.Read8(0x4084B5))
		m.Run(20000000)
	}
	m.LCD.PNG("fs_wait.png", 6, 64, 2)
	m.Keys.PressCode(0x07, true)
	m.Run(3000000)
	m.Keys.PressCode(0x07, false)
	m.Run(10000000)
	fmt.Println(m.LCD.TextLayer(8))
	m.LCD.PNG("fs_pattern.png", 6, 64, 2)
}

// Looks for tempo values in RAM after switching to pattern mode. Runs only with EXPLORE=7.
func TestExplorePatternTempo(t *testing.T) {
	if os.Getenv("EXPLORE") != "7" {
		t.Skip()
	}
	m := newTestMachine(t)
	if err := m.LoadSnapshot(snapshotFile); err != nil {
		t.Fatal(err)
	}
	find := func(tag string) {
		for _, pat := range [][2]byte{{0x04, 0xB0}, {0x02, 0x29}} {
			for a := uint32(0x400000); a < 0x420000; a++ {
				if m.Bus.Read8(a) == pat[0] && m.Bus.Read8(a+1) == pat[1] {
					fmt.Printf("%s: %02X%02X at %06X\n", tag, pat[0], pat[1], a)
				}
			}
		}
	}
	find("song mode")
	m.Keys.PressCode(0x07, true)
	m.Run(3000000)
	m.Keys.PressCode(0x07, false)
	m.Run(10000000)
	find("pattern mode")
	m.LCD.PNG("pat_full.png", 6, 64, 4)
}

// Dumps the places that held the song tempo before and after switching to pattern mode. EXPLORE=8.
func TestExplorePatternRAM(t *testing.T) {
	if os.Getenv("EXPLORE") != "8" {
		t.Skip()
	}
	m := newTestMachine(t)
	if err := m.LoadSnapshot(snapshotFile); err != nil {
		t.Fatal(err)
	}
	dump := func(tag string, a uint32, n int) {
		fmt.Printf("%s %06X:", tag, a)
		for i := 0; i < n; i++ {
			fmt.Printf(" %02X", m.Bus.Read8(a+uint32(i)))
		}
		fmt.Println()
	}
	for _, a := range []uint32{0x406D20, 0x407310} {
		dump("song", a, 32)
	}
	m.Keys.PressCode(0x07, true)
	m.Run(3000000)
	m.Keys.PressCode(0x07, false)
	m.Run(10000000)
	for _, a := range []uint32{0x406D20, 0x407310} {
		dump("patt", a, 32)
	}
}

// Which unmapped addresses does the pattern mode switch touch? EXPLORE=9.
func TestExplorePatternUnmapped(t *testing.T) {
	if os.Getenv("EXPLORE") != "9" {
		t.Skip()
	}
	m := newTestMachine(t)
	if err := m.LoadSnapshot(snapshotFile); err != nil {
		t.Fatal(err)
	}
	m.Bus.Unmapped = nil
	m.Keys.PressCode(0x07, true)
	m.Run(3000000)
	m.Keys.PressCode(0x07, false)
	m.Run(10000000)
	for _, a := range m.Bus.Unmapped {
		fmt.Printf("unmapped %06X write=%v val=%02X x%d pc=%06X\n", a.Addr, a.Write, a.Val, a.Count, a.PC)
	}
}

// Who writes the pattern working header at 0x406D20? EXPLORE=10.
func TestExploreHeaderWriter(t *testing.T) {
	if os.Getenv("EXPLORE") != "10" {
		t.Skip()
	}
	m := newTestMachine(t)
	if err := m.LoadSnapshot(snapshotFile); err != nil {
		t.Fatal(err)
	}
	n := 0
	seen := map[uint32]int{}
	m.Bus.OnWrite = func(a uint32, size int, v uint32) {
		if a >= 0x406D20 && a < 0x406D28 {
			seen[m.CPU.PC]++
			if n < 6 {
				n++
				fmt.Printf("write %06X size %d val %X pc=%06X\n", a, size, v, m.CPU.PC)
			}
		}
	}
	m.Keys.PressCode(0x07, true)
	m.Run(3000000)
	m.Keys.PressCode(0x07, false)
	m.Run(10000000)
	fmt.Println("writer pcs", seen)
}

// Screens during a cold boot, to see whether the firmware draws a start up picture. EXPLORE=11.
func TestExploreBootScreens(t *testing.T) {
	if os.Getenv("EXPLORE") != "11" {
		t.Skip()
	}
	m := newTestMachine(t)
	prevCmds := 0
	for i := 1; i <= 130; i++ {
		m.Run(1000000)
		if m.LCD.Commands != prevCmds {
			prevCmds = m.LCD.Commands
			fmt.Printf("%dM instr: lcd commands %d, text area %d gfx %d disp %02X mode %02X\n", i, m.LCD.Commands, m.LCD.TextArea, m.LCD.GfxArea, m.LCD.Disp, m.LCD.Mode)
			if i%10 == 0 || i < 12 {
				m.LCD.PNG(fmt.Sprintf("bootshots/b%03d.png", i), 6, 64, 2)
			}
		}
	}
}

// Where does the boot spend instructions 12M to 130M? EXPLORE=12.
func TestExploreBootIdle(t *testing.T) {
	if os.Getenv("EXPLORE") != "12" {
		t.Skip()
	}
	m := newTestMachine(t)
	m.Run(12000000)
	for _, span := range []int{20, 60, 100} {
		for m.CPU.Cycles < uint64(span)*1000000 && m.CPU.Cycles < 200000000 {
			m.Run(1000000)
		}
		hist := map[uint32]int{}
		for i := 0; i < 400000; i++ {
			hist[m.CPU.PC&^0xff]++
			m.CPU.Step()
		}
		fmt.Printf("at ~%dM (cycles %d): popup %02X screen %02X%02X, busiest 256 byte blocks:", span, m.CPU.Cycles, m.Bus.Read8(0x413269), m.Bus.Read8(0x4084B4), m.Bus.Read8(0x4084B5))
		for k := 0; k < 4; k++ {
			best, bn := uint32(0), 0
			for a, n := range hist {
				if n > bn {
					best, bn = a, n
				}
			}
			fmt.Printf(" %06X:%d", best, bn)
			delete(hist, best)
		}
		fmt.Println()
	}
}

// Cold boot with the battery backed RAM of the saved state. EXPLORE=13.
func TestExploreWarmBoot(t *testing.T) {
	if os.Getenv("EXPLORE") != "13" {
		t.Skip()
	}
	old := newTestMachine(t)
	if err := old.LoadSnapshot(snapshotFile); err != nil {
		t.Fatal(err)
	}
	m := newTestMachine(t)
	for _, base := range []uint32{0x400000, 0x600000} {
		copy(m.Bus.find(base).Data, old.Bus.find(base).Data)
	}
	for i := 1; i <= 16; i++ {
		m.Run(1000000)
		fmt.Printf("%dM popup=%02X screen=%02X%02X lcd cmds %d\n", i, m.Bus.Read8(0x413269), m.Bus.Read8(0x4084B4), m.Bus.Read8(0x4084B5), m.LCD.Commands)
	}
	m.LCD.PNG("warm.png", 6, 64, 2)
	m.Run(40000000)
	fmt.Printf("later popup=%02X screen=%02X%02X\n", m.Bus.Read8(0x413269), m.Bus.Read8(0x4084B4), m.Bus.Read8(0x4084B5))
	m.LCD.PNG("warm2.png", 6, 64, 2)
}

// What does PLAY do in pattern mode? EXPLORE=14.
func TestExplorePlay(t *testing.T) {
	if os.Getenv("EXPLORE") != "14" {
		t.Skip()
	}
	m := newTestMachine(t)
	if err := m.LoadSnapshot(snapshotFile); err != nil {
		t.Fatal(err)
	}
	tap := func(code uint8) {
		m.Keys.PressCode(code, true)
		m.Run(3000000)
		m.Keys.PressCode(code, false)
		m.Run(6000000)
	}
	tap(0x07) // PATTERN
	m.MIDIOut()
	before := m.LCD.Commands
	tap(0x04) // PLAY
	for i := 0; i < 6; i++ {
		m.Run(10000000)
		out := m.MIDIOut()
		clk := 0
		for _, b := range out {
			if b == 0xF8 {
				clk++
			}
		}
		fmt.Printf("+%dM lcd cmds %d led %02X screen %02X%02X midi bytes %d clocks %d first %X\n", (i+1)*10, m.LCD.Commands-before, m.Keys.LED, m.Bus.Read8(0x4084B4), m.Bus.Read8(0x4084B5), len(out), clk, out[:min(len(out), 8)])
	}
	m.LCD.PNG("play_pat.png", 6, 64, 3)
}

// Is the master tick running before and after PLAY? EXPLORE=15.
func TestExploreTicks(t *testing.T) {
	if os.Getenv("EXPLORE") != "15" {
		t.Skip()
	}
	m := newTestMachine(t)
	if err := m.LoadSnapshot(snapshotFile); err != nil {
		t.Fatal(err)
	}
	show := func(tag string) {
		t1 := m.Chip.tpu.ch[1]
		fmt.Printf("%-12s tick408DF2=%08X tstr=%02X tpu1 tcr=%02X tier=%02X tgra=%04X tsr=%02X cnt=%04X screen=%02X%02X\n", tag, m.Bus.Read32(0x408DF2), m.Chip.tpu.tstr, t1.regs[tTCR], t1.regs[tTIER], t1.tgr[0], t1.regs[tTSR], t1.cnt, m.Bus.Read8(0x4084B4), m.Bus.Read8(0x4084B5))
	}
	tap := func(code uint8) {
		m.Keys.PressCode(code, true)
		m.Run(3000000)
		m.Keys.PressCode(code, false)
		m.Run(6000000)
	}
	show("start")
	m.Run(5000000)
	show("+5M idle")
	tap(0x07)
	show("pattern")
	tap(0x04)
	show("after PLAY")
	m.Run(20000000)
	show("+20M")
}

// Frames while a pattern plays. EXPLORE=16.
func TestExplorePlayFrames(t *testing.T) {
	if os.Getenv("EXPLORE") != "16" {
		t.Skip()
	}
	os.MkdirAll("playshots", 0o755)
	m := newTestMachine(t)
	if err := m.LoadSnapshot(snapshotFile); err != nil {
		t.Fatal(err)
	}
	tap := func(code uint8) {
		m.Keys.PressCode(code, true)
		m.Run(3000000)
		m.Keys.PressCode(code, false)
		m.Run(6000000)
	}
	tap(0x07)
	tap(0x04)
	for i := 0; i < 8; i++ {
		m.Run(6000000)
		m.LCD.PNG(fmt.Sprintf("playshots/p%d.png", i), 6, 64, 2)
	}
}

// Writes to the sound generator window while a keyboard note plays. EXPLORE=17.
func TestExploreTGWrites(t *testing.T) {
	if os.Getenv("EXPLORE") != "17" {
		t.Skip()
	}
	m := newTestMachine(t)
	if err := m.LoadSnapshot(snapshotFile); err != nil {
		t.Fatal(err)
	}
	type wr struct {
		a    uint32
		size int
		v    uint32
		pc   uint32
	}
	var log []wr
	counts := map[uint32]int{}
	rec := false
	m.Bus.OnWrite = func(a uint32, size int, v uint32) {
		if rec && a >= 0xFFE000 && a < 0xFFF000 {
			counts[a]++
			if len(log) < 400 {
				log = append(log, wr{a, size, v, m.CPU.PC})
			}
		}
	}
	m.Run(3000000)
	rec = true
	m.Keys.PressCode(0x22, true) // C3
	m.Run(4000000)
	m.Keys.PressCode(0x22, false)
	m.Run(4000000)
	fmt.Println("distinct addresses:", len(counts))
	for _, w := range log[:min(len(log), 150)] {
		fmt.Printf("%06X size %d = %08X pc=%06X\n", w.a, w.size, w.v, w.pc)
	}
}

// Bytes the main CPU writes to the second CPU port at 0xE00000. EXPLORE=18.
func TestExploreSubCPU(t *testing.T) {
	if os.Getenv("EXPLORE") != "18" {
		t.Skip()
	}
	m := newTestMachine(t)
	var boot []byte
	m.Bus.OnWrite = func(a uint32, size int, v uint32) {
		if a == 0xE00000 {
			boot = append(boot, byte(v))
		}
	}
	m.Run(14000000)
	fmt.Printf("boot: %d bytes to the sub CPU port, first 80: % X\n", len(boot), boot[:min(80, len(boot))])
	boot = nil
	m.Keys.PressCode(0x07, true)
	m.Run(3000000)
	m.Keys.PressCode(0x07, false)
	m.Run(3000000)
	fmt.Printf("after PATTERN: % X\n", boot)
	boot = nil
	m.Keys.PressCode(0x22, true) // C3
	m.Run(3000000)
	m.Keys.PressCode(0x22, false)
	m.Run(3000000)
	fmt.Printf("C3 down and up: % X\n", boot)
}
