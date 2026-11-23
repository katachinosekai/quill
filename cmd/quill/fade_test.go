package main

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// The controls of a reply fade in once, when they first appear: not on a
// re-render, a pick, or when answered; and no click lands before the guard
// (longer than the fade) is over.
func TestControlsFadeInOnce(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		v := h.d.chat
		msg := &chat.Item{Seq: 991, Kind: chat.Assistant, Text: choiceMsg}
		s.conv.Items = append(s.conv.Items, msg)
		base := v.fadesStarted
		v.show(s)
		o, _ := v.renderChoiceReply(msg)
		if v.fadesStarted != base+1 {
			t.Fatalf("first appearance starts one fade: %d -> %d", base, v.fadesStarted)
		}
		if len(v.fadePend) != 0 {
			t.Fatal("veils wait nowhere once the controls are noted")
		}
		if controlFade >= clickGuard {
			t.Fatal("the click guard must outlast the fade")
		}
		// Clicks during the fade (the guard) do nothing.
		bs := swapButtons(o)
		bs[0].OnTapped()
		if len(v.choicePicks[msg]) != 0 {
			t.Fatal("a click during the fade must be ignored")
		}
		// Re-render during the fade continues it, not a new one.
		o, _ = v.renderChoiceReply(msg)
		if v.fadesStarted != base+1 {
			t.Fatal("a re-render must not start another fade")
		}
		// After the fade: re-render, pick, re-render: still one.
		v.fadeStart[msg.Seq] = time.Now().Add(-time.Second)
		v.btnChanged = time.Time{}
		swapButtons(o)[0].OnTapped()
		if len(v.choicePicks[msg]) != 1 {
			t.Fatal("after the guard the pick lands")
		}
		o, _ = v.renderChoiceReply(msg)
		if v.fadesStarted != base+1 || veilsShown(o) != 0 {
			t.Fatalf("a pick re-renders without a fade: %d veils", veilsShown(o))
		}
		// Answered: greyed controls, no new fade.
		s.conv.AddUser("done")
		o, _ = v.renderChoiceReply(msg)
		if v.fadesStarted != base+1 || veilsShown(o) != 0 {
			t.Fatal("answered state must not fade")
		}
	})
}

// A reply that is already answered when first drawn (history) doesn't fade.
func TestAnsweredControlsDoNotFade(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		v := h.d.chat
		msg := &chat.Item{Seq: 992, Kind: chat.Assistant, Text: choiceMsg}
		s.conv.Items = append(s.conv.Items, msg)
		s.conv.AddUser("1. A\n2. Small")
		base := v.fadesStarted
		v.show(s)
		o, _ := v.renderChoiceReply(msg)
		if v.fadesStarted != base || veilsShown(o) != 0 {
			t.Fatalf("answered on first draw: %d fades, %d veils", v.fadesStarted-base, veilsShown(o))
		}
	})
}

// veilsShown counts the visible fade veils in a tree (stacks ending in a
// chat-background rectangle).
func veilsShown(o fyne.CanvasObject) int {
	n := 0
	walk(o, func(x fyne.CanvasObject) {
		if c, ok := x.(*fyne.Container); ok && len(c.Objects) == 2 {
			if r, ok := c.Objects[1].(*canvas.Rectangle); ok && r.Visible() && r.FillColor != nil {
				n++
			}
		}
	})
	return n
}

// The reveal is paced by revealRate alone: the fade log takes the same
// frames at every speed, and what it logs adds up to what was revealed.
func TestTextFadeLeavesRevealTiming(t *testing.T) {
	text := strings.Repeat("Fade é in smoothly, character by character. ", 20)
	for _, speed := range []string{"slow", "normal", "fast"} {
		st := &streamView{animated: true, speed: speed}
		ref := &streamView{animated: false, speed: speed}
		start := time.Unix(1000, 0)
		now := start
		streamNow = func() time.Time { return now }
		defer func() { streamNow = time.Now }()
		frames := 0
		for st.pos < len(text) {
			st.advance(text)
			ref.advance(text)
			if st.pos != ref.pos || st.carry != ref.carry {
				t.Fatalf("%s frame %d: fade changed the reveal (%d vs %d)", speed, frames, st.pos, ref.pos)
			}
			now = now.Add(streamTick)
			if _, total := st.fadeCounts(now); total > 0 && len(st.reveals) > int(textFade/streamTick)+1 {
				t.Fatalf("%s: reveal log grew to %d", speed, len(st.reveals))
			}
			frames++
		}
		if frames > len(text) {
			t.Fatalf("%s: %d frames", speed, frames)
		}
		if !utf8.ValidString(text[:st.pos]) {
			t.Fatalf("%s: cut a character", speed)
		}
	}
}

func TestFadeCountsAndSegments(t *testing.T) {
	st := &streamView{}
	t0 := time.Unix(2000, 0)
	st.noteReveal(3, t0)
	st.noteReveal(2, t0.Add(100*time.Millisecond))
	c, total := st.fadeCounts(t0.Add(110 * time.Millisecond)) // first: age 110 -> step 2; second: age 10 -> step 0
	if total != 5 || c[0] != 2 || c[2] != 3 {
		t.Fatalf("counts %v total %d", c, total)
	}
	if _, total = st.fadeCounts(t0.Add(400 * time.Millisecond)); total != 0 || len(st.reveals) != 0 {
		t.Fatal("old batches are dropped")
	}

	rt := widget.NewRichText()
	rt.ParseMarkdown("Hello **bold** world")
	b := fadeBudget{[textFadeSteps]int{2, 0, 3, 0}} // last 2 faintest, the 3 before them step 2
	segs := fadeSegs(rt.Segments, &b)
	var all strings.Builder
	var last, mid *widget.TextSegment
	for _, s := range segs {
		ts := s.(*widget.TextSegment)
		all.WriteString(ts.Text)
		if ts.Style.ColorName == fadeNames[0] {
			last = ts
		}
		if ts.Style.ColorName == fadeNames[2] {
			mid = ts
		}
	}
	if all.String() != "Hello bold world" {
		t.Fatalf("text changed: %q", all.String())
	}
	if last == nil || last.Text != "ld" || mid == nil || mid.Text != "wor" {
		t.Fatalf("faded tails: %+v %+v", last, mid)
	}
}

// While the fade runs, the live paragraph is one RichText redrawn in place:
// no new canvas objects per frame, and the text is the revealed text.
func TestTextFadeReusesObjects(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.center = false
		h.d.app.Preferences().SetBool(prefAnimateText, false)
		h.d.open(editsID)
	})
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	full := "The live paragraph grows and fades in while it is being **revealed** a few characters at a time."
	var it *chat.Item
	h.ui(func() {
		s := h.d.sessions[editsID]
		it = s.conv.AddNotice("")
		it.Kind, it.Streaming, it.Text = chat.Assistant, true, full
		h.d.chat.show(s)
		h.d.chat.update([]*chat.Item{it})
	})
	h.ui(func() {
		v := h.d.chat
		iv := v.views[it.Seq]
		st := iv.st
		st.animated, st.pos, st.speed = true, 0, "fast" // draw frames by hand
		now := time.Unix(3000, 0)
		streamNow = func() time.Time { return now }
		defer func() { streamNow = time.Now }()
		count := func() int {
			n := 0
			walk(iv.holder, func(fyne.CanvasObject) { n++ })
			return n
		}
		var rt *widget.RichText
		objs, tree := 0, 0
		for i := 0; st.pos < len(full); i++ {
			st.advance(full)
			v.drawStream(st, iv)
			now = now.Add(streamTick)
			if rt == nil {
				rt = st.liveRT
				if rt == nil {
					t.Fatal("the live paragraph is drawn through one reused RichText")
				}
				objs, tree = len(st.live.Objects), count()
			}
			if st.liveRT != rt || len(st.live.Objects) != objs || count() != tree {
				t.Fatalf("frame %d: objects changed (rt same %v, %d objects, %d nodes vs %d)", i, st.liveRT == rt, len(st.live.Objects), count(), tree)
			}
			if got := plainText(rt); got != strings.ReplaceAll(full[:st.pos], "**", "") && !strings.HasPrefix(strings.ReplaceAll(full, "**", ""), got) {
				t.Fatalf("frame %d: shown %q", i, got)
			}
		}
		for i := 0; i < 20 && st.fadeActive(); i++ {
			now = now.Add(streamTick)
			v.drawStream(st, iv)
		}
		if st.fadeActive() || len(st.reveals) != 0 {
			t.Fatal("the fade ends")
		}
		for _, s := range rt.Segments {
			if ts, ok := s.(*widget.TextSegment); ok {
				for _, n := range fadeNames {
					if ts.Style.ColorName == n {
						t.Fatalf("faded colour left after the fade: %q", ts.Text)
					}
				}
			}
		}
		// Animation off or Instant: nothing is logged, nothing fades.
		off := &streamView{animated: false, speed: "normal"}
		off.advance(full)
		if len(off.reveals) != 0 {
			t.Fatal("no fade without animation")
		}
	})
}

func plainText(rt *widget.RichText) string {
	var b strings.Builder
	for _, s := range rt.Segments {
		b.WriteString(s.Textual())
	}
	return b.String()
}

// Centred text (the default layout) fades too, and shows the revealed text.
func TestTextFadeCentered(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.center = true
		h.d.app.Preferences().SetBool(prefAnimateText, false)
		h.d.open(editsID)
	})
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	full := "A centred paragraph with **bold** words that fades in."
	var it *chat.Item
	h.ui(func() {
		s := h.d.sessions[editsID]
		it = s.conv.AddNotice("")
		it.Kind, it.Streaming, it.Text = chat.Assistant, true, full
		h.d.chat.show(s)
		h.d.chat.update([]*chat.Item{it})
	})
	h.ui(func() {
		v := h.d.chat
		iv := v.views[it.Seq]
		st := iv.st
		st.animated, st.pos, st.speed = true, 0, "fast"
		now := time.Unix(4000, 0)
		streamNow = func() time.Time { return now }
		defer func() { streamNow = time.Now }()
		for st.pos < len(full) {
			st.advance(full)
			v.drawStream(st, iv)
			now = now.Add(streamTick)
			want := strings.ReplaceAll(full[:st.pos], "**", "")
			got := strings.ReplaceAll(strings.ReplaceAll(visibleText(iv.holder), "\n", ""), " ", "")
			if !strings.HasPrefix(strings.ReplaceAll(want, " ", ""), got) || len(got) < len(strings.ReplaceAll(want, " ", ""))-2 {
				t.Fatalf("pos %d: shown %q want about %q", st.pos, got, want)
			}
		}
	})
}
