package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type maxWidthLayout struct{ max float32 }

func (l maxWidthLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var s fyne.Size
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		s = s.Max(o.MinSize())
	}
	if s.Width > l.max {
		s.Width = l.max
	}
	return s
}

func (l maxWidthLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	w := size.Width
	if w > l.max {
		w = l.max
	}
	x := (size.Width - w) / 2
	for _, o := range objs {
		h := size.Height
		o.Move(fyne.NewPos(x, 0))
		o.Resize(fyne.NewSize(w, h))
	}
}

func centered(max float32, obj fyne.CanvasObject) *fyne.Container {
	return container.New(maxWidthLayout{max: max}, obj)
}

type sendEntry struct {
	widget.Entry
	shift  bool
	onSend func(string)
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
	e.Entry.KeyDown(k)
}

func (e *sendEntry) KeyUp(k *fyne.KeyEvent) {
	if k.Name == desktop.KeyShiftLeft || k.Name == desktop.KeyShiftRight {
		e.shift = false
	}
	e.Entry.KeyUp(k)
}

func (e *sendEntry) TypedKey(k *fyne.KeyEvent) {
	if (k.Name == fyne.KeyReturn || k.Name == fyne.KeyEnter) && !e.shift {
		if e.Disabled() {
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
