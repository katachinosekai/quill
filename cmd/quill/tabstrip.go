package main

import (
	"time"

	"fyne.io/fyne/v2"
)

const (
	tabGap     = 3
	tabSpring  = 0.32
	tabSettled = 0.5
)

type tabStrip struct {
	d      *desk
	box    *fyne.Container
	chips  []*tabChip
	ids    map[*tabChip]string
	cur    map[*tabChip]float32
	held   *tabChip
	heldID string
	grab   float32
	ptr    float32
	ticker *time.Ticker
	stop   chan struct{}
}

func (t *tabStrip) setChips(chips []*tabChip) {
	t.chips = chips
	t.ids = map[*tabChip]string{}
	for id, c := range t.d.tabChips {
		t.ids[c] = id
	}
	objs := make([]fyne.CanvasObject, len(chips))
	for i, c := range chips {
		objs[i] = c
	}
	t.box.Objects = objs
	t.box.Refresh()
}

func (t *tabStrip) order() []*tabChip {
	if t.held == nil {
		return t.chips
	}
	var rest []*tabChip
	for _, c := range t.chips {
		if c != t.held {
			rest = append(rest, c)
		}
	}
	idx := len(rest)
	x := float32(0)
	center := t.ptr - t.grab + t.held.MinSize().Width/2
	for i, c := range rest {
		w := c.MinSize().Width
		if center < x+w/2 {
			idx = i
			break
		}
		x += w + tabGap
	}
	out := append([]*tabChip{}, rest[:idx]...)
	out = append(out, t.held)
	return append(out, rest[idx:]...)
}

func (t *tabStrip) targets() map[*tabChip]float32 {
	m := map[*tabChip]float32{}
	x := float32(0)
	for _, c := range t.order() {
		m[c] = x
		x += c.MinSize().Width + tabGap
	}
	if t.held != nil {
		m[t.held] = t.ptr - t.grab
	}
	return m
}

func (t *tabStrip) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for i, o := range objs {
		m := o.MinSize()
		w += m.Width
		if i > 0 {
			w += tabGap
		}
		h = max(h, m.Height)
	}
	return fyne.NewSize(w, h)
}

func (t *tabStrip) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	tg := t.targets()
	for _, o := range objs {
		c, ok := o.(*tabChip)
		if !ok {
			continue
		}
		x, moving := t.cur[c]
		if !moving || t.ticker == nil {
			x = tg[c]
			t.cur[c] = x
		}
		m := c.MinSize()
		c.Resize(fyne.NewSize(m.Width, size.Height))
		c.Move(fyne.NewPos(x, 0))
	}
}

func (t *tabStrip) drag(c *tabChip, id string, e *fyne.DragEvent) {
	origin := fyne.CurrentApp().Driver().AbsolutePositionForObject(t.box)
	if t.held == nil {
		t.held, t.heldID = c, id
		t.grab = e.Position.X
		t.animate()
	}
	t.ptr = e.AbsolutePosition.X - origin.X
}

func (t *tabStrip) drop() {
	if t.held == nil {
		return
	}
	final := t.order()
	var ids []string
	for _, c := range final {
		if id, ok := t.ids[c]; ok {
			ids = append(ids, id)
		}
	}
	var hidden []string
	shown := map[string]bool{}
	for _, id := range ids {
		shown[id] = true
	}
	for _, id := range t.d.tabs {
		if !shown[id] {
			hidden = append(hidden, id)
		}
	}
	t.held, t.heldID = nil, ""
	t.chips = final
	t.d.tabs = append(ids, hidden...)
	t.d.saveTabs()
	t.d.tabSig = ""
	t.animate()
}

func (t *tabStrip) animate() {
	if t.ticker != nil {
		return
	}
	t.ticker = time.NewTicker(16 * time.Millisecond)
	t.stop = make(chan struct{})
	ticker, stop := t.ticker, t.stop
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				t.d.do(t.step)
			}
		}
	}()
}

func (t *tabStrip) step() {
	if t.ticker == nil {
		return
	}
	tg := t.targets()
	settled := true
	for _, c := range t.chips {
		goal := tg[c]
		x, ok := t.cur[c]
		if !ok {
			x = goal
		}
		if c == t.held {
			x = goal
		} else {
			x += (goal - x) * tabSpring
			if d := goal - x; d > tabSettled || d < -tabSettled {
				settled = false
			} else {
				x = goal
			}
		}
		t.cur[c] = x
		c.Move(fyne.NewPos(x, 0))
	}
	if settled && t.held == nil {
		t.ticker.Stop()
		close(t.stop)
		t.ticker = nil
		t.d.syncTabs()
	}
}
