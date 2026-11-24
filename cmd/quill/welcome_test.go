package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestWelcomeChoices(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KMEM_CONFIG", "")
	h := newHarness(t)
	rules := filepath.Join(home, ".local", "share", "kmem", "RULES.md")
	h.ui(func() {
		p := h.d.app.Preferences()
		done := h.d.applyWelcome(welcomeChoices{Local: true, LocalModel: "ollama:qwen3:8b", Lang: "de", Rules: true})
		if p.String("newModel") != "ollama:qwen3:8b" || p.String("sttLang") != "de" || p.Bool(prefSTTOff) || !p.Bool(prefWelcomeSeen) {
			t.Fatalf("local, German, rules: newModel %q sttLang %q off %v seen %v", p.String("newModel"), p.String("sttLang"), p.Bool(prefSTTOff), p.Bool(prefWelcomeSeen))
		}
		if b, err := os.ReadFile(rules); err != nil || string(b) != defaultKmemRules || !strings.Contains(strings.Join(done, " "), "Installed Quill's default kmem rules") {
			t.Fatalf("beginner rules installed: %v %v", err, done)
		}
		if !h.d.stt.btn.Visible() {
			t.Fatal("the mic shows while speech to text is on")
		}
		os.WriteFile(rules, []byte("1. mine\n"), 0o644)
		h.d.applyWelcome(welcomeChoices{Lang: "", Rules: true})
		if p.String("newModel") != "default" || !p.Bool(prefSTTOff) || h.d.stt.btn.Visible() {
			t.Fatal("online and speech to text off hides the mic")
		}
		if b, _ := os.ReadFile(rules); string(b) != "1. mine\n" {
			t.Fatal("an existing rules file stays as it is")
		}
		h.d.stt.setLang("fr")
		if p.Bool(prefSTTOff) || !h.d.stt.btn.Visible() {
			t.Fatal("picking a language turns speech to text on again")
		}
		done = h.d.applyWelcome(welcomeChoices{Lang: "en", PlanFirst: true, KmemMemory: true})
		// kmem not in use: the mode changes only through "Switch to kmem".
		if !h.d.planFirst || !p.Bool("planFirst") || p.String(memoryModeKey) != memoryModeClaude || !strings.Contains(strings.Join(done, " "), "Switch to kmem") {
			t.Fatalf("ask first set, kmem memory left to the switch: %q", done)
		}
		h.d.applyWelcome(welcomeChoices{Lang: "en", PlanFirst: false, KmemMemory: false})
		if h.d.planFirst || p.Bool("planFirst") || p.String(memoryModeKey) != memoryModeClaude {
			t.Fatal("let Claude work and auto memory are set")
		}
	})
}

// Skip only marks the Welcome as seen; every setting stays as it was.
func TestWelcomeSkipChangesNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KMEM_CONFIG", "")
	h := newHarness(t)
	h.ui(func() {
		p := h.d.app.Preferences()
		p.SetBool(prefWelcomeSeen, false)
		before := map[string]string{"newModel": p.String("newModel"), "sttLang": p.String("sttLang"), memoryModeKey: p.String(memoryModeKey)}
		planBefore, offBefore := h.d.planFirst, p.Bool(prefSTTOff)
		h.d.showWelcome(nil)
		var skip *widget.Button
		deepWalk(h.w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
			if b, ok := o.(*widget.Button); ok && b.Text == "Skip" {
				skip = b
			}
		})
		if skip == nil {
			t.Fatal("the Welcome has a Skip button")
		}
		test.Tap(skip)
		for k, v := range before {
			if p.String(k) != v {
				t.Errorf("Skip changed %s: %q -> %q", k, v, p.String(k))
			}
		}
		if h.d.planFirst != planBefore || p.Bool(prefSTTOff) != offBefore || !p.Bool(prefWelcomeSeen) {
			t.Fatal("Skip keeps plan first and speech to text, and marks the Welcome seen")
		}
		if _, err := os.Stat(filepath.Join(homeDir(), ".local", "share", "kmem", "RULES.md")); err == nil {
			t.Fatal("Skip installs no rules")
		}
	})
}

func TestWelcomeOnlyOnNewInstalls(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		p := h.d.app.Preferences()
		p.SetString("tabs", "")
		p.SetBool("setupSeen", false)
		p.SetBool(prefWelcomeSeen, false)
		if !h.d.freshInstall() {
			t.Fatal("no tabs and no Setup yet is a new install")
		}
		p.SetString("tabs", `["x"]`)
		if h.d.freshInstall() {
			t.Fatal("saved tabs mean Quill was used here")
		}
		h.d.setupAtStart()
		if !p.Bool(prefWelcomeSeen) {
			t.Fatal("an existing install never gets the Welcome")
		}
	})
	if suggestLocal(16) != "ollama:qwen3:8b" || suggestLocal(32) != "ollama:gpt-oss:20b" {
		t.Fatal("the local suggestion fits the memory")
	}
	t.Setenv("LANG", "de_DE.UTF-8")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	if systemLang() != "de" {
		t.Fatal("speech to text starts from the system language")
	}
}

func TestWelcomeShot(t *testing.T) {
	if os.Getenv("DESK_SHOTS") == "" {
		t.Skip("set DESK_SHOTS=1")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KMEM_CONFIG", "")
	dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
	os.MkdirAll(dir, 0o755)
	h := newHarness(t)
	h.ui(func() { h.w.Resize(fyne.NewSize(1280, 860)); h.d.showWelcome(nil) })
	h.ui(func() { shot(t, h.w, dir, "welcome.png") })
}
