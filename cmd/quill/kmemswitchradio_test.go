package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func findRadio(o fyne.CanvasObject) *widget.RadioGroup {
	var r *widget.RadioGroup
	deepWalk(o, func(x fyne.CanvasObject) {
		if g, ok := x.(*widget.RadioGroup); ok && r == nil {
			r = g
		}
	})
	return r
}

const (
	radioKmem   = "kmem (shared memory: handoffs, tasks, rules)"
	radioClaude = "Claude's auto memory (MEMORY.md per folder)"
)

// TestMemoryModeRadioGoesThroughSwitch: Settings → Memory's radio (also in
// /memory) opens "Switch to kmem" instead of flipping the mode; cancel keeps
// Claude, a completed switch selects kmem; Claude after the switch opens the
// undo. settings.json follows CLAUDE_CONFIG_DIR throughout.
func TestMemoryModeRadioGoesThroughSwitch(t *testing.T) {
	home := switchHome(t, userSettings)
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(userSettings), 0o600)
	h := newHarness(t)
	bin, _ := fakeKmemBinary(t, fakeKmemOpts{})
	changed := 0
	var r *widget.RadioGroup
	h.ui(func() {
		useFakeKmem(h.d, bin)
		h.d.kmemOff = false
		// Claude chosen before (with no choice an installed kmem is the
		// default: TestKmemMemoryDefault)
		h.d.app.Preferences().SetString(memoryModeKey, memoryModeClaude)
		r = findRadio(h.d.memoryModeRadio(func() { changed++ }))
		if r.Selected != radioClaude || r.Disabled() {
			t.Fatalf("starts on Claude, enabled: %q", r.Selected)
		}
		r.SetSelected(radioKmem)
		if r.Selected != radioClaude || h.d.app.Preferences().String(memoryModeKey) == memoryModeKmem || h.d.kmemSwitchUI == nil {
			t.Fatalf("kmem opens the switch dialog, the radio stays on Claude: %q", r.Selected)
		}
	})
	h.eventually("preview", func() bool { return overlayButton(h, "Switch") != nil })
	h.ui(func() {
		test.Tap(overlayButton(h, "Cancel"))
		if r.Selected != radioClaude || h.d.kmemMemory() || changed != 1 {
			t.Fatalf("cancel leaves Claude: %q changed=%d", r.Selected, changed)
		}
		r.SetSelected(radioKmem)
	})
	h.eventually("preview again", func() bool { return overlayButton(h, "Switch") != nil })
	h.ui(func() { test.Tap(overlayButton(h, "Switch")) })
	h.eventually("switched", func() bool { return h.d.kmemSwitchUI.result != nil })
	h.ui(func() {
		if err := h.d.kmemSwitchUI.result.Err; err != nil {
			t.Fatal(err)
		}
		test.Tap(overlayButton(h, "Close"))
		if r.Selected != radioKmem || !h.d.kmemMemory() || changed != 2 {
			t.Fatalf("a completed switch selects kmem: %q changed=%d", r.Selected, changed)
		}
	})
	m, err := readSettings(filepath.Join(cfg, "settings.json"))
	if err != nil || m["autoMemoryEnabled"] != false || !kmemHooksAnyPresent(home) {
		t.Fatalf("the switch writes $CLAUDE_CONFIG_DIR/settings.json: %v %v", m, err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json")); string(b) != userSettings {
		t.Fatal("~/.claude/settings.json stays untouched with CLAUDE_CONFIG_DIR set")
	}

	// Claude with a recorded switch: the undo confirmation; cancel keeps kmem.
	h.ui(func() {
		r.SetSelected(radioClaude)
		if r.Selected != radioKmem || overlayButton(h, "Switch back") == nil {
			t.Fatal("Claude opens Switch back to Claude memory, the radio stays on kmem")
		}
		test.Tap(overlayButton(h, "Cancel"))
		if r.Selected != radioKmem || !h.d.kmemMemory() || changed != 3 {
			t.Fatalf("cancelled undo keeps kmem: %q", r.Selected)
		}
		r.SetSelected(radioClaude)
		test.Tap(overlayButton(h, "Switch back"))
		if r.Selected != radioClaude || h.d.kmemMemory() || h.d.kmemSwitchUndoDir() != "" || changed != 4 {
			t.Fatalf("undo runs and selects Claude: %q", r.Selected)
		}
	})
	m, _ = readSettings(filepath.Join(cfg, "settings.json"))
	if _, set := m["autoMemoryEnabled"]; set || kmemHooksAnyPresent(home) || m["model"] != "placeholder-model" {
		t.Fatalf("undo reverts $CLAUDE_CONFIG_DIR/settings.json: %v", m)
	}

	// Without a recorded switch Claude is just set.
	h.ui(func() {
		for _, o := range h.w.Canvas().Overlays().List() {
			h.w.Canvas().Overlays().Remove(o)
		}
		h.d.app.Preferences().SetString(memoryModeKey, memoryModeKmem)
		r = findRadio(h.d.memoryModeRadio(nil))
		r.SetSelected(radioClaude)
		if h.d.app.Preferences().String(memoryModeKey) != memoryModeClaude || r.Selected != radioClaude || overlayButton(h, "Switch back") != nil {
			t.Fatal("no switch recorded: Claude is set directly")
		}
	})
}

// TestClaudeSettingsFileConfigDir: Claude Code's settings.json follows
// CLAUDE_CONFIG_DIR for every reader and writer.
func TestClaudeSettingsFileConfigDir(t *testing.T) {
	home := switchHome(t, userSettings)
	os.WriteFile(filepath.Join(home, ".local", "bin", "kmem"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	if got := claudeSettingsFile(home); got != filepath.Join(home, ".claude", "settings.json") {
		t.Fatalf("default %s", got)
	}
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	want := filepath.Join(cfg, "settings.json")
	if got := claudeSettingsFile(home); got != want {
		t.Fatalf("CLAUDE_CONFIG_DIR: %s", got)
	}
	if settingsFiles("/placeholder/proj")[0][1] != want {
		t.Fatal("the User settings file in Permissions/Hooks")
	}
	if !autoMemoryOn() {
		t.Fatal("no settings.json in CLAUDE_CONFIG_DIR: auto memory on")
	}
	if err := setAutoMemory(false); err != nil || autoMemoryOn() {
		t.Fatalf("the /memory switch writes CLAUDE_CONFIG_DIR: %v", err)
	}
	if _, err := addKmemHooks(home, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !kmemHooksPresent(home) || !kmemHooksAnyPresent(home) {
		t.Fatal("kmem hooks found in CLAUDE_CONFIG_DIR")
	}
	if err := removeLeftKmemHooks(home); err != nil || kmemHooksAnyPresent(home) {
		t.Fatalf("removeLeftKmemHooks: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json")); string(b) != userSettings {
		t.Fatal("~/.claude/settings.json untouched")
	}
}

// TestWelcomeKmemGoesThroughSwitch: Welcome's kmem choice doesn't set the
// mode; the switch dialog opens after it, also when kmem is already the
// default (no mode chosen, never switched).
func TestWelcomeKmemGoesThroughSwitch(t *testing.T) {
	switchHome(t, "")
	h := newHarness(t)
	bin, _ := fakeKmemBinary(t, fakeKmemOpts{})
	h.ui(func() {
		useFakeKmem(h.d, bin)
		h.d.kmemOff = false
		p := h.d.app.Preferences()
		c := welcomeChoices{Lang: "en", KmemMemory: true}
		if !h.d.kmemMemory() || !h.d.welcomeMemoryNext(c) {
			t.Fatal("kmem by default, never switched: the switch comes next")
		}
		h.d.applyWelcome(c)
		if p.String(memoryModeKey) != "" || !h.d.kmemMemory() || !h.d.welcomeMemoryNext(c) {
			t.Fatal("Welcome alone stores nothing; kmem stays the default until the switch")
		}
		p.SetString(memoryModeKey, memoryModeClaude)
		if !h.d.welcomeMemoryNext(c) {
			t.Fatal("kmem not in use: the switch comes next")
		}
		h.d.applyWelcome(c)
		if p.String(memoryModeKey) != memoryModeClaude || h.d.kmemMemory() {
			t.Fatal("Welcome alone doesn't switch")
		}
		if h.d.welcomeMemoryNext(welcomeChoices{Lang: "en"}) {
			t.Fatal("Claude without a recorded switch needs no dialog")
		}
	})
}
