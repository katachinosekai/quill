package main

import (
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
	if installed := kmemScriptVersion(p); installed != "" && versionLess(bundled, installed) {
		t.Fatalf("third_party/kmem is %s but kmem %s is installed: run make kmem-sync", bundled, installed)
	}
}
