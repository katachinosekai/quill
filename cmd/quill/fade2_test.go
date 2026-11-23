package main

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

// streamHarness opens a streaming reply with the given text and layout, to
// be drawn frame by frame by hand.
func streamHarness(t *testing.T, center bool, full, speed string) (*harness, *chat.Item, *itemView) {
	h := newHarness(t)
	h.ui(func() {
		h.d.center = center
		h.d.app.Preferences().SetBool(prefAnimateText, false)
		h.d.open(editsID)
	})
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	var it *chat.Item
	var iv *itemView
	h.ui(func() {
		s := h.d.sessions[editsID]
		it = s.conv.AddNotice("")
		it.Kind, it.Streaming, it.Text = chat.Assistant, true, full
		h.d.chat.show(s)
		h.d.chat.update([]*chat.Item{it})
		iv = h.d.chat.views[it.Seq]
		iv.st.animated, iv.st.pos, iv.st.speed = true, 0, speed
	})
	return h, it, iv
}

// objectsOf lists the canvas objects of a tree.
func objectsOf(o fyne.CanvasObject) []fyne.CanvasObject {
	var l []fyne.CanvasObject
	walk(o, func(x fyne.CanvasObject) { l = append(l, x) })
	return l
}

// The live blocks of centred and lettered-list text are the same objects
// from frame to frame (updated in place), like the left-aligned paragraph.
func TestTextFadeReusesBlocks(t *testing.T) {
	for name, tc := range map[string]struct {
		center bool
		text   string
	}{
		"centred":  {true, "A centred paragraph with **bold** words, `code` and more words that fade in one by one."},
		"lettered": {false, "Pick one:\na. the first choice is **this** one\nb. the second choice is that one\nc. the third and last choice"},
		"both":     {true, "Pick one:\na. the first choice is **this** one\nb. the second choice is that one\nc. the third and last choice"},
	} {
		t.Run(name, func(t *testing.T) {
			h, it, iv := streamHarness(t, tc.center, tc.text, "fast")
			h.ui(func() {
				v, st := h.d.chat, iv.st
				now := time.Unix(5000, 0)
				streamNow = func() time.Time { return now }
				defer func() { streamNow = time.Now }()
				var first []fyne.CanvasObject
				for st.pos < len(it.Text) {
					st.advance(it.Text)
					v.drawStream(st, iv)
					now = now.Add(streamTick)
					if first == nil && strings.Contains(it.Text[:st.pos], "choice is that") || first == nil && tc.text[:1] == "A" && st.pos > 40 {
						first = objectsOf(iv.holder)
						if st.liveTree == nil {
							t.Fatal("the live blocks are kept")
						}
						continue
					}
					if first != nil {
						now := objectsOf(iv.holder)
						if len(now) != len(first) {
							t.Fatalf("pos %d: %d objects, was %d", st.pos, len(now), len(first))
						}
						for i := range now {
							if now[i] != first[i] {
								t.Fatalf("pos %d: object %d (%T) was replaced", st.pos, i, now[i])
							}
						}
					}
				}
				if first == nil {
					t.Fatal("never reached a stable layout")
				}
			})
		})
	}
}

// A question dialog and a permission card fade in once, when first shown,
// and clicks wait for the guard.
func TestQuestionAndPermissionFadeOnce(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.chat.show(s)
		v := h.d.chat
		q := &chat.Item{Seq: 971, Kind: chat.Permission, ToolName: "AskUserQuestion", PermState: chat.PermPending,
			Input: []byte(`{"questions":[{"question":"Which?","options":[{"label":"A"},{"label":"B"}]}]}`)}
		p := &chat.Item{Seq: 972, Kind: chat.Permission, ToolName: "Bash", Summary: "ls", PermState: chat.PermPending}
		done := &chat.Item{Seq: 973, Kind: chat.Permission, ToolName: "Bash", Summary: "ls", PermState: chat.PermAllowed}
		for name, it := range map[string]*chat.Item{"question": q, "permission": p} {
			base := v.fadesStarted
			o := v.renderPermission(it)
			if v.fadesStarted != base+1 || veilsShown(o) != 1 || len(v.fadePend) != 0 {
				t.Fatalf("%s: first draw: %d fades, %d veils", name, v.fadesStarted-base, veilsShown(o))
			}
			// A click inside the guard is ignored.
			for _, b := range swapButtons(o) {
				b.OnTapped()
			}
			if it.PermState != chat.PermPending {
				t.Fatalf("%s: a click during the fade landed", name)
			}
			// A redraw during the fade continues it; it is not a new one.
			o = v.renderPermission(it)
			if v.fadesStarted != base+1 {
				t.Fatalf("%s: a redraw started another fade", name)
			}
			// After the fade, redraws are plain.
			v.fadeStart[it.Seq] = time.Now().Add(-time.Second)
			delete(v.qcards, it)
			o = v.renderPermission(it)
			if v.fadesStarted != base+1 || veilsShown(o) != 0 {
				t.Fatalf("%s: after the fade: %d fades, %d veils", name, v.fadesStarted-base, veilsShown(o))
			}
		}
		base := v.fadesStarted
		if o := v.renderPermission(done); v.fadesStarted != base || veilsShown(o) != 0 {
			t.Fatal("a card that is already answered does not fade")
		}
		_ = protocol.Allow
	})
}

// For a reply with every kind of marker, streamed at Slow, no frame shows a
// raw marker, and the finished reply looks like before.
func TestStreamShowsNoRawMarkers(t *testing.T) {
	full := "## The heading\n\nSome **bold words** and *italic ones* with `inline code` and a [link text](https://example.com/page) here.\n\n" +
		"- first item\n- second **item**\n\n1. numbered one\n2. numbered two\n\n```go\nfmt.Println(\"hi\")\nx := 2 * 3\n```\n\nThe end."
	for _, center := range []bool{false, true} {
		h, it, iv := streamHarness(t, center, full, "slow")
		h.ui(func() {
			v, st := h.d.chat, iv.st
			now := time.Unix(6000, 0)
			streamNow = func() time.Time { return now }
			defer func() { streamNow = time.Now }()
			frames, last, still := 0, -1, 0
			for st.pos < len(it.Text) {
				st.advance(it.Text)
				v.drawStream(st, iv)
				now = now.Add(streamTick)
				frames++
				if st.pos == last {
					still++
				}
				last = st.pos
				shown := visibleText(iv.holder)
				for _, bad := range []string{"**", "`", "#", "](", "[", "http"} {
					if strings.Contains(shown, bad) {
						t.Fatalf("center=%v pos %d: raw %q in %q", center, st.pos, bad, shown)
					}
				}
				for _, l := range strings.Split(shown, "\n") {
					l = strings.TrimSpace(l)
					if strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ") || strings.HasPrefix(l, "1.") && !strings.Contains(l, "numbered") {
						t.Fatalf("center=%v pos %d: raw list marker in %q", center, st.pos, l)
					}
				}
			}
			// Slow shows a quarter of a character a frame at least; markers
			// take no frames of their own.
			if frames > 4*len(it.Text) {
				t.Fatalf("%d frames for %d bytes (%d still)", frames, len(it.Text), still)
			}
			it.Streaming = false
			for i := 0; i < 20 && st.fadeActive(); i++ {
				now = now.Add(streamTick)
				v.drawStream(st, iv)
			}
			v.refreshItem(it, iv)
			want := visibleText(v.markdownBlocks(it.Text))
			if got := visibleText(iv.holder); strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(want), " ") {
				t.Fatalf("center=%v: finished reply %q, want %q", center, got, want)
			}
		})
	}
}

// streamMarkdown cuts the markers at the end and closes what is open.
func TestStreamMarkdownClosesOpenMarkup(t *testing.T) {
	for in, want := range map[string]string{
		"Hello **bo":              "Hello **bo**",
		"Hello **":                "Hello ",
		"Hello **bold**":          "Hello **bold**",
		"a *it":                   "a *it*",
		"a 2 * 3":                 "a 2 * 3",
		"use `co":                 "use `co`",
		"see [lin":                "see [lin](#)",
		"see [link](http://e":     "see [link](#)",
		"see [link](http://e) go": "see [link](http://e) go",
		"## ":                     "",
		"## Ti":                   "## Ti",
		"- ":                      "",
		"- it":                    "- it",
		"1. ":                     "",
		"12":                      "",
		"```go\n":                 "",
		"```go\nx := 1":           "```go\nx := 1\n```",
		"```go\nx := 1\n``":       "```go\nx := 1\n```",
		"**a *b* c**":             "**a *b* c**",
	} {
		if got := streamMarkdown(in); got != want {
			t.Errorf("streamMarkdown(%q) = %q, want %q", in, got, want)
		}
	}
	_ = widget.NewLabel
}
