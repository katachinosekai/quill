package main

import (
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type textRun struct {
	t     *canvas.Text
	pos   fyne.Position
	owner *widget.RichText
	src   int
}

type selPoint struct {
	run, char int
}

type selLayer struct {
	widget.BaseWidget
	v        *chatView
	from, to fyne.Position
	has      bool
	dragging bool
	copyBtn  *widget.Button
}

func newSelLayer(v *chatView) *selLayer {
	l := &selLayer{v: v}
	l.copyBtn = widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() { l.copy() })
	l.copyBtn.Importance = widget.HighImportance
	l.ExtendBaseWidget(l)
	return l
}

func (l *selLayer) Dragged(e *fyne.DragEvent) {
	if !l.dragging {
		l.dragging = true
		l.from = e.Position.Subtract(e.Dragged)
	}
	l.to = e.Position
	l.has = true
	l.Refresh()
}

func (l *selLayer) DragEnd() {
	l.dragging = false
	l.Refresh()
}

func (l *selLayer) clear() {
	if !l.has {
		return
	}
	l.has, l.dragging = false, false
	l.Refresh()
}

func (l *selLayer) content() fyne.CanvasObject {
	if l.v == nil || l.v.box == nil {
		return nil
	}
	return l.v.box
}

func (l *selLayer) copy() bool {
	text := l.text()
	if text == "" {
		return false
	}
	l.v.d.app.Clipboard().SetContent(text)
	l.clear()
	l.v.setLive("Copied " + plural(len([]rune(text)), "character"))
	return true
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

func collectRuns(root fyne.CanvasObject) []textRun {
	if root == nil {
		return nil
	}
	var runs []textRun
	var owners []*widget.RichText
	var skip fyne.CanvasObject
	origin := root.Position()
	driver.WalkVisibleObjectTree(root, func(o fyne.CanvasObject, pos fyne.Position, _ fyne.Position, _ fyne.Size) bool {
		pos = pos.Subtract(origin)
		if skip != nil {
			return false
		}
		switch x := o.(type) {
		case *widget.Button, *widget.Entry, *widget.Select, *widget.Check, *widget.RadioGroup, *widget.CheckGroup, *selLayer:
			skip = o
		case *widget.RichText:
			owners = append(owners, x)
		case *canvas.Text:
			if strings.TrimSpace(x.Text) != "" {
				var owner *widget.RichText
				if len(owners) > 0 {
					owner = owners[len(owners)-1]
				}
				runs = append(runs, textRun{t: x, pos: pos, owner: owner, src: -1})
			}
		}
		return false
	}, func(o fyne.CanvasObject, _ fyne.Position, _ fyne.CanvasObject) {
		if o == skip {
			skip = nil
		}
		if rt, ok := o.(*widget.RichText); ok && len(owners) > 0 && owners[len(owners)-1] == rt {
			owners = owners[:len(owners)-1]
		}
	})
	mapSources(runs)
	sort.SliceStable(runs, func(i, j int) bool {
		a, b := runs[i], runs[j]
		if a.pos.Y+a.t.Size().Height/2 < b.pos.Y {
			return true
		}
		if b.pos.Y+b.t.Size().Height/2 < a.pos.Y {
			return false
		}
		return a.pos.X < b.pos.X
	})
	return runs
}

func ownerSource(rt *widget.RichText) string {
	var b strings.Builder
	for _, seg := range rt.Segments {
		b.WriteString(seg.Textual())
		if !seg.Inline() {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func mapSources(runs []textRun) {
	srcs := map[*widget.RichText]string{}
	cur := map[*widget.RichText]int{}
	for i := range runs {
		rt := runs[i].owner
		if rt == nil {
			continue
		}
		src, ok := srcs[rt]
		if !ok {
			src = ownerSource(rt)
			srcs[rt] = src
		}
		if k := strings.Index(src[cur[rt]:], runs[i].t.Text); k >= 0 {
			runs[i].src = cur[rt] + k
			cur[rt] = runs[i].src + len(runs[i].t.Text)
		}
	}
}

func textStart(r textRun) float32 {
	w := fyne.MeasureText(r.t.Text, r.t.TextSize, r.t.TextStyle).Width
	switch r.t.Alignment {
	case fyne.TextAlignCenter:
		return r.pos.X + (r.t.Size().Width-w)/2
	case fyne.TextAlignTrailing:
		return r.pos.X + r.t.Size().Width - w
	}
	return r.pos.X
}

func charX(r textRun, n int) float32 {
	rs := []rune(r.t.Text)
	if n > len(rs) {
		n = len(rs)
	}
	return textStart(r) + fyne.MeasureText(string(rs[:n]), r.t.TextSize, r.t.TextStyle).Width
}

func charAt(r textRun, x float32) int {
	rs := []rune(r.t.Text)
	start := textStart(r)
	for i := range rs {
		w := fyne.MeasureText(string(rs[:i]), r.t.TextSize, r.t.TextStyle).Width
		cw := fyne.MeasureText(string(rs[i]), r.t.TextSize, r.t.TextStyle).Width
		if x < start+w+cw/2 {
			return i
		}
	}
	return len(rs)
}

func hitRun(runs []textRun, p fyne.Position) selPoint {
	if len(runs) == 0 {
		return selPoint{-1, 0}
	}
	best := -1
	for i, r := range runs {
		h := r.t.Size().Height
		if p.Y < r.pos.Y || p.Y > r.pos.Y+h {
			continue
		}
		if best == -1 || p.X >= textStart(r) {
			best = i
		}
	}
	if best >= 0 {
		return selPoint{best, charAt(runs[best], p.X)}
	}
	for i, r := range runs {
		if r.pos.Y > p.Y {
			return selPoint{i, 0}
		}
	}
	last := len(runs) - 1
	return selPoint{last, len([]rune(runs[last].t.Text))}
}

func (l *selLayer) span(runs []textRun) (selPoint, selPoint, bool) {
	if !l.has || len(runs) == 0 {
		return selPoint{}, selPoint{}, false
	}
	a, b := hitRun(runs, l.from), hitRun(runs, l.to)
	if b.run < a.run || b.run == a.run && b.char < a.char {
		a, b = b, a
	}
	if a.run == b.run && a.char == b.char {
		return a, b, false
	}
	return a, b, true
}

func selectedText(runs []textRun, a, b selPoint) string {
	var out strings.Builder
	i := a.run
	for i <= b.run {
		owner := runs[i].owner
		j := i
		for j+1 <= b.run && owner != nil && runs[j+1].owner == owner {
			j++
		}
		c0, c1 := 0, len([]rune(runs[j].t.Text))
		if i == a.run {
			c0 = a.char
		}
		if j == b.run {
			c1 = b.char
		}
		mapped := owner != nil && runs[i].src >= 0 && runs[j].src >= 0
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		if mapped {
			src := ownerSource(owner)
			start := runs[i].src + len(string([]rune(runs[i].t.Text)[:c0]))
			end := runs[j].src + len(string([]rune(runs[j].t.Text)[:c1]))
			if start <= end && end <= len(src) {
				out.WriteString(src[start:end])
				i = j + 1
				continue
			}
		}
		for k := i; k <= j; k++ {
			rs := []rune(runs[k].t.Text)
			s, e := 0, len(rs)
			if k == a.run {
				s = a.char
			}
			if k == b.run {
				e = b.char
			}
			if k > i {
				if runs[k].pos.Y > runs[k-1].pos.Y+runs[k-1].t.Size().Height/2 {
					out.WriteString("\n")
				} else {
					out.WriteString(" ")
				}
			}
			if s < e {
				out.WriteString(string(rs[s:e]))
			}
		}
		i = j + 1
	}
	return strings.TrimRight(out.String(), "\n")
}

func (l *selLayer) text() string {
	runs := collectRuns(l.content())
	a, b, ok := l.span(runs)
	if !ok {
		return ""
	}
	return selectedText(runs, a, b)
}

func (l *selLayer) CreateRenderer() fyne.WidgetRenderer {
	return &selRenderer{l: l}
}

type selRenderer struct {
	l     *selLayer
	rects []fyne.CanvasObject
	objs  []fyne.CanvasObject
}

func (r *selRenderer) Layout(fyne.Size) {}

func (r *selRenderer) MinSize() fyne.Size { return fyne.Size{} }

func (r *selRenderer) Objects() []fyne.CanvasObject { return r.objs }

func (r *selRenderer) Destroy() {}

func (r *selRenderer) Refresh() {
	l := r.l
	r.rects = r.rects[:0]
	var runs []textRun
	if l.has {
		runs = collectRuns(l.content())
	}
	a, b, ok := l.span(runs)
	col := theme.Color(theme.ColorNameSelection)
	var last fyne.Position
	if ok {
		for i := a.run; i <= b.run; i++ {
			run := runs[i]
			c0, c1 := 0, len([]rune(run.t.Text))
			if i == a.run {
				c0 = a.char
			}
			if i == b.run {
				c1 = b.char
			}
			if c1 <= c0 {
				continue
			}
			x0, x1 := charX(run, c0), charX(run, c1)
			rect := canvas.NewRectangle(col)
			rect.Move(fyne.NewPos(x0, run.pos.Y))
			rect.Resize(fyne.NewSize(x1-x0, run.t.Size().Height))
			r.rects = append(r.rects, rect)
			last = fyne.NewPos(x1, run.pos.Y+run.t.Size().Height)
		}
	}
	r.objs = append([]fyne.CanvasObject{}, r.rects...)
	if ok && !l.dragging {
		btn := l.copyBtn
		size := btn.MinSize()
		x := last.X - size.Width
		if x < 0 {
			x = 0
		}
		if w := l.Size().Width; w > 0 && x+size.Width > w {
			x = w - size.Width
		}
		btn.Move(fyne.NewPos(x, last.Y+2))
		btn.Resize(size)
		r.objs = append(r.objs, btn)
	}
	canvas.Refresh(l)
}
