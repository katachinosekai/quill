package main

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// Streaming replies (user 2026-11-21, made smooth 2026-11-22): a reply is
// laid out paragraph by paragraph, the same way while it streams and after
// it ends (markdownBlocks), so nothing re-flows when it finishes. Finished
// paragraphs are rendered once and never redrawn or faded again; the
// growing last paragraph is rendered with its open markup closed for now.
// With "Animate new text" on, one timer per reply reveals the text at a
// steady pace, character by character (user 2026-11-22; it was whole words),
// speeding up gently when the model is ahead and never more than
// revealMaxStep characters per frame; off, text appears as it arrives.

const (
	prefAnimateText = "animateText"
	streamTick      = 16 * time.Millisecond // ~60 frames a second
	revealMinRate   = 2                     // characters per frame at least
	revealCatchUp   = 12                    // frames to close the gap, roughly
	revealMaxStep   = 24                    // characters per frame at most
)

// Text animation speed (user 2026-11-23, rescaled the same day: even Slow felt
// fast): each speed is a fraction of the pace above (the old Normal). Fast is
// 1/2, Normal 1/4 (the default), Slow 1/8; Instant shows text at once (no
// reveal). Retune a speed by changing its one constant. It only matters while
// "Animate new text" is on.
const (
	speedScaleFast   = 1.0 / 2
	speedScaleNormal = 1.0 / 4
	speedScaleSlow   = 1.0 / 8
)

const prefAnimateSpeed = "animateSpeed"

var animSpeedNames = []string{"Slow", "Normal", "Fast", "Instant"}

// speedKey is the stored value of a speed name ("Normal" -> "normal").
func speedKey(name string) string { return strings.ToLower(name) }

// speedName is the display name of a stored value; unknown means Normal.
func speedName(key string) string {
	for _, n := range animSpeedNames {
		if speedKey(n) == key {
			return n
		}
	}
	return "Normal"
}

// revealRate is how many bytes the reveal advances per frame (can be below
// one) when gap bytes are still hidden, at a speed (stored value; unknown
// means normal).
func revealRate(speed string, gap int) float64 {
	scale := speedScaleNormal
	switch speed {
	case "slow":
		scale = speedScaleSlow
	case "fast":
		scale = speedScaleFast
	case "instant":
		return float64(gap)
	}
	return float64(min(max(revealMinRate, gap/revealCatchUp), revealMaxStep)) * scale
}

func (d *desk) animateSpeed() string {
	return speedKey(speedName(d.app.Preferences().StringWithFallback(prefAnimateSpeed, "normal")))
}

type streamView struct {
	it       *chat.Item
	box      *fyne.Container // finished paragraphs, then live
	live     *fyne.Container // the growing last paragraph
	done     int             // finished paragraphs drawn
	pos      int             // bytes of it.Text revealed
	shown    string          // the live paragraph as drawn
	ticking  bool
	animated bool
	speed    string  // animateSpeed when the reply started
	carry    float64 // fraction of a byte not yet revealed
	renders  int     // live redraws (tests)

	reveals   []reveal           // characters shown in the last textFade (textfade.go)
	fadeDirty bool               // the last draw had faded characters
	lastFade  [textFadeSteps]int // the fade steps of the last draw
	liveRT    *widget.RichText   // the live paragraph's one RichText, redrawn in place
}

func (d *desk) animateText() bool {
	return d.app.Preferences().BoolWithFallback(prefAnimateText, true)
}

// splitParas cuts text at blank lines outside code fences. The last part is
// the paragraph still growing (it can be empty).
func splitParas(text string) (done []string, live string) {
	inFence, start, pos := false, 0, 0
	lines := strings.SplitAfter(text, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			inFence = !inFence
		}
		pos += len(l)
		if !inFence && strings.TrimSpace(l) == "" && strings.TrimSpace(text[pos:]) != "" && !betweenLettered(lines, i) {
			if p := strings.TrimSpace(text[start:pos]); p != "" {
				done = append(done, p)
			}
			start = pos
		}
	}
	return done, strings.TrimLeft(text[start:], "\n")
}

// rawLettered: a markdown line that starts a lettered item ("a. ",
// "**b)** ").
var rawLettered = regexp.MustCompile(`^\s*(?:\*\*)?[a-hA-H][.)](?:\*\*)?\s`)

// betweenLettered: blank line i sits between two lettered lines, which
// stay one list (blank lines between items are fine, as in Quill Android).
func betweenLettered(lines []string, i int) bool {
	p := i - 1
	for p >= 0 && strings.TrimSpace(lines[p]) == "" {
		p--
	}
	n := i + 1
	for n < len(lines) && strings.TrimSpace(lines[n]) == "" {
		n++
	}
	return p >= 0 && n < len(lines) && rawLettered.MatchString(lines[p]) && rawLettered.MatchString(lines[n])
}

// streamCut: the offset where the growing last paragraph starts (0 = none
// finished yet).
func streamCut(text string) int {
	inFence, cut, pos := false, 0, 0
	for _, l := range strings.SplitAfter(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			inFence = !inFence
		}
		pos += len(l)
		if !inFence && strings.TrimSpace(l) == "" && pos > len(l) && strings.TrimSpace(text[pos:]) != "" {
			cut = pos
		}
	}
	return cut
}

// markdownBlocks lays out a reply paragraph by paragraph (the same layout a
// streaming reply has).
func (v *chatView) markdownBlocks(text string) fyne.CanvasObject {
	done, live := splitParas(strings.TrimRight(text, "\n"))
	box := container.NewVBox()
	for _, p := range done {
		box.Add(v.markdown(p))
	}
	if strings.TrimSpace(live) != "" {
		box.Add(v.markdown(live))
	}
	if len(box.Objects) == 1 {
		return box.Objects[0]
	}
	return box
}

// streaming: this reply is still drawn by refreshStream (it is streaming,
// or its reveal is still catching up).
func streaming(it *chat.Item, iv *itemView) bool {
	return it.Kind == chat.Assistant && (it.Streaming || iv.st != nil && iv.st.it == it && iv.st.pos < len(it.Text))
}

// refreshStream draws a streaming assistant reply (UI thread).
func (v *chatView) refreshStream(it *chat.Item, iv *itemView) {
	st := iv.st
	if st == nil || st.it != it {
		st = &streamView{it: it, live: container.NewStack(), animated: v.d.animateText() && v.d.animateSpeed() != "instant", speed: v.d.animateSpeed()}
		st.box = container.NewVBox(st.live)
		iv.st = st
		iv.holder.Objects = []fyne.CanvasObject{spaced(it.Kind, msgText(st.box))}
		iv.holder.Refresh()
	}
	if !st.animated {
		st.pos = len(it.Text)
		v.drawStream(st, iv)
		return
	}
	if !st.ticking {
		st.ticking = true
		go v.revealLoop(st, iv)
	}
}

// revealLoop advances the reveal at a steady pace until it has caught up
// with a finished reply.
func (v *chatView) revealLoop(st *streamView, iv *itemView) {
	t := time.NewTicker(streamTick)
	defer t.Stop()
	for range t.C {
		stop := false
		v.d.do(func() {
			if iv.st != st {
				stop = true
				return
			}
			text := st.it.Text
			if st.pos < len(text) {
				st.advance(text)
				v.drawStream(st, iv)
			} else if st.fadeActive() {
				v.drawStream(st, iv) // the last characters finish fading in
			}
			if st.pos >= len(text) && !st.fadeActive() {
				// Caught up: stop ticking (no idle timer while Claude
				// thinks); the next text starts it again.
				st.ticking = false
				stop = true
				if !st.it.Streaming {
					v.refreshItem(st.it, iv) // the finished reply, same layout
				}
			}
		})
		if stop {
			return
		}
	}
}

// advance reveals one frame's worth of text (a steady pace, character by
// character, never in the middle of a multi-byte one) and logs it for the
// fade.
func (st *streamView) advance(text string) {
	gap := len(text) - st.pos
	st.carry += revealRate(st.speed, gap)
	step := int(st.carry)
	st.carry -= float64(step)
	n := min(st.pos+step, len(text))
	for n < len(text) && !utf8.RuneStart(text[n]) {
		n++
	}
	if st.animated {
		st.noteReveal(utf8.RuneCountInString(text[st.pos:n]), streamNow())
	}
	st.pos = n
}

// drawStream shows st.it.Text[:st.pos]: new finished paragraphs are added
// once, the live paragraph is redrawn when it changed.
// Each new paragraph of a streaming reply gets its own message theme: a
// widget added to the reply's existing ThemeOverride drew at the default
// size until that override was refreshed, so text snapped between sizes
// while streaming (user 2026-11-22; list text also needed the RichText
// parent fix in third_party/fyne/widget/richtext.go).
func (v *chatView) drawStream(st *streamView, iv *itemView) {
	done, live := splitParas(st.it.Text[:st.pos])
	if len(done) < st.done {
		// A paragraph counted as finished joined the next one (a lettered
		// item followed by another after a blank line): draw them again.
		st.box.Objects = []fyne.CanvasObject{st.live}
		st.done = 0
	}
	if len(done) > st.done {
		objs := st.box.Objects[:len(st.box.Objects)-1]
		for _, p := range done[st.done:] {
			objs = append(objs, msgText(v.markdown(p)))
		}
		st.box.Objects = append(objs, st.live)
		st.done = len(done)
		st.liveRT = nil // a new live paragraph starts
		st.box.Refresh()
	}
	counts, total := st.fadeCounts(streamNow())
	// A frame is redrawn when the text changed or a character moved to the
	// next fade step (not every frame: at Slow most frames change nothing).
	if live != st.shown || counts != st.lastFade || st.fadeDirty && total == 0 {
		st.shown, st.lastFade = live, counts
		st.renders++
		st.fadeDirty = total > 0
		switch {
		case strings.TrimSpace(live) == "":
			st.liveRT = nil
			st.live.Objects = nil
			st.live.Refresh()
		case st.animated:
			v.drawLiveFaded(st, closeOpenMarkdown(live), counts)
		default:
			st.live.Objects = []fyne.CanvasObject{msgText(v.markdown(closeOpenMarkdown(live)))}
			st.live.Refresh()
		}
	}
	v.settleHeight(iv)
}
