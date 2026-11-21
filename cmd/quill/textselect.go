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
	pass     fyne.Draggable
	passAt   fyne.Position
}

func (l *selLayer) draggableAt(p fyne.Position) (fyne.Draggable, fyne.Position) {
	root := l.content()
	if root == nil {
		return nil, fyne.Position{}
	}
	origin := root.Position()
	var hit fyne.Draggable
	var at fyne.Position
	driver.WalkVisibleObjectTree(root, func(o fyne.CanvasObject, pos fyne.Position, _ fyne.Position, _ fyne.Size) bool {
		dr, ok := o.(fyne.Draggable)
		if !ok || o == l {
			return false
		}
		if _, row := o.(*queueRow); row {
			return false
		}
		pos = pos.Subtract(origin)
		sz := o.Size()
		if p.X >= pos.X && p.X <= pos.X+sz.Width && p.Y >= pos.Y && p.Y <= pos.Y+sz.Height {
			hit, at = dr, pos
		}
		return false
	}, nil)
	return hit, at
}

func (l *selLayer) Resize(s fyne.Size) {
	l.BaseWidget.Resize(s)
	if l.has {
		l.Refresh()
	}
}

func newSelLayer(v *chatView) *selLayer {
	l := &selLayer{v: v}
	l.copyBtn = widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() { l.copy() })
	l.copyBtn.Importance = widget.HighImportance
	l.ExtendBaseWidget(l)
	return l
}

func (l *selLayer) Dragged(e *fyne.DragEvent) {
	if !l.dragging && l.pass == nil {
		start := e.Position.Subtract(e.Dragged)
		if dr, at := l.draggableAt(start); dr != nil {
			l.pass, l.passAt = dr, at
		}
	}
	if l.pass != nil {
		ev := *e
		ev.Position = e.Position.Subtract(l.passAt)
		l.pass.Dragged(&ev)
		return
	}
	if !l.dragging {
		l.dragging = true
		l.from = e.Position.Subtract(e.Dragged)
		if l.v != nil && l.v.d != nil {
			for _, v := range l.v.d.allViews() {
				if v.sel != nil && v.sel != l {
					v.sel.clear()
				}
			}
		}
	}
	l.to = e.Position
	l.has = true
	l.autoScroll()
	l.Refresh()
}

const edgeScroll = 28

func (l *selLayer) autoScroll() {
	if l.v == nil || l.v.scroll == nil {
		return
	}
	sc := l.v.scroll
	view := sc.Size().Height
	content := sc.Content.MinSize().Height
	y := l.Position().Y + l.to.Y - sc.Offset.Y
	off := sc.Offset.Y
	switch {
	case y < edgeScroll:
		off = max(0, off-edgeScroll)
		l.v.follow = false
	case y > view-edgeScroll:
		off = min(max(0, content-view), off+edgeScroll)
	default:
		return
	}
	if off != sc.Offset.Y {
		sc.Offset.Y = off
		sc.Refresh()
	}
}

func (l *selLayer) selectWordAt(abs fyne.Position) {
	runs := collectRuns(l.content())
	if len(runs) == 0 {
		return
	}
	p := abs.Subtract(fyne.CurrentApp().Driver().AbsolutePositionForObject(l))
	pt := hitRun(runs, p)
	if pt.run < 0 {
		return
	}
	r := runs[pt.run]
	c := min(pt.char, len([]rune(r.t.Text)))
	ws, we := wordStart(r, c), wordEnd(r, c)
	if we <= ws {
		return
	}
	y := r.pos.Y + r.t.Size().Height/2
	l.from = fyne.NewPos(charX(r, ws)+0.1, y)
	l.to = fyne.NewPos(charX(r, we)-0.1, y)
	l.has, l.dragging = true, false
	l.Refresh()
}

func (l *selLayer) selectAll() {
	runs := collectRuns(l.content())
	if len(runs) == 0 {
		return
	}
	first, last := runs[0], runs[len(runs)-1]
	l.from = fyne.NewPos(textStart(first), first.pos.Y+first.t.Size().Height/2)
	l.to = fyne.NewPos(runEnd(last)+1, last.pos.Y+last.t.Size().Height/2)
	l.has, l.dragging = true, false
	l.Refresh()
}

func (l *selLayer) DragEnd() {
	if l.pass != nil {
		p := l.pass
		l.pass = nil
		p.DragEnd()
		return
	}
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

type textRow struct {
	top, bottom float32
	first, last int
}

func buildRows(runs []textRun) []textRow {
	var rows []textRow
	for i, r := range runs {
		h := r.t.Size().Height
		mid := r.pos.Y + h/2
		if n := len(rows); n > 0 && mid >= rows[n-1].top && mid <= rows[n-1].bottom {
			rows[n-1].last = i
			rows[n-1].bottom = max(rows[n-1].bottom, r.pos.Y+h)
			continue
		}
		rows = append(rows, textRow{top: r.pos.Y, bottom: r.pos.Y + h, first: i, last: i})
	}
	return rows
}

func runEnd(r textRun) float32 {
	return charX(r, len([]rune(r.t.Text)))
}

func hitRun(runs []textRun, p fyne.Position) selPoint {
	rows := buildRows(runs)
	if len(rows) == 0 {
		return selPoint{-1, 0}
	}
	k := len(rows) - 1
	for i := range rows {
		hi := rows[i].bottom
		if i+1 < len(rows) {
			hi = (rows[i].bottom + rows[i+1].top) / 2
		}
		if p.Y <= hi || i == len(rows)-1 {
			k = i
			break
		}
	}
	row := rows[k]
	if p.X <= textStart(runs[row.first]) {
		return selPoint{row.first, 0}
	}
	for i := row.first; i <= row.last; i++ {
		r := runs[i]
		if p.X < textStart(r) {
			prev := runs[i-1]
			if p.X-runEnd(prev) < textStart(r)-p.X {
				return selPoint{i - 1, len([]rune(prev.t.Text))}
			}
			return selPoint{i, 0}
		}
		if p.X <= runEnd(r) {
			return selPoint{i, charAt(r, p.X)}
		}
	}
	return selPoint{row.last, len([]rune(runs[row.last].t.Text))}
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t'
}

func wordStart(r textRun, c int) int {
	rs := []rune(r.t.Text)
	for c > 0 && c <= len(rs) && !isSpace(rs[c-1]) {
		c--
	}
	return c
}

func wordEnd(r textRun, c int) int {
	rs := []rune(r.t.Text)
	for c < len(rs) && !isSpace(rs[c]) {
		c++
	}
	return c
}

func (l *selLayer) span(runs []textRun) (selPoint, selPoint, bool) {
	if !l.has || len(runs) == 0 {
		return selPoint{}, selPoint{}, false
	}
	a, b := hitRun(runs, l.from), hitRun(runs, l.to)
	if a.run < 0 || b.run < 0 {
		return selPoint{}, selPoint{}, false
	}
	if b.run < a.run || b.run == a.run && b.char < a.char {
		a, b = b, a
	}
	if a.run == b.run && a.char == b.char {
		return a, b, false
	}
	same := a.run == b.run && !strings.ContainsFunc(string([]rune(runs[a.run].t.Text)[a.char:b.char]), isSpace)
	if !same {
		a.char = wordStart(runs[a.run], a.char)
		b.char = wordEnd(runs[b.run], b.char)
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
	var lastH float32
	if ok {
		rows := buildRows(runs)
		for k, row := range rows {
			if row.last < a.run || row.first > b.run {
				continue
			}
			x0, x1 := float32(-1), float32(-1)
			for i := max(row.first, a.run); i <= min(row.last, b.run); i++ {
				run := runs[i]
				c0, c1 := 0, len([]rune(run.t.Text))
				if i == a.run {
					c0 = a.char
				}
				if i == b.run {
					c1 = b.char
				}
				if c1 < c0 {
					continue
				}
				lo, hi := charX(run, c0), charX(run, c1)
				if x0 < 0 || lo < x0 {
					x0 = lo
				}
				if hi > x1 {
					x1 = hi
				}
			}
			if x0 < 0 || x1 <= x0 {
				continue
			}
			bottom := row.bottom
			if k+1 < len(rows) && rows[k+1].first <= b.run && rows[k+1].top-row.bottom < (row.bottom-row.top)/2 {
				bottom = rows[k+1].top
			}
			rect := canvas.NewRectangle(col)
			rect.CornerRadius = 3
			rect.Move(fyne.NewPos(x0, row.top))
			rect.Resize(fyne.NewSize(x1-x0, bottom-row.top))
			r.rects = append(r.rects, rect)
			last = fyne.NewPos(max(x1, runEnd(runs[row.last])), row.top)
			lastH = row.bottom - row.top
		}
	}
	r.objs = append([]fyne.CanvasObject{}, r.rects...)
	if ok && !l.dragging {
		btn := l.copyBtn
		size := btn.MinSize()
		pos := fyne.NewPos(last.X+6, last.Y-(size.Height-lastH)/2)
		if w := l.Size().Width; w > 0 && pos.X+size.Width > w {
			pos = fyne.NewPos(max(0, min(last.X-size.Width, w-size.Width)), last.Y+lastH+2)
		}
		btn.Move(pos)
		btn.Resize(size)
		r.objs = append(r.objs, btn)
	}
	canvas.Refresh(l)
}
