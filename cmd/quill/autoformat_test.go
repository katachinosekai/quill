package main

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func typeInto(e *sendEntry, s string) {
	for _, r := range s {
		e.TypedRune(r)
	}
}

func TestAutoFormat(t *testing.T) {
	newBox := func(on bool) *sendEntry {
		e := newSendEntry(func(string) {})
		e.autoFormat = func() bool { return on }
		test.NewTempWindow(t, e)
		return e
	}
	for _, c := range []struct{ typed, want string }{
		{"hello there. next one", "Hello there. Next one"},
		{"see e.g. this", "See e.g. this"},
		{"use `git status` here", "Use `git status` here"},
		{"`foo bar` x", "`foo bar` x"},
		{"/clear now", "/clear now"},
		{"one -- two", "One — two"},
		{"run ls --all now", "Run ls --all now"},
		{"in v1.2. ok", "In v1.2. ok"},
	} {
		e := newBox(true)
		typeInto(e, c.typed)
		if e.Text != c.want {
			t.Errorf("typed %q: got %q, want %q", c.typed, e.Text, c.want)
		}
	}
	// Off: nothing changes.
	e := newBox(false)
	typeInto(e, "hello -- x")
	if e.Text != "hello -- x" {
		t.Errorf("off: %q", e.Text)
	}
	// Backspace right after a change undoes it.
	e = newBox(true)
	typeInto(e, "h")
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyBackspace})
	if e.Text != "h" {
		t.Errorf("undo capital: %q", e.Text)
	}
	e = newBox(true)
	typeInto(e, "A -- ")
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyBackspace})
	if e.Text != "A -- " {
		t.Errorf("undo dash: %q", e.Text)
	}
	// Shift+Enter continues a list; an empty item ends it.
	e = newBox(true)
	typeInto(e, "- first")
	e.shift = true
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	typeInto(e, "second")
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if e.Text != "- First\n- Second\n- " {
		t.Errorf("list: %q", e.Text)
	}
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if e.Text != "- First\n- Second\n" {
		t.Errorf("empty item ends the list: %q", e.Text)
	}
	e = newBox(true)
	typeInto(e, "1. a")
	e.shift = true
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if e.Text != "1. A\n2. " {
		t.Errorf("numbered list: %q", e.Text)
	}
}

// Every Welcome choice can be changed in Settings afterwards.
func TestSettingsCoverWelcome(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		p := h.d.app.Preferences()
		box := h.d.newSessionModelSetting()
		var sel *widget.Select
		deepWalk(box, func(o fyne.CanvasObject) {
			if s, ok := o.(*widget.Select); ok {
				sel = s
			}
		})
		if sel == nil || len(sel.Options) < 2 {
			t.Fatal("New sessions: online and local models")
		}
		sel.SetSelected(sel.Options[1])
		if !isLocal(p.String("newModel")) {
			t.Fatalf("picking a local model sets newModel: %q", p.String("newModel"))
		}
		sel.SetSelected("Online: Claude")
		if p.String("newModel") != "default" {
			t.Fatalf("online sets newModel back: %q", p.String("newModel"))
		}
		h.d.applyWelcome(welcomeChoices{Lang: "en", Typing: []string{welcomeAutoFormat}})
		if p.Bool("autocorrect") || !p.Bool(prefAutoFormat) {
			t.Fatal("the Welcome's typing help sets autocorrect and auto formatting")
		}
		h.d.applyWelcome(welcomeChoices{Lang: "en", Typing: []string{welcomeAutocorrect, welcomeAutoFormat}})
		if !h.d.autocorrectOn() || !h.d.autoFormatOn() {
			t.Fatal("both on")
		}
	})
}

// Settings → New sessions shows the live newModel each time Settings opens,
// and picking a model or backend for one session doesn't change it.
func TestNewSessionsSettingLive(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		p := h.d.app.Preferences()
		sel := h.d.newSessSel
		if sel == nil || sel.Selected != optNewOnline {
			t.Fatalf("starts Online: %+v", sel)
		}
		p.SetString("newModel", "ollama:qwen3:8b")
		h.d.showSettings()
		if sel.Selected != "Local: qwen3:8b" {
			t.Fatalf("Settings shows the live newModel on open: %q", sel.Selected)
		}
		h.d.closeSettings()
		p.SetString("newModel", "default")
		h.d.showSettings()
		h.d.closeSettings()
		if sel.Selected != optNewOnline || p.String("newModel") != "default" {
			t.Fatalf("back to Online, and showing it changes nothing: %q %q", sel.Selected, p.String("newModel"))
		}
		s := h.d.createSession(h.proj, "opus", "auto")
		h.d.open(s.id)
		h.d.modelSel.SetSelected("sonnet")
		if s.model != "sonnet" || p.String("newModel") != "default" {
			t.Fatalf("the session switches, New sessions stays: %q %q", s.model, p.String("newModel"))
		}
	})
}
