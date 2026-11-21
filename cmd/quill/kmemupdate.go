package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var kmemVersionRe = regexp.MustCompile(`(?m)^KMEM_VERSION = "([0-9][0-9.]*)"`)

// kmemScriptVersion reads KMEM_VERSION from a kmem script ("" if unknown).
func kmemScriptVersion(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if m := kmemVersionRe.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}

// versionLess compares dotted numeric versions (1.3.1 < 1.4.0 < 1.10.0).
func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// updateKmem keeps kmem in ~/.local/bin as new as the kmem that comes with
// Quill: when the installed kmem is an older release, Quill replaces kmem and
// kmem-guard (program files only; the store in ~/.local/share/kmem and the
// config are never touched) and keeps the old files as *.old. Symlinks (a
// kmem run from a checkout), unknown versions and newer kmems are left alone.
// Returns what changed ("" when nothing did).
func updateKmem(home, bundled string) (string, error) {
	if bundled == "" {
		return "", nil
	}
	want := kmemScriptVersion(filepath.Join(bundled, "kmem"))
	bin := filepath.Join(home, ".local", "bin")
	dst := filepath.Join(bin, "kmem")
	st, err := os.Lstat(dst)
	if err != nil || !st.Mode().IsRegular() {
		return "", nil
	}
	have := kmemScriptVersion(dst)
	if want == "" || have == "" || !versionLess(have, want) {
		return "", nil
	}
	for _, f := range []string{"kmem", "kmem-guard"} {
		p := filepath.Join(bin, f)
		if st, err := os.Lstat(p); err == nil && !st.Mode().IsRegular() {
			continue
		}
		if b, err := os.ReadFile(p); err == nil {
			if err := os.WriteFile(p+".old", b, 0o644); err != nil {
				return "", err
			}
		}
		if err := copyExec(filepath.Join(bundled, f), p); err != nil {
			return "", err
		}
	}
	msg := "Updated kmem " + have + " to " + want + " (the kmem that comes with this Quill Desktop)."
	// kmem 1.5.2 guards the PowerShell tool: existing kmem-guard hooks get
	// PowerShell in their matcher (settings backed up first).
	if kmemHooksPresent(home) {
		changed, _, err := mergeKmemHooks(filepath.Join(home, ".claude", "settings.json"), home, kmemNow())
		if err != nil {
			return msg, err
		}
		if changed {
			msg += " The kmem-guard hook now also checks PowerShell commands."
		}
	}
	return msg, nil
}

// kmemNow is the clock for settings backups (tests replace it).
var kmemNow = time.Now
