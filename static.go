// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Files the page needs besides index.html: the vendored soundfont synthesizer and the
// soundfonts, read from disk next to the program (like boot.snap), not embedded.
const (
	vendorDir = "vendor"
	fontDir   = "soundfonts"
)

//go:embed sfengine.js
var sfEngineJS []byte

func (s *Server) handleSFEngine(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(sfEngineJS)
}

// contentType is set explicitly: on Windows Go reads the registry for the type of .js, which
// can say text/plain, and browsers refuse module scripts served that way.
func contentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".json":
		return "application/json"
	case ".wasm":
		return "application/wasm"
	case ".txt", ".md":
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}

// staticHandler serves regular files below dir at prefix. No directory listings and no way
// out of dir.
func staticHandler(prefix, dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, prefix)
		if rel == "" || strings.HasSuffix(rel, "/") || strings.Contains(rel, "..") || strings.ContainsAny(rel, `\:`) {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil || fi.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType(rel))
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, rel, fi.ModTime(), f)
	})
}

type fontInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// handleSoundfonts lists the soundfont files in fontDir.
func (s *Server) handleSoundfonts(w http.ResponseWriter, r *http.Request) {
	list := []fontInfo{}
	if entries, err := os.ReadDir(fontDir); err == nil {
		for _, e := range entries {
			switch strings.ToLower(filepath.Ext(e.Name())) {
			case ".sf2", ".sf3", ".dls", ".sfogg":
				if info, err := e.Info(); err == nil && !e.IsDir() {
					list = append(list, fontInfo{e.Name(), info.Size()})
				}
			}
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(list)
}
