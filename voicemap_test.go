// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// sf2Presets reads the preset headers of a SoundFont 2 file: (bank, program) -> name.
func sf2Presets(t *testing.T, path string) map[[2]int]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no soundfont: ", err)
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "sfbk" {
		t.Fatalf("%s is not an SF2 file", path)
	}
	walk := func(pos, end int, fn func(id string, body []byte)) {
		for pos+8 <= end {
			size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
			if pos+8+size > len(data) {
				return
			}
			fn(string(data[pos:pos+4]), data[pos+8:pos+8+size])
			pos += 8 + size + size&1
		}
	}
	out := map[[2]int]string{}
	walk(12, len(data), func(id string, body []byte) {
		if id != "LIST" || string(body[:4]) != "pdta" {
			return
		}
		pos, end := 4, len(body)
		for pos+8 <= end {
			size := int(binary.LittleEndian.Uint32(body[pos+4 : pos+8]))
			if string(body[pos:pos+4]) == "phdr" {
				h := body[pos+8 : pos+8+size]
				for i := 0; i+38 <= len(h)-38; i += 38 { // the last record is the terminator
					name := strings.TrimRight(string(h[i:i+20]), "\x00")
					prog := int(binary.LittleEndian.Uint16(h[i+20:]))
					bank := int(binary.LittleEndian.Uint16(h[i+22:]))
					out[[2]int{bank, prog}] = name
				}
			}
			pos += 8 + size + size&1
		}
	})
	return out
}

type voiceEntry struct {
	N string         `json:"n"`
	B *int           `json:"b"`
	P *int           `json:"p"`
	T map[string]int `json:"t"`
}

// Every voice map beside a soundfont must point at presets that exist in that soundfont, and
// use only the controllers the engine adds offsets to.
func TestVoiceMapsPointAtRealPresets(t *testing.T) {
	maps, _ := filepath.Glob(filepath.Join(fontDir, "*.voicemap.json"))
	if len(maps) == 0 {
		t.Skip("no voice maps")
	}
	allowed := map[string]bool{"71": true, "72": true, "73": true, "74": true, "75": true, "91": true, "93": true}
	for _, mp := range maps {
		sf := strings.TrimSuffix(mp, ".voicemap.json") + ".sf2"
		presets := sf2Presets(t, sf)
		raw, err := os.ReadFile(mp)
		if err != nil {
			t.Fatal(err)
		}
		var m struct {
			Voices map[string]voiceEntry `json:"voices"`
			Drums  map[string]voiceEntry `json:"drums"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", mp, err)
		}
		if len(m.Voices) < 300 || len(m.Drums) < 20 {
			t.Errorf("%s: only %d voices and %d drum kits", mp, len(m.Voices), len(m.Drums))
		}
		for key, e := range m.Voices {
			parts := strings.Split(key, ":")
			if len(parts) != 3 {
				t.Errorf("voice key %q", key)
				continue
			}
			prog, _ := strconv.Atoi(parts[2])
			bank, program := 0, prog
			if e.B != nil {
				bank = *e.B
			}
			if e.P != nil {
				program = *e.P
			}
			if _, ok := presets[[2]int{bank, program}]; !ok {
				t.Errorf("%s: voice %s %q maps to soundfont preset %d/%d, which does not exist", mp, key, e.N, bank, program)
			}
			for cc, v := range e.T {
				if !allowed[cc] || v < -127 || v > 127 {
					t.Errorf("%s: voice %s has tweak %s=%d", mp, key, cc, v)
				}
			}
		}
		for key, e := range m.Drums {
			if e.P == nil {
				t.Errorf("%s: drum kit %s has no program", mp, key)
				continue
			}
			if _, ok := presets[[2]int{120, *e.P}]; !ok {
				t.Errorf("%s: drum kit %s %q maps to kit %d, which the soundfont does not have in bank 120", mp, key, e.N, *e.P)
			}
		}
	}
}
