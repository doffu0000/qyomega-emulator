// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func newStaticServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := NewServer(newTestMachine(t))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestVendorFilesAreServedAsModules(t *testing.T) {
	ts := newStaticServer(t)
	for _, p := range []string{
		"/vendor/spessasynth_lib/index.js",
		"/vendor/spessasynth_lib/spessasynth_processor.min.js",
		"/vendor/spessasynth_core/index.js",
		"/vendor/fflate/browser.js",
		"/vendor/stb-vorbis/index.js",
		"/sfengine.js",
	} {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || len(body) < 1000 {
			t.Errorf("%s: status %d, %d bytes", p, resp.StatusCode, len(body))
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
			t.Errorf("%s: content type %q, module scripts need a JavaScript type", p, ct)
		}
	}
}

func TestStaticRoutesStayInTheirFolders(t *testing.T) {
	ts := newStaticServer(t)
	for _, p := range []string{
		"/vendor/", "/vendor/spessasynth_lib/", "/soundfonts/", "/vendor/nothing.js",
		"/vendor/../main.go", "/vendor/..%2fmain.go", "/vendor/%2e%2e/main.go", "/soundfonts/..%5cmain.go",
		"/vendor/spessasynth_lib%5cindex.js", "/vendor/C:/Windows/win.ini",
	} {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 200 && !strings.Contains(string(body), "<!doctype html>") {
			t.Errorf("%s was served (%d bytes), want a refusal", p, len(body))
		}
	}
}

func TestSoundfontList(t *testing.T) {
	ts := newStaticServer(t)
	resp, err := http.Get(ts.URL + "/soundfonts.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list []fontInfo
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	onDisk, _ := filepath.Glob(filepath.Join(fontDir, "*.sf2"))
	if len(list) < len(onDisk) {
		t.Fatalf("listed %d soundfonts, %d .sf2 files on disk", len(list), len(onDisk))
	}
	for _, f := range list {
		if strings.ContainsAny(f.Name, `/\`) || f.Size <= 0 {
			t.Errorf("bad entry %+v", f)
		}
		// the file itself is reachable by that name
		r, err := http.Head(ts.URL + "/soundfonts/" + f.Name)
		if err != nil || r.StatusCode != 200 {
			t.Errorf("%s: HEAD %v %v", f.Name, r, err)
		}
	}
}

// Every bare import in the vendored modules and in sfengine.js must have an entry in the
// import map of index.html, or the page fails at run time with nothing in the Go suite to say so.
func TestImportMapCoversBareImports(t *testing.T) {
	page, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)<script type="importmap">(.*?)</script>`).FindSubmatch(page)
	if m == nil {
		t.Fatal("no import map in index.html")
	}
	var im struct{ Imports map[string]string }
	if err := json.Unmarshal(m[1], &im); err != nil {
		t.Fatal(err)
	}
	for name, target := range im.Imports {
		if _, err := os.Stat(strings.TrimPrefix(target, "/")); err != nil {
			t.Errorf("import map %s points at %s: %v", name, target, err)
		}
	}
	from := regexp.MustCompile(`(?m)^\s*(?:import|export)[^'"\n]*?from\s*["']([^"']+)["']`)
	files, _ := filepath.Glob("vendor/*/*.js")
	files = append(files, "sfengine.js")
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, mm := range from.FindAllSubmatch(src, -1) {
			spec := string(mm[1])
			if strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") {
				continue
			}
			if _, ok := im.Imports[spec]; !ok {
				t.Errorf("%s imports %q, which the import map does not know", f, spec)
			}
		}
	}
}
