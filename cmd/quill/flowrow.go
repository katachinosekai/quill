package main

import (
	"fyne.io/fyne/v2"
)

// flowRow lays buttons out left to right and wraps them onto further lines
// when the row is narrower than the buttons (user 2026-11-21: on a small
// window a choice card's three buttons forced the whole chat wider than the
// window, clipping every message on the right). Its minimum width is only
// that of a short button, so it never widens the chat; a single button wider
// than the row is shrunk to the row's width.
type flowRow struct {
	gap    float32
	center bool
	width  float32        // width of the last layout; decides how many lines MinSize reports
	hint   func() float32 // expected width before the first layout (the chat column)
}

const flowMinWidth = 140

func newFlowRow(gap float32, center bool, hint func() float32, objs ...fyne.CanvasObject) *fyne.Container {
	return fyne.NewContainerWithLayout(&flowRow{gap: gap, center: center, hint: hint}, objs...)
}

func (l *flowRow) lines(objs []fyne.CanvasObject, width float32) [][]fyne.CanvasObject {
	var out [][]fyne.CanvasObject
	var line []fyne.CanvasObject
	used := float32(0)
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		w := min(o.MinSize().Width, width)
		if len(line) > 0 && used+l.gap+w > width {
			out = append(out, line)
			line, used = nil, 0
		}
		if len(line) > 0 {
			used += l.gap
		}
		line = append(line, o)
		used += w
	}
	if len(line) > 0 {
		out = append(out, line)
	}
	return out
}

func (l *flowRow) heightFor(objs []fyne.CanvasObject, width float32) float32 {
	h := float32(0)
	for i, line := range l.lines(objs, width) {
		lh := float32(0)
		for _, o := range line {
			lh = max(lh, o.MinSize().Height)
		}
		if i > 0 {
			h += l.gap
		}
		h += lh
	}
	return h
}

func (l *flowRow) MinSize(objs []fyne.CanvasObject) fyne.Size {
	widest, single := float32(0), float32(0)
	for i, o := range objs {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		widest = max(widest, m.Width)
		if i > 0 {
			single += l.gap
		}
		single += m.Width
	}
	w := l.width
	if w <= 0 && l.hint != nil {
		w = l.hint()
	}
	if w <= 0 {
		w = single // nothing known yet: one line
	}
	return fyne.NewSize(min(widest, flowMinWidth), l.heightFor(objs, w))
}

func (l *flowRow) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for _, line := range l.lines(objs, size.Width) {
		lineW, lh := float32(0), float32(0)
		for i, o := range line {
			if i > 0 {
				lineW += l.gap
			}
			lineW += min(o.MinSize().Width, size.Width)
			lh = max(lh, o.MinSize().Height)
		}
		x := float32(0)
		if l.center && lineW < size.Width {
			x = (size.Width - lineW) / 2
		}
		for _, o := range line {
			w := min(o.MinSize().Width, size.Width)
			o.Move(fyne.NewPos(x, y))
			o.Resize(fyne.NewSize(w, lh))
			x += w + l.gap
		}
		y += lh + l.gap
	}
	l.width = size.Width // the chat relayouts after a resize and then asks MinSize again
}
