package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func kmemSetupRow(h *harness) setupRow {
	for _, r := range h.d.setupRows(nil) {
		if strings.HasPrefix(r.title, "kmem") {
			return r
		}
	}
	return setupRow{}
}

// findButton: the first button with text in o's tree.
func findButton(o fyne.CanvasObject, text string) *widget.Button {
	switch w := o.(type) {
	case *widget.Button:
		if w.Text == text {
			return w
		}
	case *fyne.Container:
		for _, c := range w.Objects {
			if b := findButton(c, text); b != nil {
				return b
			}
		}
	case *container.Scroll:
		return findButton(w.Content, text)
	}
	return nil
}

// TestNoKmemReducedMode: kmem is not part of Quill Desktop (user
// 2026-11-24). Without an installed kmem sessions use Claude's auto memory,
// the kmem button is a Memory button for MEMORY.md, and Setup shows the
// "Get kmem" hint; an installed kmem brings everything back.
func TestNoKmemReducedMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KMEM_CONFIG", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	h := newHarness(t) // kmemOff: no kmem installed
	h.ui(func() {
		p := h.d.app.Preferences()
		bt := h.d.box().btns[boxKmem]
		if bt == nil || bt.btn.Text != "Memory" || !bt.box.Visible() {
			t.Fatalf("no kmem: the toolbar shows a Memory button: %+v", bt)
		}
		if bt.side != nil && bt.side.Text != "Memory" {
			t.Fatalf("and the sidebar too: %q", bt.side.Text)
		}
		if h.d.kmemMemory() {
			t.Fatal("no kmem: Claude's auto memory by default")
		}
		p.SetString(memoryModeKey, memoryModeKmem)
		if h.d.kmemMemory() || strings.Contains(h.d.sessionPromptFor(&deskSession{id: "x"}, h.d.kmemMemory()), kmemMemoryHint) {
			t.Fatal("no kmem: Claude's memory and no kmem hints even when kmem memory was picked")
		}
		if h.d.kmemSwitchOffer() || h.d.kmemSwitch.Visible() {
			t.Fatal("no kmem: nothing to switch to")
		}
		row := kmemSetupRow(h)
		if row.ok || !row.optional || row.state != kmemGetHint || row.action != "Get kmem..." || row.do == nil {
			t.Fatalf("Setup: the optional Get kmem hint: %+v", row)
		}
		if !strings.Contains(kmemGetHint, "Patreon") {
			t.Fatalf("the hint says where kmem comes from: %q", kmemGetHint)
		}
		test.Tap(bt.btn)
		if h.d.assistBoxRole() == boxKmem {
			t.Fatal("no kmem: the kmem box doesn't open")
		}
		if overlayButton(h, "Save") == nil && overlayButton(h, "Learn more") == nil {
			t.Fatal("the Memory button opens Claude's memory panel")
		}
		h.d.applyWelcome(welcomeChoices{Lang: "en", NoKmem: true})
		if p.String(memoryModeKey) != memoryModeKmem {
			t.Fatal("the Welcome without kmem keeps the memory choice for when kmem is installed")
		}

		// kmem installed: the kmem button comes back.
		h.d.kmemOff = false
		h.d.syncKmemButton()
		if bt.btn.Text != "kmem" || bt.side != nil && bt.side.Text != "kmem" {
			t.Fatal("installed kmem: the kmem button")
		}
		if kmemSetupRow(h).state == kmemGetHint {
			t.Fatal("installed kmem: no Get kmem hint")
		}
		if !h.d.kmemMemory() {
			t.Fatal("installed kmem with kmem memory picked: sessions use kmem")
		}
		h.d.kmemOff = true
		h.d.syncKmemButton()
		if bt.btn.Text != "Memory" {
			t.Fatal("and Memory again when it is gone")
		}
	})
}

// TestClaudeMemoryPane edits the project's MEMORY.md.
func TestClaudeMemoryPane(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	h := newHarness(t)
	cwd := filepath.Join(t.TempDir(), "proj")
	path := autoMemoryPath(cwd)
	if !strings.HasPrefix(path, cfg) || filepath.Base(path) != "MEMORY.md" {
		t.Fatalf("Claude Code's memory folder: %s", path)
	}
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte("- placeholder note\n"), 0o600)
	h.ui(func() {
		pane := h.d.claudeMemoryPane(cwd, func() fyne.Window { return h.w })
		var ed *widget.Entry
		var walk func(o fyne.CanvasObject)
		walk = func(o fyne.CanvasObject) {
			switch w := o.(type) {
			case *widget.Entry:
				ed = w
			case *fyne.Container:
				for _, c := range w.Objects {
					walk(c)
				}
			}
		}
		walk(pane)
		if ed == nil || ed.Text != "- placeholder note\n" {
			t.Fatal("MEMORY.md is shown")
		}
		ed.SetText("- placeholder note\n- another placeholder\n")
		test.Tap(findButton(pane, "Save"))
	})
	if b, _ := os.ReadFile(path); string(b) != "- placeholder note\n- another placeholder\n" {
		t.Fatalf("saved: %q", b)
	}
}

// TestKmemAdvert: the advert shows in Settings → General → Memory, the Memory
// tab, the Claude memory panel and the Welcome, only without kmem; a dismiss
// hides every one for good.
func TestKmemAdvert(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	h := newHarness(t)
	h.ui(func() {
		p := h.d.app.Preferences()
		h.d.kmemAdverts = nil
		settings := h.d.kmemSettings()
		memTab := h.d.memoryPane(nil, t.TempDir())
		panel := h.d.claudeMemoryPane(t.TempDir(), func() fyne.Window { return h.w })
		h.d.showWelcome(nil)
		if len(h.d.kmemAdverts) != 4 {
			t.Fatalf("four placements: %d", len(h.d.kmemAdverts))
		}
		for i, c := range h.d.kmemAdverts {
			if !c.Visible() {
				t.Fatalf("advert %d shows without kmem", i)
			}
		}
		for _, o := range []fyne.CanvasObject{settings, memTab, panel} {
			if findButton(o, "Learn more") == nil {
				t.Fatal("each has Learn more")
			}
		}
		if !strings.Contains(kmemAdvertText, "Patreon") {
			t.Fatal(kmemAdvertText)
		}
		h.d.dismissKmemAdvert()
		if !p.Bool(prefKmemAdvertOff) {
			t.Fatal("the dismiss is remembered")
		}
		for i, c := range h.d.kmemAdverts {
			if c.Visible() {
				t.Fatalf("advert %d hidden after a dismiss", i)
			}
		}
		if h.d.kmemAdvert().Visible() {
			t.Fatal("and new ones stay hidden")
		}
		p.SetBool(prefKmemAdvertOff, false)
		h.d.kmemOff = false
		if h.d.kmemAdvert().Visible() {
			t.Fatal("never shown with kmem installed")
		}
	})
}

// TestKmemSwitchOffer: with kmem installed and sessions on Claude's memory,
// the header offers once to switch to kmem.
func TestKmemSwitchOffer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	h := newHarness(t)
	h.ui(func() {
		p := h.d.app.Preferences()
		p.SetString(memoryModeKey, memoryModeClaude)
		h.d.kmemOff = false
		h.d.syncKmemButton()
		if !h.d.kmemSwitch.Visible() {
			t.Fatal("installed kmem, Claude's memory in use: the offer shows")
		}
		test.Tap(findButton(h.d.kmemSwitch, "No thanks"))
		if h.d.kmemSwitch.Visible() || !p.Bool(prefKmemSwitchOffered) || p.String(memoryModeKey) != memoryModeClaude {
			t.Fatal("No thanks: hidden for good, memory unchanged")
		}
		h.d.syncKmemButton()
		if h.d.kmemSwitch.Visible() {
			t.Fatal("offered once")
		}
		p.SetBool(prefKmemSwitchOffered, false)
		h.d.syncKmemButton()
		test.Tap(findButton(h.d.kmemSwitch, "Switch to kmem..."))
		if h.d.kmemSwitchUI == nil || !h.d.kmemSwitch.Visible() || p.String(memoryModeKey) != memoryModeClaude {
			t.Fatal("Switch to kmem... opens the switch's preview; nothing changes before Switch (TestKmemSwitchFlow)")
		}
	})
	// the preview's background check finishes before the test ends
	h.eventually("checked", func() bool {
		return h.d.kmemSwitchUI.buttons.Objects[0].(*widget.Button).Text != "Cancel" || len(h.d.kmemSwitchUI.buttons.Objects) > 1
	})
}

// TestKmemSwitchOfferDefault: kmem installed and no mode chosen, so sessions
// use kmem by default: the header offers the switch once (import Claude's
// notes, add hooks); not after "No thanks", not after a finished switch, and
// not when a mode was chosen.
func TestKmemSwitchOfferDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	h := newHarness(t)
	h.ui(func() {
		p := h.d.app.Preferences()
		h.d.syncKmemButton()
		if h.d.kmemSwitch.Visible() {
			t.Fatal("no kmem: no offer")
		}
		h.d.kmemOff = false
		h.d.syncKmemButton()
		if !h.d.kmemMemory() || !h.d.kmemSwitch.Visible() {
			t.Fatal("kmem by default, never switched: the offer shows")
		}
		test.Tap(findButton(h.d.kmemSwitch, "No thanks"))
		if h.d.kmemSwitch.Visible() || !p.Bool(prefKmemSwitchOffered) || p.String(memoryModeKey) != "" || !h.d.kmemMemory() {
			t.Fatal("No thanks: answered for good, kmem stays the default, nothing stored")
		}
		h.d.syncKmemButton()
		if h.d.kmemSwitch.Visible() {
			t.Fatal("offered once")
		}

		// a finished switch (it stores the mode and its backup) answers it
		p.SetBool(prefKmemSwitchOffered, false)
		p.SetString(prefKmemSwitchBackup, "/placeholder/backup")
		h.d.syncKmemButton()
		if h.d.kmemSwitch.Visible() {
			t.Fatal("after a switch: no offer")
		}
		p.RemoveValue(prefKmemSwitchBackup)
		p.SetString(memoryModeKey, memoryModeKmem)
		h.d.syncKmemButton()
		if h.d.kmemSwitch.Visible() {
			t.Fatal("kmem chosen explicitly: no offer")
		}
		p.SetString(memoryModeKey, "")
		h.d.syncKmemButton()
		test.Tap(findButton(h.d.kmemSwitch, "Switch to kmem..."))
		if h.d.kmemSwitchUI == nil || p.String(memoryModeKey) != "" {
			t.Fatal("Switch to kmem... opens the preview; nothing changes before Switch")
		}
	})
	h.eventually("checked", func() bool {
		return h.d.kmemSwitchUI.buttons.Objects[0].(*widget.Button).Text != "Cancel" || len(h.d.kmemSwitchUI.buttons.Objects) > 1
	})
}

// TestClaudeConfigDirPaths: the user CLAUDE.md, the auto-memory folder and
// the user agents folder follow CLAUDE_CONFIG_DIR like Claude Code.
func TestClaudeConfigDirPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	cwd := "/placeholder/proj"
	slug := filepath.Join("projects", "-placeholder-proj", "memory", "MEMORY.md")
	def := filepath.Join(home, ".claude")
	if claudeConfigDir(home) != def || claudeSettingsFile(home) != filepath.Join(def, "settings.json") || userAgentsDir() != filepath.Join(def, "agents") {
		t.Fatalf("default ~/.claude: %s", claudeConfigDir(home))
	}
	if f := memoryFiles(cwd); f[0] != filepath.Join(def, "CLAUDE.md") || f[4] != filepath.Join(def, slug) {
		t.Fatalf("memory files: %v", f)
	}
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	if claudeConfigDir(home) != cfg || claudeSettingsFile(home) != filepath.Join(cfg, "settings.json") {
		t.Fatalf("CLAUDE_CONFIG_DIR: %s", claudeConfigDir(home))
	}
	f := memoryFiles(cwd)
	if f[0] != filepath.Join(cfg, "CLAUDE.md") || f[4] != filepath.Join(cfg, slug) || f[2] != filepath.Join(cwd, ".claude", "CLAUDE.md") {
		t.Fatalf("memory files with CLAUDE_CONFIG_DIR: %v", f)
	}
	if dirs := agentDirs(cwd); dirs[0][1] != filepath.Join(cfg, "agents") || dirs[1][1] != filepath.Join(cwd, ".claude", "agents") {
		t.Fatalf("agent dirs: %v", dirs)
	}
	os.MkdirAll(filepath.Join(cfg, "agents"), 0o755)
	os.WriteFile(filepath.Join(cfg, "agents", "placeholder-agent.md"), []byte("---\nname: placeholder-agent\ndescription: placeholder\n---\nbody\n"), 0o600)
	os.MkdirAll(filepath.Join(home, ".claude", "agents"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", "agents", "stale-agent.md"), []byte("---\nname: stale-agent\n---\n"), 0o600)
	var names []string
	for _, a := range agentFiles(cwd) {
		names = append(names, a.name)
	}
	if len(names) != 1 || names[0] != "placeholder-agent" {
		t.Fatalf("user agents come from CLAUDE_CONFIG_DIR only: %v", names)
	}
}
