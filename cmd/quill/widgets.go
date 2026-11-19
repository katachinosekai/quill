package main

import (
	"image/color"
	"strings"

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
		h := size.Height
		o.Move(fyne.NewPos(x, 0))
		o.Resize(fyne.NewSize(w, h))
	}
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
}

func (e *sendEntry) setSuggestion(s string) {
	if s == e.suggestion {
		return
	}
	e.suggestion = s
	if s != "" {
		e.SetPlaceHolder(s + "   (Tab to use)")
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
	if e.onKey != nil && e.onKey(k) {
		return
	}
	if k.Name == fyne.KeyTab && e.AcceptsTab() {
		e.SetText(e.suggestion)
		e.CursorRow = strings.Count(e.Text, "\n")
		e.CursorColumn = len([]rune(e.Text[strings.LastIndex(e.Text, "\n")+1:]))
		e.Refresh()
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
