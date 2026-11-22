package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

func TestPatternLayout(t *testing.T) {
	at := time.Date(2026, 11, 22, 14, 3, 9, 0, time.Local)
	for p, want := range map[string]string{
		"HH:mm":               "14:03",
		"h:mm a":              "2:03 PM",
		"ddd d MMM, HH:mm":    "Sun 22 Nov, 14:03",
		"yyyy-MM-dd HH:mm:ss": "2026-11-22 14:03:09",
	} {
		l, ok := patternLayout(p)
		if !ok || at.Format(l) != want {
			t.Errorf("%q: %q (ok %v), want %q", p, at.Format(l), ok, want)
		}
	}
	if _, ok := patternLayout("!!"); ok {
		t.Error("a pattern without tokens is refused")
	}
}

func TestMessageTimes(t *testing.T) {
	// Transcript items keep their time; live ones get the arrival time.
	c := chat.New()
	raw := []byte(`{"type":"user","timestamp":"2026-11-20T09:15:00.000Z","message":{"role":"user","content":"hi"}}`)
	ev, err := protocol.ParseLine(raw)
	if err != nil {
		t.Fatal(err)
	}
	items := c.Apply(ev)
	if len(items) == 0 || !items[0].Time.Equal(time.Date(2026, 11, 20, 9, 15, 0, 0, time.UTC)) {
		t.Fatalf("transcript time kept: %v", items)
	}
	if live := c.AddUser("now"); time.Since(live.Time) > time.Minute {
		t.Fatal("a live message gets its arrival time")
	}

	h := newHarness(t)
	h.ui(func() {
		p := h.d.app.Preferences()
		p.SetString(prefTimeFormat, "HH:mm")
		at := time.Date(2026, 11, 22, 14, 3, 0, 0, time.Local)
		if got := h.d.msgTime(at); got != "14:03" {
			t.Fatalf("custom pattern: %q", got)
		}
		p.SetString(prefTimeFormat, "")
		if got := h.d.msgTime(time.Now()); !strings.Contains(got, ":") {
			t.Fatalf("system format: %q", got)
		}
		h.d.open(editsID)
	})
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	var timed int
	h.ui(func() {
		s := h.d.sessions[editsID]
		it := s.conv.AddUser("what time is it")
		h.d.chat.update([]*chat.Item{it})
		want := h.d.msgTime(it.Time)
		if strings.Contains(visibleLabels(h.d.chat.views[it.Seq].holder), want) {
			timed++
		}
		h.d.app.Preferences().SetBool(prefShowTimes, false)
		h.d.redrawChats()
		it2 := s.conv.AddUser("and now")
		h.d.chat.update([]*chat.Item{it2})
		if strings.Contains(visibleLabels(h.d.chat.views[it2.Seq].holder), h.d.msgTime(it2.Time)) {
			t.Fatal("times hidden when switched off")
		}
		h.d.app.Preferences().SetBool(prefShowTimes, true)
		h.d.redrawChats()
	})
	if timed != 1 {
		t.Fatal("a message shows its time under it")
	}
	if os.Getenv("DESK_SHOTS") != "" {
		dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
		os.MkdirAll(dir, 0o755)
		h.ui(func() { h.w.Resize(fyne.NewSize(1280, 860)); h.d.chat.scrollBottom() })
		time.Sleep(200 * time.Millisecond)
		h.ui(func() { shot(t, h.w, dir, "message-times.png") })
		h.ui(func() {
			dlg := dialog.NewCustom("Settings (excerpt)", "Close", container2(h.d), h.w)
			dlg.Resize(fyne.NewSize(820, 560))
			dlg.Show()
		})
		h.ui(func() { shot(t, h.w, dir, "settings-times-typing.png") })
	}
}

func container2(d *desk) fyne.CanvasObject {
	return widget.NewForm(
		widget.NewFormItem("New sessions", d.newSessionModelSetting()),
		widget.NewFormItem("Message times", d.timeSettings()),
		widget.NewFormItem("Typing", d.autocorrectSettings()),
	)
}

// visibleLabels: the text of the labels and canvas texts in a tree.
func visibleLabels(o fyne.CanvasObject) string {
	var b strings.Builder
	deepWalk(o, func(x fyne.CanvasObject) {
		switch w := x.(type) {
		case *widget.Label:
			b.WriteString(w.Text + "\n")
		case *canvas.Text:
			b.WriteString(w.Text + "\n")
		}
	})
	return b.String()
}
