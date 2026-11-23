package main

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"

	"github.com/katachinosekai/quill/internal/chat"
)

type streamFrame struct {
	at      time.Duration
	shown   int     // characters of visible text
	height  float32 // the message's height
	covered bool    // an opaque cover hides text (the old fade)
}

// coveredText: a cover in the window's background colour over the text (what
// the old fade-in drew); inline code backgrounds have their own colour.
func coveredText(o fyne.CanvasObject) bool {
	br, bg, bb, _ := theme.Color(theme.ColorNameBackground).RGBA()
	hit := false
	deepWalk(o, func(x fyne.CanvasObject) {
		if r, ok := x.(*canvas.Rectangle); ok && x.Visible() {
			if c, ok := r.FillColor.(color.NRGBA); ok && c.A > 40 && uint32(c.R)<<8|uint32(c.R) == br && uint32(c.G)<<8|uint32(c.G) == bg && uint32(c.B)<<8|uint32(c.B) == bb {
				hit = true
			}
		}
	})
	return hit
}

// Streams a demo reply in realistic bursts and records frames, then checks
// the things that looked jarring (user 2026-11-22): text vanishing again,
// the message shrinking, an uneven pace, and a jump when the reply ends.
func TestStreamingIsSmooth(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.app.Preferences().SetBool(prefAnimateText, true)
		h.d.app.Preferences().SetString(prefAnimateSpeed, "fast") // the timing must not depend on the user-facing default
		h.d.app.Preferences().SetBool(prefShowTimes, false)
		h.d.open(editsID)
	})
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	full := strings.Repeat("The first paragraph has **bold** words, `code` and a list of reasons to stream smoothly. ", 3) +
		"\n\n- one item\n- another item\n\n" +
		strings.Repeat("The second paragraph keeps growing while the reply arrives in bursts from the model. ", 4) +
		"\n\nAnd a short last one."
	var it *chat.Item
	h.ui(func() {
		s := h.d.sessions[editsID]
		it = s.conv.AddNotice("")
		it.Kind, it.Streaming = chat.Assistant, true
		h.d.chat.show(s)
	})
	var frames []streamFrame
	start := time.Now()
	sample := func() {
		h.ui(func() {
			iv := h.d.chat.views[it.Seq]
			if iv == nil {
				return
			}
			frames = append(frames, streamFrame{time.Since(start), len([]rune(strings.Join(strings.Fields(visibleText(iv.holder)), " "))), iv.holder.MinSize().Height, coveredText(iv.holder)})
		})
	}
	done := make(chan struct{})
	go func() { // the model: bursts of ~40 characters every 60 ms
		for n := 40; ; n += 40 {
			m := min(n, len(full))
			h.ui(func() { it.Text = full[:m]; h.d.chat.update([]*chat.Item{it}) })
			if m == len(full) {
				close(done)
				return
			}
			time.Sleep(60 * time.Millisecond)
		}
	}()
	for {
		sample()
		select {
		case <-done:
			goto streamed
		default:
		}
		time.Sleep(16 * time.Millisecond)
	}
streamed:
	// Let the reveal catch up, then end the reply.
	deadline := time.Now().Add(5 * time.Second)
	var last streamFrame
	for time.Now().Before(deadline) {
		sample()
		last = frames[len(frames)-1]
		if strings.Contains(visibleTextOf(h, it), "short last one") {
			break
		}
		time.Sleep(16 * time.Millisecond)
	}
	sample()
	last = frames[len(frames)-1]
	h.ui(func() { it.Streaming = false; h.d.chat.update([]*chat.Item{it}) })
	time.Sleep(300 * time.Millisecond)
	var finalH float32
	var finalText string
	h.ui(func() {
		iv := h.d.chat.views[it.Seq]
		finalH = iv.holder.MinSize().Height
		finalText = strings.Join(strings.Fields(visibleText(iv.holder)), " ")
	})

	var shrinks, blinks, gaps int
	maxStep := 0
	for i := 1; i < len(frames); i++ {
		a, b := frames[i-1], frames[i]
		if b.shown < a.shown {
			shrinks++
		}
		if b.covered {
			blinks++
		}
		if b.height < a.height-0.5 {
			shrinks++
		}
		if d := b.shown - a.shown; d > maxStep {
			maxStep = d
		}
		if b.shown == a.shown && b.at-a.at > 0 && b.shown < len([]rune(full))/2 && b.at < 600*time.Millisecond {
			gaps++
		}
	}
	t.Logf("frames %d, text shrinks %d, covered frames %d, largest step %d chars, last streaming height %.0f, final height %.0f",
		len(frames), shrinks, blinks, maxStep, last.height, finalH)
	if blinks > 0 {
		t.Errorf("text was covered (blinked) in %d frames", blinks)
	}
	if shrinks > 0 {
		t.Errorf("visible text or height went back %d times", shrinks)
	}
	if maxStep > 40 {
		t.Errorf("text jumped by %d characters in one frame (not a steady reveal)", maxStep)
	}
	if d := finalH - last.height; d < -0.5 || d > 0.5 {
		t.Errorf("the message jumped when the reply ended: %.0f -> %.0f", last.height, finalH)
	}
	if !strings.Contains(finalText, "short last one") || strings.Contains(finalText, "**") {
		t.Errorf("final text: %q", finalText)
	}
	if os.Getenv("DESK_SHOTS") != "" {
		dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
		os.MkdirAll(dir, 0o755)
		h.ui(func() { shot(t, h.w, dir, "stream-final.png") })
	}
}

func visibleTextOf(h *harness, it *chat.Item) string {
	var s string
	h.ui(func() {
		if iv := h.d.chat.views[it.Seq]; iv != nil {
			s = strings.Join(strings.Fields(visibleText(iv.holder)), " ")
		}
	})
	return s
}
