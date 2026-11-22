package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	"github.com/katachinosekai/quill/internal/transcript"
)

// rowCaptionShown is the caption drawn right of the title in the sidebar
// row of session id ("" when the row isn't rendered or has no caption).
func rowCaptionShown(d *desk, id string) (string, bool) {
	for _, it := range d.sideItems {
		r, ok := it.row()
		if !ok || !it.Visible() || r.id != id {
			continue
		}
		head := it.content.(*fyne.Container).Objects[2].(*fyne.Container).Objects[0].(*fyne.Container).Objects[0].(*fyne.Container)
		return head.Objects[1].(*fyne.Container).Objects[0].(*canvas.Text).Text, true
	}
	return "", false
}

func TestPortraitRowTokens(t *testing.T) {
	h := newHarness(t)
	var withCtx, tokOnly, none *deskSession
	setUsage := func(s *deskSession, in int64) {
		info := h.d.infos[s.id]
		info.ID = s.id
		info.Usage = transcript.Tokens{In: in}
		h.d.infos[s.id] = info
	}
	h.ui(func() {
		h.d.setLayoutMode(layoutVertical)
		withCtx = h.d.createSession(h.proj, "default", "default")
		h.d.renameSession(withCtx.id, "Release notes")
		tokOnly = h.d.createSession(h.proj, "default", "default")
		h.d.renameSession(tokOnly.id, "Fix the tests")
		none = h.d.createSession(h.proj, "default", "default")
		h.d.renameSession(none.id, "Empty session")
		setUsage(withCtx, 12300)
		withCtx.conv.Stats.ContextUsed, withCtx.conv.Stats.ContextMax = 84_000, 200_000
		setUsage(tokOnly, 5000)
		h.d.refreshSidebar()
	})
	want := func(what string, s *deskSession, cap string) {
		t.Helper()
		var got string
		var ok bool
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			h.ui(func() { got, ok = rowCaptionShown(h.d, s.id) })
			if ok && got == cap {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%s: caption %q (rendered %v), want %q", what, got, ok, cap)
	}
	want("tokens and context", withCtx, "12.3k tok · ctx 42%")
	want("tokens only while context is unknown", tokOnly, "5.0k tok")
	want("nothing without usage", none, "")

	if os.Getenv("DESK_SHOTS") != "" {
		dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
		os.MkdirAll(dir, 0o755)
		h.ui(func() { shot(t, h.w, dir, "portrait-row-tokens.png") })
	}

	h.ui(func() {
		setUsage(tokOnly, 1_500_000)
		tokOnly.conv.Stats.ContextUsed, tokOnly.conv.Stats.ContextMax = 50_000, 200_000
		h.d.usage.update()
	})
	want("live update from usage", tokOnly, "1.5M tok · ctx 25%")

	h.ui(func() { h.d.setLayoutMode(layoutHorizontal) })
	want("landscape: no caption", withCtx, "")
	want("landscape: no caption", tokOnly, "")
}
