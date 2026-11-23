package main

import (
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Fade-in of newly revealed characters (user 2026-11-23): while a reply is
// revealed at Slow, Normal or Fast, the characters shown in the last
// textFade are drawn with a rising alpha instead of popping in. Fyne text
// has one colour per segment, so the tail of the live paragraph is cut into
// segments of textFadeSteps colours (quillFade0..: the text colour at 1/5
// .. 4/5 alpha, msgTheme.Color), the newest characters faintest. This only
// changes how the text is drawn: the reveal itself (revealRate, pos) is
// untouched. Nothing is allocated per character or kept per frame: the
// reveal log holds a handful of batches, and the live paragraph reuses one
// RichText that is re-parsed in place (streamView.liveRT).
const (
	textFade      = 150 * time.Millisecond
	textFadeSteps = 4
)

// streamNow is the clock of the fade (tests replace it).
var streamNow = time.Now

// fadeNames: the theme colour of each fade step, newest (faintest) first.
var fadeNames = [textFadeSteps]fyne.ThemeColorName{"quillFade0", "quillFade1", "quillFade2", "quillFade3"}

// fadeAlpha: the alpha of fade step k.
func fadeAlpha(k int) uint8 { return uint8(255 * (k + 1) / (textFadeSteps + 1)) }

type reveal struct {
	runes int
	at    time.Time
}

// noteReveal logs n newly shown characters.
func (st *streamView) noteReveal(runes int, at time.Time) {
	if runes > 0 {
		st.reveals = append(st.reveals, reveal{runes, at})
	}
}

// fadeCounts: how many characters are at each fade step now (step 0 the
// newest); batches older than textFade are dropped (in place).
func (st *streamView) fadeCounts(now time.Time) (c [textFadeSteps]int, total int) {
	keep := st.reveals[:0]
	for _, r := range st.reveals {
		age := now.Sub(r.at)
		if age >= textFade {
			continue
		}
		k := int(age * textFadeSteps / textFade)
		c[max(k, 0)] += r.runes
		total += r.runes
		keep = append(keep, r)
	}
	st.reveals = keep
	return c, total
}

// fadeActive: the reveal log still has batches, or the last draw was faded
// (one more draw puts the colours back).
func (st *streamView) fadeActive() bool { return len(st.reveals) > 0 || st.fadeDirty }

// fadeBudget: the characters still to fade, per step.
type fadeBudget struct{ n [textFadeSteps]int }

func (b *fadeBudget) empty() bool {
	for _, n := range b.n {
		if n > 0 {
			return false
		}
	}
	return true
}

// take removes up to max characters from the lowest step with any left.
func (b *fadeBudget) take(max int) (step, n int) {
	for k := range b.n {
		if b.n[k] > 0 {
			n = min(b.n[k], max)
			b.n[k] -= n
			return k, n
		}
	}
	return 0, 0
}

// fadeSegs colours the last characters of segs (the budget's) by step.
// Segments are the caller's own, just parsed; the ones touched are replaced.
func fadeSegs(segs []widget.RichTextSegment, b *fadeBudget) []widget.RichTextSegment {
	var rev []widget.RichTextSegment // the replaced tail, last first
	i := len(segs)
	for ; i > 0 && !b.empty(); i-- {
		switch s := segs[i-1].(type) {
		case *widget.TextSegment:
			runes := []rune(s.Text)
			var pieces []widget.RichTextSegment // last first
			for len(runes) > 0 && !b.empty() {
				step, n := b.take(len(runes))
				c := *s
				c.Text = string(runes[len(runes)-n:])
				if s.Style.ColorName == theme.ColorNameForeground {
					c.Style.ColorName = fadeNames[step]
				}
				pieces = append(pieces, &c)
				runes = runes[:len(runes)-n]
			}
			if len(runes) > 0 {
				c := *s
				c.Text = string(runes)
				pieces = append(pieces, &c)
			}
			rev = append(rev, pieces...)
		case *widget.ParagraphSegment:
			s.Texts = fadeSegs(s.Texts, b)
			rev = append(rev, s)
		case *widget.ListSegment:
			s.Items = fadeSegs(s.Items, b)
			rev = append(rev, s)
		default:
			// Not text we can colour (a link, a rule): its characters count.
			b.take(utf8.RuneCountInString(s.Textual()))
			rev = append(rev, s)
		}
	}
	if len(rev) == 0 {
		return segs
	}
	out := append(segs[:i:i], make([]widget.RichTextSegment, 0, len(rev))...)
	for j := len(rev) - 1; j >= 0; j-- {
		out = append(out, rev[j])
	}
	return out
}

// drawLiveFaded draws the live paragraph md into its one reused RichText,
// the newest characters faded. False when this paragraph can't be done so
// (centred layout, lettered list): the caller draws it plainly.
func (v *chatView) drawLiveFaded(st *streamView, md string, counts [textFadeSteps]int) bool {
	if v.d.center || hasLetteredRun(md) {
		st.liveRT = nil
		return false
	}
	if st.liveRT == nil {
		rt := widget.NewRichText()
		rt.Wrapping = fyne.TextWrapWord
		st.liveRT = rt
		st.live.Objects = []fyne.CanvasObject{msgText(rt)}
		st.live.Refresh()
	}
	st.liveRT.ParseMarkdown(hardBreaks(autolinks(md)))
	wrapCodeBlocks(st.liveRT)
	b := fadeBudget{counts}
	st.liveRT.Segments = fadeSegs(st.liveRT.Segments, &b)
	st.liveRT.Refresh()
	return true
}

// wrapCodeBlocks: code blocks wrap their lines (as markdown does).
func wrapCodeBlocks(rt *widget.RichText) {
	for i, seg := range rt.Segments {
		if cb, ok := seg.(*widget.CodeBlockSegment); ok {
			rt.Segments[i] = &wrapCode{text: cb.Text}
		}
	}
}
