package main

import (
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"

	"github.com/katachinosekai/quill/internal/chat"
)

// Streaming replies are formatted while they arrive (user 2026-11-21): the
// paragraphs that are complete are rendered once and fade in; the growing
// last paragraph is rendered with its open markup closed for now
// (closeOpenMarkdown) and revealed smoothly. It is redrawn at most every
// streamEvery. When the reply ends the whole text is rendered as before, so
// the finished message looks exactly as it always did.

const (
	prefAnimateText = "animateText"
	streamEvery     = 100 * time.Millisecond
	streamFade      = 150 * time.Millisecond
	streamReveal    = 120 * time.Millisecond
)

type streamView struct {
	it       *chat.Item
	box      *fyne.Container // finished blocks, then live
	live     *fyne.Container // the growing last paragraph
	done     int             // bytes of it.Text rendered as finished blocks
	shown    string          // the live text currently drawn
	last     time.Time       // last live redraw
	pending  bool            // a throttled redraw is scheduled
	reveal   *fyne.Animation
	renders  int // live redraws (tests)
	animated bool
}

func (d *desk) animateText() bool {
	return d.app.Preferences().BoolWithFallback(prefAnimateText, true)
}

// streamCut: where the finished paragraphs end, at the last blank line
// outside a code fence (0 = nothing finished yet).
func streamCut(text string) int {
	cut, inFence, pos := 0, false, 0
	lines := strings.SplitAfter(text, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			inFence = !inFence
		}
		pos += len(l)
		// A blank line that has more text after it closes a paragraph.
		if !inFence && strings.TrimSpace(l) == "" && i > 0 && strings.TrimSpace(text[pos:]) != "" {
			cut = pos
		}
	}
	return cut
}

// refreshStream draws a streaming assistant reply (UI thread).
func (v *chatView) refreshStream(it *chat.Item, iv *itemView) {
	st := iv.st
	if st == nil || st.it != it {
		st = &streamView{it: it, live: container.NewStack(), animated: v.d.animateText()}
		st.box = container.NewVBox(st.live)
		iv.st = st
		iv.holder.Objects = []fyne.CanvasObject{spaced(it.Kind, msgText(st.box))}
		iv.holder.Refresh()
	}
	text := it.Text
	if cut := streamCut(text); cut > st.done {
		block := strings.TrimSpace(text[st.done:cut])
		st.done = cut
		st.shown = ""
		if block != "" {
			obj := v.markdown(block)
			if st.animated {
				obj = v.fadeIn(obj)
			}
			objs := st.box.Objects[:len(st.box.Objects)-1]
			st.box.Objects = append(append(objs, obj), st.live)
			st.box.Refresh()
		}
	}
	v.redrawLive(st, iv)
}

// redrawLive draws the live paragraph now, or once streamEvery has passed.
func (v *chatView) redrawLive(st *streamView, iv *itemView) {
	if wait := streamEvery - time.Since(st.last); wait > 0 {
		if !st.pending {
			st.pending = true
			time.AfterFunc(wait, func() {
				v.d.do(func() {
					st.pending = false
					if iv.st == st && st.it.Streaming {
						v.redrawLive(st, iv)
					}
				})
			})
		}
		return
	}
	tail := strings.TrimLeft(st.it.Text[min(st.done, len(st.it.Text)):], "\n")
	if tail == st.shown {
		return
	}
	st.last = time.Now()
	from := st.shown
	if !st.animated || !strings.HasPrefix(tail, from) || len(tail)-len(from) < 4 {
		v.drawLive(st, iv, tail)
		return
	}
	// Smooth reveal: the new words appear over streamReveal instead of all
	// at once.
	if st.reveal != nil {
		st.reveal.Stop()
	}
	st.reveal = fyne.NewAnimation(streamReveal, func(f float32) {
		if iv.st != st {
			return
		}
		n := len(from) + int(f*float32(len(tail)-len(from)))
		if f >= 1 {
			n = len(tail)
		} else if sp := strings.IndexAny(tail[n:], " \n"); sp >= 0 {
			n += sp // whole words only
		} else {
			n = len(tail)
		}
		if part := tail[:n]; part != st.shown {
			v.drawLive(st, iv, part)
		}
	})
	st.reveal.Curve = fyne.AnimationEaseOut
	st.reveal.Start()
}

func (v *chatView) drawLive(st *streamView, iv *itemView, text string) {
	st.shown = text
	st.renders++
	if strings.TrimSpace(text) == "" {
		st.live.Objects = nil
	} else {
		st.live.Objects = []fyne.CanvasObject{v.markdown(closeOpenMarkdown(text))}
	}
	st.live.Refresh()
	if h := iv.holder.MinSize().Height; h != iv.height {
		iv.height = h
		relayout(v.box)
		if v.follow && !v.steps {
			v.scrollBottom()
		}
	}
}

// fadeIn shows obj under a cover in the chat's background colour that
// clears over streamFade.
func (v *chatView) fadeIn(obj fyne.CanvasObject) fyne.CanvasObject {
	bg := theme.Color(theme.ColorNameBackground)
	r, g, b, _ := bg.RGBA()
	base := color.NRGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 0xff}
	cover := canvas.NewRectangle(base)
	a := fyne.NewAnimation(streamFade, func(f float32) {
		c := base
		c.A = uint8((1 - f) * 255)
		cover.FillColor = c
		cover.Refresh()
		if f >= 1 {
			cover.Hide()
		}
	})
	a.Start()
	return container.NewStack(obj, cover)
}
