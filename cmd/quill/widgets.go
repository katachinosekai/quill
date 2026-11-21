package main

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type maxWidthLayout struct {
	max   float32
	width func() float32
	shift func() float32
}

func (l maxWidthLayout) limit() float32 {
	if l.width != nil {
		return l.width()
	}
	return l.max
}

func (l maxWidthLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var s fyne.Size
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		s = s.Max(o.MinSize())
	}
	if m := l.limit(); s.Width > m {
		s.Width = m
	}
	return s
}

func (l maxWidthLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	w := min(size.Width, l.limit())
	x := (size.Width - w) / 2
	if l.shift != nil {
		x -= l.shift() / 2
	}
	x = max(0, min(x, size.Width-w))
	for _, o := range objs {
		placeGliding(o, rect{fyne.NewPos(x, 0), fyne.NewSize(w, size.Height)})
	}
}

const (
	glideWindow   = 400 * time.Millisecond
	glideDuration = 180 * time.Millisecond
)

var (
	glideUntil time.Time
	glides     = map[fyne.CanvasObject]*glideAnim{}
)

type rect struct {
	pos  fyne.Position
	size fyne.Size
}

type glideAnim struct {
	to   rect
	anim *fyne.Animation
}

func lerp(a, b, p float32) float32 { return a + (b-a)*p }

func glideStep(from, to rect, p float32) rect {
	return rect{
		fyne.NewPos(lerp(from.pos.X, to.pos.X, p), lerp(from.pos.Y, to.pos.Y, p)),
		fyne.NewSize(lerp(from.size.Width, to.size.Width, p), lerp(from.size.Height, to.size.Height, p)),
	}
}

type glideBorder struct{}

func (glideBorder) MinSize(objs []fyne.CanvasObject) fyne.Size {
	top, bottom, center := objs[0].MinSize(), objs[1].MinSize(), objs[2].MinSize()
	return fyne.NewSize(max(top.Width, bottom.Width, center.Width), top.Height+bottom.Height+center.Height+2*theme.Padding())
}

func (glideBorder) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	top, bottom, center := objs[0], objs[1], objs[2]
	pad := theme.Padding()
	th := float32(0)
	if top.Visible() {
		th = top.MinSize().Height
		top.Move(fyne.NewPos(0, 0))
		top.Resize(fyne.NewSize(size.Width, th))
		th += pad
	}
	bh := float32(0)
	if bottom.Visible() {
		bh = bottom.MinSize().Height
		bottom.Move(fyne.NewPos(0, size.Height-bh))
		bottom.Resize(fyne.NewSize(size.Width, bh))
		bh += pad
	}
	placeGliding(center, rect{fyne.NewPos(0, th), fyne.NewSize(size.Width, max(0, size.Height-th-bh))})
}

func shouldGlide(from, to rect) bool {
	return time.Now().Before(glideUntil) && !from.size.IsZero() && from != to
}

func placeGliding(o fyne.CanvasObject, to rect) {
	if g := glides[o]; g != nil {
		if g.to == to {
			return
		}
		g.anim.Stop()
		delete(glides, o)
	}
	from := rect{o.Position(), o.Size()}
	if !shouldGlide(from, to) {
		o.Move(to.pos)
		o.Resize(to.size)
		return
	}
	g := &glideAnim{to: to}
	g.anim = fyne.NewAnimation(glideDuration, func(p float32) {
		r := glideStep(from, to, p)
		o.Move(r.pos)
		o.Resize(r.size)
		if p >= 1 && glides[o] == g {
			delete(glides, o)
		}
	})
	g.anim.Curve = fyne.AnimationEaseOut
	glides[o] = g
	g.anim.Start()
}

func centered(max float32, obj fyne.CanvasObject) *fyne.Container {
	return container.New(maxWidthLayout{max: max}, obj)
}

func windowCentered(width, shift func() float32, obj fyne.CanvasObject) *fyne.Container {
	return container.New(maxWidthLayout{width: width, shift: shift}, obj)
}

type sendEntry struct {
	widget.Entry
	shift      bool
	onSend     func(string)
	suggestion string
	hint       string
	onKey      func(*fyne.KeyEvent) bool
	onSendNow  func(string)
	ctrl       bool
	menuOpen   func() bool
	onShortcut func(fyne.Shortcut) bool
	onHistory  func(*fyne.KeyEvent) bool
	hist       *history
	browsing   bool
	onFocus    func()
	onEscape   func() bool
	ac         *autocorrector
	known      func() func(string) bool
	lastFix    *acFix
	onFix      func(string)
	cursorGen  int
}

func (e *sendEntry) FocusGained() {
	if e.onFocus != nil {
		e.onFocus()
	}
	e.Entry.FocusGained()
}

func (e *sendEntry) TypedShortcut(s fyne.Shortcut) {
	if c, ok := s.(*desktop.CustomShortcut); ok && (c.KeyName == fyne.KeyReturn || c.KeyName == fyne.KeyEnter) {
		if c.Modifier == fyne.KeyModifierControl && !e.Disabled() {
			if e.onSendNow != nil {
				e.onSendNow(e.Text)
			} else if e.onSend != nil {
				e.onSend(e.Text)
			}
		}
		return
	}
	if e.onShortcut != nil && e.onShortcut(s) {
		return
	}
	e.Entry.TypedShortcut(s)
}

func (e *sendEntry) setSuggestion(s string) {
	if s == e.suggestion {
		return
	}
	e.suggestion = s
	if s != "" {
		e.SetPlaceHolder(s + "   (Tab)")
	} else {
		e.SetPlaceHolder(e.hint)
	}
}

func (e *sendEntry) AcceptsTab() bool {
	if e.menuOpen != nil && e.menuOpen() {
		return true
	}
	return e.suggestion != "" && e.Text == ""
}

func newSendEntry(onSend func(string)) *sendEntry {
	e := &sendEntry{onSend: onSend}
	e.MultiLine = true
	e.Wrapping = fyne.TextWrapWord
	e.SetMinRowsVisible(3)
	e.ExtendBaseWidget(e)
	return e
}

func (e *sendEntry) KeyDown(k *fyne.KeyEvent) {
	if k.Name == desktop.KeyShiftLeft || k.Name == desktop.KeyShiftRight {
		e.shift = true
	}
	if k.Name == desktop.KeyControlLeft || k.Name == desktop.KeyControlRight {
		e.ctrl = true
	}
	e.Entry.KeyDown(k)
}

func (e *sendEntry) KeyUp(k *fyne.KeyEvent) {
	if k.Name == desktop.KeyShiftLeft || k.Name == desktop.KeyShiftRight {
		e.shift = false
	}
	if k.Name == desktop.KeyControlLeft || k.Name == desktop.KeyControlRight {
		e.ctrl = false
	}
	e.Entry.KeyUp(k)
}

func (e *sendEntry) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyBackspace && e.undoFix() {
		return
	}
	e.lastFix = nil
	if e.onKey != nil && e.onKey(k) {
		return
	}
	if e.onHistory != nil && e.onHistory(k) {
		return
	}
	if k.Name == fyne.KeyEscape && e.onEscape != nil && e.onEscape() {
		return
	}
	if k.Name != fyne.KeyUp && k.Name != fyne.KeyDown && !e.browsing {
		e.hist = nil
	}
	if k.Name == fyne.KeyTab && e.AcceptsTab() {
		e.SetText(e.suggestion)
		e.SetCursorTextOffset(len([]rune(e.Text)))
		return
	}
	if (k.Name == fyne.KeyReturn || k.Name == fyne.KeyEnter) && !e.shift {
		if e.Disabled() {
			return
		}
		if e.ctrl && e.onSendNow != nil {
			e.onSendNow(e.Text)
			return
		}
		if e.onSend != nil {
			e.onSend(e.Text)
		}
		return
	}
	e.Entry.TypedKey(k)
}

func statusMark(st status) *fyne.Container {
	icon := widget.NewIcon(theme.NewColoredResource(theme.ViewRefreshIcon(), theme.ColorNamePrimary))
	icon.Hide()
	c := container.NewStack(container.NewCenter(sized(9, 9, dot(color.Transparent))), icon)
	setStatusMark(c, st)
	return c
}

func setStatusMark(c *fyne.Container, st status) {
	circle := c.Objects[0].(*fyne.Container).Objects[0].(*fyne.Container).Objects[0].(*canvas.Circle)
	icon := c.Objects[1]
	if st == stRestart {
		circle.Hide()
		icon.Show()
		return
	}
	icon.Hide()
	circle.Show()
	if circle.FillColor != st.color() {
		circle.FillColor = st.color()
		circle.Refresh()
	}
}

func dot(c color.Color) *canvas.Circle {
	d := canvas.NewCircle(c)
	d.StrokeWidth = 0
	return d
}

type fixedSize struct{ size fyne.Size }

func (f fixedSize) MinSize([]fyne.CanvasObject) fyne.Size { return f.size }
func (f fixedSize) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		s := f.size
		o.Resize(s)
		o.Move(fyne.NewPos(0, (size.Height-s.Height)/2))
	}
}

func sized(w, h float32, obj fyne.CanvasObject) *fyne.Container {
	return container.New(fixedSize{fyne.NewSize(w, h)}, obj)
}

func bg(c color.Color, radius float32, obj fyne.CanvasObject) *fyne.Container {
	r := canvas.NewRectangle(c)
	r.CornerRadius = radius
	return container.NewStack(r, container.NewPadded(obj))
}

func mutedText(s string, size float32) *canvas.Text {
	t := canvas.NewText(s, colMuted)
	t.TextSize = size
	return t
}

func padTop(v float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(1, v))
	return r
}

type clipLayout struct{}

func (clipLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var h float32
	for _, o := range objs {
		h = max(h, o.MinSize().Height)
	}
	return fyne.NewSize(10, h)
}

func (clipLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(theme.Padding(), 0))
		o.Resize(fyne.NewSize(size.Width-2*theme.Padding(), size.Height))
	}
}

func bgPad(c color.Color, radius, vpad, hpad float32, obj fyne.CanvasObject) *fyne.Container {
	r := canvas.NewRectangle(c)
	r.CornerRadius = radius
	return container.NewStack(r, container.New(layout.NewCustomPaddedLayout(vpad, vpad, hpad, hpad), obj))
}

type fitOrHide struct{}

func (fitOrHide) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var h float32
	for _, o := range objs {
		h = max(h, o.MinSize().Height)
	}
	return fyne.NewSize(0, h)
}

func (fitOrHide) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		m := o.MinSize()
		if m.Width+2*theme.Padding() > size.Width {
			o.Hide()
			continue
		}
		o.Show()
		o.Move(fyne.NewPos(theme.Padding(), (size.Height-m.Height)/2))
		o.Resize(m)
	}
}

type fitRight struct{}

func (fitRight) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return fitOrHide{}.MinSize(objs)
}

func (fitRight) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		m := o.MinSize()
		if m.Width+2*theme.Padding() > size.Width {
			o.Hide()
			continue
		}
		o.Show()
		o.Move(fyne.NewPos(size.Width-m.Width-theme.Padding(), (size.Height-m.Height)/2))
		o.Resize(m)
	}
}

// Resize re-places the text cursor whenever the message box changes width.
// Fyne's entry only moves its cursor during a resize if the text position
// under the cursor changes in that same layout pass; on the real driver the
// text re-wraps afterwards, so after a window resize the cursor kept its old
// spot (user 2026-11-21). Refresh re-wraps the text and moves the cursor; the
// second, deferred one catches a re-wrap that lands after this frame.
func (e *sendEntry) Resize(size fyne.Size) {
	old := e.Size()
	e.Entry.Resize(size)
	if old.Width == size.Width || old.IsZero() {
		return
	}
	e.Refresh()
	e.cursorGen++
	gen := e.cursorGen
	go func() {
		time.Sleep(30 * time.Millisecond)
		fyne.Do(func() {
			if e.cursorGen == gen {
				e.Refresh()
			}
		})
	}()
}
