package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeKmem(t *testing.T, dir, ver, guard string) {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "kmem"), []byte("#!/usr/bin/env python3\nKMEM_VERSION = \""+ver+"\"\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "kmem-guard"), []byte(guard), 0o755)
}

func TestUpdateKmem(t *testing.T) {
	if !versionLess("1.3.1", "1.4.0") || !versionLess("1.9", "1.10") || versionLess("1.4.0", "1.4") || versionLess("2.0", "1.9.9") {
		t.Fatal("versionLess")
	}
	bundled := t.TempDir()
	writeKmem(t, bundled, "1.4.0", "new guard")

	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	writeKmem(t, bin, "1.3.1", "old guard")
	msg, err := updateKmem(home, bundled)
	if err != nil || !strings.Contains(msg, "1.3.1 to 1.4.0") {
		t.Fatalf("older kmem is updated: %q %v", msg, err)
	}
	if kmemScriptVersion(filepath.Join(bin, "kmem")) != "1.4.0" {
		t.Fatal("kmem not replaced")
	}
	if b, _ := os.ReadFile(filepath.Join(bin, "kmem-guard")); string(b) != "new guard" {
		t.Fatal("kmem-guard not replaced")
	}
	if kmemScriptVersion(filepath.Join(bin, "kmem.old")) != "1.3.1" {
		t.Fatal("old kmem kept as kmem.old")
	}
	if st, _ := os.Stat(filepath.Join(bin, "kmem")); st.Mode().Perm() != 0o755 {
		t.Fatal("kmem stays executable")
	}

	for name, setup := range map[string]func(bin string){
		"same":  func(bin string) { writeKmem(t, bin, "1.4.0", "mine") },
		"newer": func(bin string) { writeKmem(t, bin, "1.5.0", "mine") },
		"unknown": func(bin string) {
			os.MkdirAll(bin, 0o755)
			os.WriteFile(filepath.Join(bin, "kmem"), []byte("mine"), 0o755)
		},
		"missing": func(bin string) {},
		"symlink": func(bin string) {
			src := t.TempDir()
			writeKmem(t, src, "1.0.0", "mine")
			os.MkdirAll(bin, 0o755)
			os.Symlink(filepath.Join(src, "kmem"), filepath.Join(bin, "kmem"))
		},
	} {
		home := t.TempDir()
		bin := filepath.Join(home, ".local", "bin")
		setup(bin)
		if msg, err := updateKmem(home, bundled); msg != "" || err != nil {
			t.Errorf("%s: left alone, got %q %v", name, msg, err)
		}
		if _, err := os.Stat(filepath.Join(bin, "kmem.old")); err == nil {
			t.Errorf("%s: no backup when nothing changes", name)
		}
	}
}

// The kmem bundled in third_party must be the installed kmem release, so a
// Quill Desktop release never ships an older kmem than the one in use
// (scripts/vendor-kmem.sh, make kmem-sync).
func TestBundledKmemIsCurrent(t *testing.T) {
	bundled := kmemScriptVersion(filepath.Join("..", "..", "third_party", "kmem", "kmem"))
	if bundled == "" || bundledKmemVersion(filepath.Join("..", "..", "third_party", "kmem")) != bundled {
		t.Fatalf("third_party/kmem: script %q and VERSION disagree", bundled)
	}
	p, err := exec.LookPath("kmem")
	if err != nil {
		t.Skip("kmem not installed")
	}
	home, _ := os.UserHomeDir()
	if installed := installedKmemVersion(home, p); installed != "" && versionLess(bundled, installed) {
		t.Fatalf("third_party/kmem is %s but kmem %s is installed: run make kmem-sync", bundled, installed)
	}
}

// installedKmemVersion: the kmem release in use. Since kmem 1.8.0,
// ~/.local/bin/kmem is a launcher without KMEM_VERSION that runs
// releases/<v>/kmem; during a staged rollout the bundle must match the
// stable release (rollout.json "stable"), not a candidate some sessions
// try. Older installs carry KMEM_VERSION in the script; else ask
// `kmem version --json`.
func installedKmemVersion(home, script string) string {
	if b, err := os.ReadFile(filepath.Join(home, ".local", "share", "kmem", "rollout.json")); err == nil {
		var r struct {
			Stable string `json:"stable"`
		}
		if json.Unmarshal(b, &r) == nil && r.Stable != "" {
			return r.Stable
		}
	}
	if v := kmemScriptVersion(script); v != "" {
		return v
	}
	out, err := exec.Command(script, "version", "--json").Output()
	if err != nil {
		return ""
	}
	var v struct {
		Kmem string `json:"kmem"`
	}
	if json.Unmarshal(out, &v) != nil {
		return ""
	}
	return v.Kmem
}

func TestInstalledKmemVersion(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	os.MkdirAll(bin, 0o755)
	script := filepath.Join(bin, "kmem")
	// Old single-file install: the script's KMEM_VERSION.
	os.WriteFile(script, []byte("#!/usr/bin/env python3\nKMEM_VERSION = \"1.6.3\"\n"), 0o755)
	if v := installedKmemVersion(home, script); v != "1.6.3" {
		t.Fatalf("script version: %q", v)
	}
	// Launcher (no KMEM_VERSION): `kmem version --json`.
	os.WriteFile(script, []byte("#!/bin/sh\necho '{\"kmem\": \"1.8.0\", \"api\": 1}'\n"), 0o755)
	if v := installedKmemVersion(home, script); v != "1.8.0" {
		t.Fatalf("launcher version: %q", v)
	}
	// A staged rollout: the stable release counts, not the candidate.
	share := filepath.Join(home, ".local", "share", "kmem")
	os.MkdirAll(share, 0o755)
	os.WriteFile(filepath.Join(share, "rollout.json"), []byte(`{"stable": "1.7.0", "candidate": "1.8.0"}`), 0o644)
	if v := installedKmemVersion(home, script); v != "1.7.0" {
		t.Fatalf("rollout stable: %q", v)
	}
}
