package main

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
)

// Control fade-in (user 2026-11-23): when the controls of a plan or question
// first appear (radio group, buttons, checkboxes, text fields, Send answers,
// or the fallback card), they fade in over controlFade. Fyne has no
// per-widget opacity, so each control set is stacked under a rectangle in the
// chat background colour whose alpha runs from opaque to clear
// (fyne.NewColorRGBAAnimation on its premultiplied colour, a plain linear
// fade). The rectangle takes no clicks; the click guard (600 ms, longer than
// the fade) already ignores clicks until the controls are fully shown.
//
// A fade starts once per item: when it first shows controls that are not
// already answered ("none"). Re-renders, a repeated question, and state
// changes (answered, replaced, resend) do not start another; a re-render
// during the fade continues it from where it is.
const controlFade = 200 * time.Millisecond

// fadeIn wraps controls of item seq with the fade veil (see above). The
// controls are built before noteButtons says what they are, so a first build's
// veils wait in fadePend: noteButtons starts them, or clears them when the
// item has nothing to ask.
func (v *chatView) fadeIn(seq int, o fyne.CanvasObject) fyne.CanvasObject {
	r := canvas.NewRectangle(colBackground)
	if start, ok := v.fadeStart[seq]; ok && time.Since(start) < controlFade {
		v.runFade(r, controlFade-time.Since(start), float64(controlFade-time.Since(start))/float64(controlFade))
	} else if _, seen := v.btnKinds[seq]; !seen {
		v.fadePend = append(v.fadePend, r)
	} else {
		return o
	}
	return container.NewStack(o, r)
}

// startFades is noteButtons' part: first says the item shows its first
// buttons; kind "none" means no live controls (nothing to fade).
func (v *chatView) startFades(seq int, first bool, kind string) {
	pend := v.fadePend
	v.fadePend = nil
	if !first || kind == "none" {
		for _, r := range pend {
			r.Hide()
		}
		return
	}
	if v.fadeStart == nil {
		v.fadeStart = map[int]time.Time{}
	}
	v.fadeStart[seq] = time.Now()
	v.fadesStarted++
	for _, r := range pend {
		v.runFade(r, controlFade, 1)
	}
}

// runFade fades r from the given share of opaque to clear over d.
func (v *chatView) runFade(r *canvas.Rectangle, d time.Duration, from float64) {
	r.FillColor = veil(from)
	if fyne.CurrentApp() == nil {
		return
	}
	a := canvas.NewColorRGBAAnimation(veil(from), color.RGBA{}, d, func(c color.Color) {
		r.FillColor = c
		r.Refresh()
	})
	a.Curve = fyne.AnimationLinear
	a.Start()
}

// veil: the background colour at a share of full opacity (premultiplied).
func veil(f float64) color.RGBA {
	s := func(c uint8) uint8 { return uint8(float64(c) * f) }
	return color.RGBA{s(colBackground.R), s(colBackground.G), s(colBackground.B), s(colBackground.A)}
}
