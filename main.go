// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  emu dis HEXPC [COUNT]
  emu trace HEXPC COUNT [FROM]      registers per instruction (pipe through grep or tail)
  emu run HEXPC COUNT               run, then list accesses to unmodelled hardware
  emu profile HEXPC COUNT [SKIP]    hottest addresses outside the kernel
  emu watch HEXPC COUNT LO HI       log writes to LO..HI (hex) with step and pc
  emu screen COUNT [FONTW [ROWS [PNGFILE]]]   run from reset, then show the LCD
  emu sysex BOOT GAP "F0 43 ..." ["F0 .."...]  boot, then send each message to MIDI IN (GAP instructions
                                    of running after each) and print what the unit sends back
  emu keys BOOT GAP KEY [KEY ...]   boot, then press each key (names like PLAY, RIGHT, k:C3), run GAP
                                    instructions held and GAP released, and print the text layer
  emu syx BOOT SETTLE OUT.syx FILE.syx...   boot, key EXIT, send bulk mode ON, every message of the files,
                                    bulk mode OFF, request pattern 1 back and save the reply in OUT.syx
  emu keyscan BOOT GAP              boot, EXIT, then press every key code once and report what changed
  emu dump BOOT HEXADDR LEN         boot, then hex dump memory
  emu serve [127.0.0.1:8100]        live front end on localhost (loads boot.snap if present)
  emu snap BOOT FILE                boot, then save the whole machine state to FILE
  emu restore FILE COUNT [PNGFILE]  load a snapshot, run COUNT more instructions, show the text layer
  (COUNT is decimal; HEXPC is the start address, DCEEA is the reset handler)`)
	os.Exit(2)
}

func hexArg(s string) uint32 {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		usage()
	}
	return uint32(v)
}

func intArg(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		usage()
	}
	return v
}

func newMachine() *Machine {
	path := firmwarePath()
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(os.Stderr, "firmware image not found: %s\n"+
			"This program does not include Yamaha's firmware. Decode your own copy of the v1.37 update\n"+
			"to a 2 MB image and point QYOMEGA_FIRMWARE at it, or put it next to the program as sequencer_flash.bin.\n", path)
		os.Exit(1)
	}
	m, err := NewMachine(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return m
}

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "serve" {
		serveMain(os.Args[2:])
		return
	}
	if len(os.Args) < 3 {
		usage()
	}
	m := newMachine()
	c, b := m.CPU, m.Bus
	if os.Args[1] != "screen" && os.Args[1] != "sysex" && os.Args[1] != "keys" && os.Args[1] != "dump" && os.Args[1] != "calls" && os.Args[1] != "syx" && os.Args[1] != "keyscan" && os.Args[1] != "snap" && os.Args[1] != "restore" {
		c.PC = hexArg(os.Args[2])
	}
	switch os.Args[1] {
	case "snap":
		if len(os.Args) < 4 {
			usage()
		}
		if err := m.Run(intArg(os.Args[2])); err != nil {
			fmt.Println("boot stopped:", err)
			return
		}
		if err := m.SaveSnapshot(os.Args[3]); err != nil {
			fmt.Println("snapshot:", err)
			os.Exit(1)
		}
		fmt.Println("saved", os.Args[3])
	case "restore":
		// emu restore FILE COUNT [FONTW [ROWS [PNGFILE]]]: load, run COUNT more, show the LCD
		if len(os.Args) < 4 {
			usage()
		}
		if err := m.LoadSnapshot(os.Args[2]); err != nil {
			fmt.Println("restore:", err)
			os.Exit(1)
		}
		if err := m.Run(intArg(os.Args[3])); err != nil {
			fmt.Println("stopped:", err)
		}
		fmt.Print("text layer:\n", m.LCD.TextLayer(8))
		if len(os.Args) > 4 {
			if err := m.LCD.PNG(os.Args[4], 6, 64, 4); err != nil {
				fmt.Println("png:", err)
			}
		}
	case "dis":
		n := 20
		if len(os.Args) > 3 {
			n = intArg(os.Args[3])
		}
		pc := c.PC
		for i := 0; i < n; i++ {
			in := Decode(b, pc)
			fmt.Printf("%06X  %s\n", pc, in)
			pc += uint32(in.Size)
		}
	case "profile":
		n := intArg(os.Args[3])
		skip := 0
		if len(os.Args) > 4 {
			skip = intArg(os.Args[4])
		}
		hist := map[uint32]int{}
		for i := 0; i < n; i++ {
			if i >= skip && !(c.PC >= 0x10000 && c.PC < 0x13000) { // leave the kernel out
				hist[c.PC]++
			}
			if err := c.Step(); err != nil {
				fmt.Println("stopped:", err)
				break
			}
		}
		for k := 0; k < 25; k++ {
			best, bc := uint32(0), 0
			for a, cnt := range hist {
				if cnt > bc {
					best, bc = a, cnt
				}
			}
			if bc == 0 {
				break
			}
			fmt.Printf("%06X %9d  %s\n", best, bc, Decode(b, best))
			delete(hist, best)
		}
		fmt.Printf("pc=%06X halted=%v\n", c.PC, c.Halted)
	case "screen":
		fontW, rows := 6, 64
		if len(os.Args) > 3 {
			fontW = intArg(os.Args[3])
		}
		if len(os.Args) > 4 {
			rows = intArg(os.Args[4])
		}
		for i := 0; i < intArg(os.Args[2]); i++ {
			if err := c.Step(); err != nil {
				fmt.Println("stopped:", err)
				break
			}
		}
		fmt.Printf("lcd: %d commands, gfx home %04X area %d, text home %04X area %d, mode %02X display %02X\n",
			m.LCD.Commands, m.LCD.GfxHome, m.LCD.GfxArea, m.LCD.TextHome, m.LCD.TextArea, m.LCD.Mode, m.LCD.Disp)
		fmt.Print(m.LCD.Text(fontW, rows))
		fmt.Print("text layer:\n", m.LCD.TextLayer(8))
		if len(os.Args) > 5 {
			if err := m.LCD.PNG(os.Args[5], fontW, rows, 4); err != nil {
				fmt.Println("png:", err)
			}
		}
	case "sysex":
		boot, gap := intArg(os.Args[2]), intArg(os.Args[3])
		if err := m.Run(boot); err != nil {
			fmt.Println("boot stopped:", err)
			return
		}
		for _, arg := range os.Args[4:] {
			var msg []byte
			for _, f := range strings.Fields(arg) {
				v, err := strconv.ParseUint(f, 16, 8)
				if err != nil {
					usage()
				}
				msg = append(msg, byte(v))
			}
			fmt.Printf("> %d bytes %s\n", len(msg), arg)
			m.MIDIIn(msg)
			if err := m.Run(gap); err != nil {
				fmt.Println("stopped:", err)
				return
			}
			out := m.MIDIOut()
			fmt.Printf("< %d bytes: % X\n", len(out), out)
		}
		fmt.Print(m.LCD.TextLayer(8))
	case "keys":
		boot, gap := intArg(os.Args[2]), intArg(os.Args[3])
		if err := m.Run(boot); err != nil {
			fmt.Println("boot stopped:", err)
			return
		}
		m.MIDIOut()                                // forget what the boot sent
		if pk := os.Getenv("EMU_POKE"); pk != "" { // EMU_POKE=ADDR=VAL,ADDR=VAL (hex bytes) before the keys
			for _, kv := range strings.Split(pk, ",") {
				var a, v uint32
				fmt.Sscanf(kv, "%x=%x", &a, &v)
				b.Write8(a, uint8(v))
			}
			m.Run(3000000)
		}
		prev := m.LCD.VRAM
		if w := os.Getenv("EMU_WATCH"); w != "" { // EMU_WATCH=LO-HI (hex) logs writes after boot
			var lo, hi uint32
			fmt.Sscanf(w, "%x-%x", &lo, &hi)
			b.OnWrite = func(a uint32, size int, v uint32) {
				if a+uint32(size) > lo && a <= hi {
					fmt.Printf("  write%d %06X = %X pc=%06X cycle %d\n", size, a, v, c.PC, c.Cycles)
				}
			}
		}
		fmt.Print("before:\n", m.LCD.TextLayer(8))
		for _, k := range os.Args[4:] {
			if err := m.Keys.Press(k, true); err != nil {
				fmt.Println(err)
				return
			}
			r0, h0 := m.Keys.Reads, m.Keys.Hits
			press, rel := 0, 0
			for i := 0; i < gap; i++ {
				switch c.PC {
				case 0x82ECE:
					press++
				case 0x82F66:
					rel++
				}
				if err := c.Step(); err != nil {
					fmt.Println("stopped:", err)
					return
				}
			}
			fmt.Printf("%s: key down handler reached %d times, key up handler %d times\n", k, press, rel)
			fmt.Printf("%s held: %d matrix reads, %d saw a pressed key, rows %v\n", k, m.Keys.Reads-r0, m.Keys.Hits-h0, m.Keys.rows)
			if out := m.MIDIOut(); len(out) > 0 {
				fmt.Printf("midi out while held: % X\n", out)
			}
			m.Keys.Press(k, false)
			m.Run(gap)
			if out := m.MIDIOut(); len(out) > 0 {
				fmt.Printf("midi out: % X\n", out)
			}
			diff := 0
			for i := range prev {
				if prev[i] != m.LCD.VRAM[i] {
					diff++
				}
			}
			prev = m.LCD.VRAM
			fmt.Printf("%s: %d display bytes changed\n", k, diff)
			fmt.Printf("after %s:\n%s", k, m.LCD.TextLayer(8))
		}
		if os.Getenv("EMU_PNG") != "" {
			m.LCD.PNG(os.Getenv("EMU_PNG"), 6, 64, 4)
		}
		if os.Getenv("EMU_LCDCMDS") != "" { // which display controller commands the firmware used
			for cmd, n := range m.LCD.Cmds {
				if n > 0 {
					fmt.Printf("lcd command %02X x%d\n", cmd, n)
				}
			}
		}
	case "calls":
		// emu calls BOOT GAP KEY: functions called only while KEY is pressed and released
		boot, gap := intArg(os.Args[2]), intArg(os.Args[3])
		if err := m.Run(boot); err != nil {
			fmt.Println("boot stopped:", err)
			return
		}
		record := func(n int) map[uint32]int {
			set := map[uint32]int{}
			for i := 0; i < n; i++ {
				in := Decode(b, c.PC)
				if err := c.Step(); err != nil {
					break
				}
				if in.Name == "jsr" || in.Name == "bsr" {
					set[c.PC]++
				}
			}
			return set
		}
		last := os.Args[len(os.Args)-1]
		for _, k := range os.Args[4 : len(os.Args)-1] { // earlier keys just get pressed and released
			m.Keys.Press(k, true)
			m.Run(gap)
			m.Keys.Press(k, false)
			m.Run(gap)
		}
		idle := record(gap * 2)
		m.Keys.Press(last, true)
		down := record(gap)
		m.Keys.Press(last, false)
		up := record(gap)
		for name, set := range map[string]map[uint32]int{"while held": down, "after release": up} {
			var list []uint32
			for a := range set {
				if idle[a] == 0 {
					list = append(list, a)
				}
			}
			sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
			fmt.Printf("%s, functions not called when idle:\n", name)
			for _, a := range list {
				fmt.Printf("  %06X x%d\n", a, set[a])
			}
		}
	case "syx":
		boot, settle, outFile := intArg(os.Args[2]), intArg(os.Args[3]), os.Args[4]
		if err := m.Run(boot); err != nil {
			fmt.Println("boot stopped:", err)
			return
		}
		m.Keys.Press("EXIT", true) // dismiss the Factory Set popup
		m.Run(3000000)
		m.Keys.Press("EXIT", false)
		m.Run(3000000)
		m.MIDIOut()
		send := func(msg []byte) []byte {
			out, err := m.SendSysEx(msg, settle)
			if err != nil {
				fmt.Println("stopped:", err)
				os.Exit(1)
			}
			return out
		}
		fmt.Printf("bulk on: % X\n", send([]byte{0xF0, 0x43, 0x10, 0x5F, 0x10, 0x00, 0x00, 0x01, 0xF7}))
		for _, f := range os.Args[5:] {
			data, err := os.ReadFile(f)
			if err != nil {
				fmt.Println(err)
				return
			}
			for i, msg := range SplitSysEx(data) {
				out := send(msg)
				fmt.Printf("%s #%d (%d bytes) -> %d bytes back\n", f, i, len(msg), len(out))
			}
		}
		fmt.Print(m.LCD.TextLayer(8))
		out := send([]byte{0xF0, 0x43, 0x20, 0x5F, 0x12, 0x00, 0x00, 0xF7})
		var reply []byte
		for _, msg := range SplitSysEx(out) {
			if len(msg) > 8 && msg[2] == 0x00 && msg[3] == 0x5F {
				reply = append(reply, msg...)
			}
		}
		fmt.Printf("pattern 1 request: %d bytes back, %d bytes of dump messages\n", len(out), len(reply))
		os.WriteFile(outFile, reply, 0o644)
		send([]byte{0xF0, 0x43, 0x10, 0x5F, 0x10, 0x00, 0x00, 0x00, 0xF7})
		fmt.Print(m.LCD.TextLayer(8))
		for _, r := range [][2]uint32{{0x40459A, 32}, {0x4047BC, 48}, {0x406D20, 32}} {
			fmt.Printf("%06X ", r[0])
			for i := uint32(0); i < r[1]; i++ {
				fmt.Printf(" %02X", b.Read8(r[0]+i))
			}
			fmt.Println()
		}
	case "keyscan":
		boot, gap := intArg(os.Args[2]), intArg(os.Args[3])
		if err := m.Run(boot); err != nil {
			fmt.Println("boot stopped:", err)
			return
		}
		tap := func(k string) {
			m.Keys.Press(k, true)
			m.Run(gap)
			m.Keys.Press(k, false)
			m.Run(gap)
		}
		tap("EXIT")
		for code := 0; code < 0x38; code++ {
			if _, _, ok := m.Keys.locate(uint8(code)); !ok {
				continue
			}
			prev := m.LCD.VRAM
			k := fmt.Sprintf("code:%02X", code)
			tap(k)
			diff := 0
			for i := range prev {
				if prev[i] != m.LCD.VRAM[i] {
					diff++
				}
			}
			title := m.LCD.TextLayer(1)
			fmt.Printf("%s: %5d bytes changed  screen id %02X%02X  text row 0 %q\n", k, diff, b.Read8(0x4084B4), b.Read8(0x4084B5), title[:22])
			if diff > 0 {
				tap("EXIT")
				tap("EXIT")
			}
		}
	case "dump":
		if err := m.Run(intArg(os.Args[2])); err != nil {
			fmt.Println("boot stopped:", err)
			return
		}
		a, n := hexArg(os.Args[3]), intArg(os.Args[4])
		for i := 0; i < n; i += 16 {
			fmt.Printf("%06X ", a+uint32(i))
			for j := 0; j < 16 && i+j < n; j++ {
				fmt.Printf(" %02X", b.Read8(a+uint32(i+j)))
			}
			fmt.Println()
		}
	case "watch":
		if len(os.Args) < 6 {
			usage()
		}
		n, lo, hi := intArg(os.Args[3]), hexArg(os.Args[4]), hexArg(os.Args[5])
		step := 0
		b.OnWrite = func(a uint32, size int, v uint32) {
			if a+uint32(size) > lo && a <= hi {
				fmt.Printf("%9d pc=%06X write%d %06X = %X\n", step, c.PC, size, a, v)
			}
		}
		for ; step < n; step++ {
			if err := c.Step(); err != nil {
				fmt.Println("stopped:", err)
				break
			}
		}
	case "trace", "run":
		if len(os.Args) < 4 {
			usage()
		}
		n := intArg(os.Args[3])
		from := 0
		if len(os.Args) > 4 {
			from = intArg(os.Args[4])
		}
		for i := 0; i < n; i++ {
			in := Decode(b, c.PC)
			if os.Args[1] == "trace" && i >= from {
				fmt.Printf("%7d %06X  %-30s sp=%08X er0=%08X er1=%08X er2=%08X er3=%08X er4=%08X er5=%08X er6=%08X ccr=%02X\n",
					i, in.PC, in, c.R[7], c.R[0], c.R[1], c.R[2], c.R[3], c.R[4], c.R[5], c.R[6], c.CCR)
			}
			if err := c.Step(); err != nil {
				fmt.Println("stopped:", err)
				break
			}
			if c.Halted {
				fmt.Printf("sleep at %06X\n", c.PC)
				break
			}
		}
		fmt.Printf("pc=%06X sp=%08X cycles=%d\n", c.PC, c.R[7], c.Cycles)
		if os.Args[1] == "run" {
			fmt.Println("accesses to unmapped addresses (first access: addr R/W value pc, count):")
			var keys []string
			for k := range b.Unmapped {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				x := b.Unmapped[k]
				fmt.Printf("  %06X %s %02X pc=%06X n=%d\n", x.Addr, rw(x.Write), x.Val, x.PC, x.Count)
			}
			fmt.Println("unmodelled on chip register accesses (first access: addr R/W value pc, total count):")
			for _, x := range m.Chip.Unmodelled() {
				fmt.Printf("  %06X %s %02X pc=%06X n=%d\n", x.Addr, rw(x.Write), x.Val, x.PC, x.Count)
			}
		}
	default:
		usage()
	}
}

func rw(write bool) string {
	if write {
		return "W"
	}
	return "R"
}
