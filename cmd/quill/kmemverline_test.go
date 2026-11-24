package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

func TestKmemVerState(t *testing.T) {
	if (kmemVerState{}).label() != "" || (kmemVerState{installed: "1.12.0"}).label() != "kmem 1.12.0" {
		t.Fatal("label: the installed version only")
	}
}

func TestKmemVersionCached(t *testing.T) {
	h := newHarness(t)
	h.d.kmemOff = false
	h.d.kmemVer.reset()
	calls := 0
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		if strings.Join(args, " ") == "version --json" {
			calls++
			return `{"kmem": "1.7.0", "api": 1}`, nil
		}
		return "", nil
	}
	if v := h.d.kmemInstalledVersion(); v != "1.7.0" {
		t.Fatalf("version %q", v)
	}
	h.d.kmemAPILevel()
	if calls != 1 {
		t.Fatalf("one kmem version call serves the API level and the version: %d", calls)
	}
}

func TestKmemVerLine(t *testing.T) {
	h := newHarness(t)
	h.d.kmemOff = false
	h.d.kmemRun = func(string, ...string) (string, error) { return "", nil }
	h.d.app.Preferences().SetString(memoryModeKey, memoryModeKmem)
	h.ui(func() {
		l := h.d.verLine
		if l == nil || l.tap.Visible() {
			t.Fatal("hidden until the version is known")
		}
		h.d.setKmemVer(kmemVerState{installed: "1.7.0"})
		if !l.tap.Visible() || l.ver.Text != "kmem 1.7.0" {
			t.Fatalf("plain version: %q", l.ver.Text)
		}
		h.d.flashHint(h.d.folder, "Enter sends · Shift+Enter new line")
		// Both sit in the row under the message box, the version left of the hints.
		h.w.Resize(fyne.NewSize(1600, 900))
		row := h.d.statsRow
		row.Layout.Layout(row.Objects, fyne.NewSize(600, 20))
		if !l.slot.Visible() || !(l.slot.Position().X >= 0 && l.slot.Position().X+l.slot.MinSize().Width <= h.d.folder.Position().X) {
			t.Fatalf("both fit: version %v left of hints %v", l.slot.Position(), h.d.folder.Position())
		}
		narrow := h.d.folder.MinSize().Width + 2*theme.Padding() + 4
		row.Layout.Layout(row.Objects, fyne.NewSize(narrow, 20))
		if !h.d.folder.Visible() || l.slot.Visible() {
			t.Fatal("too narrow for both: the flashed hint wins, the version steps aside")
		}
		row.Layout.Layout(row.Objects, row.Size())
		// The hint line changing width lays the row out again.
		row.Resize(fyne.NewSize(narrow, 20))
		h.d.flashHint(h.d.folder, "ok")
		if !l.slot.Visible() {
			t.Fatal("a short hint leaves room for the version again")
		}
		h.d.app.Preferences().SetString(memoryModeKey, memoryModeClaude)
		h.d.setKmemVer(kmemVerState{installed: "1.7.0"})
		if l.tap.Visible() {
			t.Fatal("hidden when sessions use Claude's own memory")
		}
		h.d.app.Preferences().SetString(memoryModeKey, memoryModeKmem)
		h.d.setKmemVer(kmemVerState{})
		if l.tap.Visible() {
			t.Fatal("hidden when kmem is missing")
		}
		h.d.setKmemVer(kmemVerState{installed: "1.7.0"})
	})
	if os.Getenv("DESK_SHOTS") != "" {
		dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
		os.MkdirAll(dir, 0o755)
		h.ui(func() { shot(t, h.w, dir, "kmem-version-line.png") })
	}
}
