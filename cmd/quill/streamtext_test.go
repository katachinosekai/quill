package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

func TestStreamCut(t *testing.T) {
	for text, want := range map[string]int{
		"hello":                      0,
		"one\n\ntwo":                 5,
		"one\n\ntwo\n\nthr":          10,
		"```\na\n\nb\n```\n\nafter":  14,
		"```\na\n\nb still in fence": 0,
		"para\n\n":                   0,
	} {
		if got := streamCut(text); got != want {
			t.Errorf("streamCut(%q) = %d, want %d", text, got, want)
		}
	}
}

// visibleText: the text drawn in a canvas tree (rich text segments).
func visibleText(o fyne.CanvasObject) string {
	var b strings.Builder
	deepWalk(o, func(x fyne.CanvasObject) {
		if rt, ok := x.(*widget.RichText); ok && x.Visible() {
			for _, s := range rt.Segments {
				b.WriteString(s.Textual())
				b.WriteString("\n")
			}
		}
	})
	return b.String()
}

// A streaming reply is formatted while it arrives: no raw ** or # on screen,
// redraws are rate-limited, and the finished message looks as before.
func TestStreamingReplyIsFormatted(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.app.Preferences().SetBool(prefAnimateText, false)
		h.d.open(editsID)
	})
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	full := "## Plan\n\nThis is **bold** and `code`.\n\n- one\n- two\n\nThe last paragraph is **still growing** here."
	var it *chat.Item
	h.ui(func() {
		s := h.d.sessions[editsID]
		it = s.conv.AddNotice("")
		it.Kind, it.Streaming = chat.Assistant, true
		h.d.chat.show(s)
	})
	// Feed it in small pieces, faster than the redraw limit.
	start := time.Now()
	for i := 1; i <= len(full); i += 3 {
		n := min(i, len(full))
		h.ui(func() {
			it.Text = full[:n]
			h.d.chat.update([]*chat.Item{it})
		})
		time.Sleep(2 * time.Millisecond)
	}
	h.ui(func() { it.Text = full; h.d.chat.update([]*chat.Item{it}) })
	var st *streamView
	h.eventually("live paragraph drawn", func() bool {
		st = h.d.chat.views[it.Seq].st
		return st != nil && strings.Contains(visibleText(h.d.chat.views[it.Seq].holder), "still growing")
	})
	h.ui(func() {
		shown := visibleText(h.d.chat.views[it.Seq].holder)
		if strings.Contains(shown, "**") || strings.Contains(shown, "## ") || strings.Contains(shown, "`") {
			t.Fatalf("raw markup on screen while streaming:\n%s", shown)
		}
		limit := int(time.Since(start)/streamTick) + len(full)/3 + 3
		if st.renders > limit {
			t.Fatalf("live paragraph redrawn %d times in %v (limit %d)", st.renders, time.Since(start), limit)
		}
		if len(st.box.Objects) < 4 {
			t.Fatalf("finished paragraphs are kept as blocks: %d objects", len(st.box.Objects))
		}
		it.Streaming = false
		h.d.chat.update([]*chat.Item{it})
		if h.d.chat.views[it.Seq].st != nil {
			t.Fatal("the finished reply is rendered whole, as before")
		}
		if got := visibleText(h.d.chat.views[it.Seq].holder); !strings.Contains(got, "still growing") || strings.Contains(got, "**") {
			t.Fatalf("finished text: %s", got)
		}
	})
}

func TestStreamingAnimationShot(t *testing.T) {
	if os.Getenv("DESK_SHOTS") == "" {
		t.Skip("set DESK_SHOTS=1")
	}
	dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
	os.MkdirAll(dir, 0o755)
	h := newHarness(t)
	h.ui(func() {
		h.d.app.Preferences().SetBool(prefAnimateText, true)
		h.d.open(editsID)
	})
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	var it *chat.Item
	h.ui(func() {
		s := h.d.sessions[editsID]
		it = s.conv.AddNotice("")
		it.Kind, it.Streaming = chat.Assistant, true
		it.Text = "## Streaming now\n\nThe first paragraph has **bold** text and `code`.\n\n- a list item\n- another one\n\nThe last paragraph is **still gro"
		h.d.chat.show(s)
		h.d.chat.update([]*chat.Item{it})
	})
	time.Sleep(60 * time.Millisecond) // mid-fade
	h.ui(func() { shot(t, h.w, dir, "stream-mid-fade.png") })
	time.Sleep(400 * time.Millisecond)
	h.ui(func() { shot(t, h.w, dir, "stream-settled.png") })
}
