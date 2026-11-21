package main

import (
	"fmt"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

// entryCursor finds the text cursor rectangle drawn inside a Fyne entry.
func entryCursor(e fyne.Widget) *canvas.Rectangle {
	var cur *canvas.Rectangle
	border := theme.InputBorderSize()
	walkObjects([]fyne.CanvasObject{e}, func(o fyne.CanvasObject) {
		if r, ok := o.(*canvas.Rectangle); ok && r.Size().Width == border && r.Size().Height > 0 {
			cur = r
		}
	})
	return cur
}

func TestMessageBoxCursorFollowsResize(t *testing.T) {
	a := test.NewTempApp(t)
	_ = a
	e := newSendEntry(func(string) {})
	w := test.NewTempWindow(t, container.NewWithoutLayout(e))
	w.Resize(fyne.NewSize(1200, 400))
	e.Resize(fyne.NewSize(900, 120))
	w.Canvas().Focus(e)
	text := "the quick brown fox jumps over the lazy dog and keeps running across the field until the sun goes down behind the hills"
	e.SetText(text)
	e.setCursor(len([]rune(text)))
	check := func(when string) {
		t.Helper()
		cur := entryCursor(e)
		if cur == nil {
			t.Fatal("no cursor drawn")
		}
		if got, want := cur.Position(), e.CursorPosition(); got != want {
			t.Fatalf("%s: cursor drawn at %v, but the text position is %v", when, got, want)
		}
	}
	check("before resize")
	for _, width := range []float32{420, 260, 700, 900} {
		e.Resize(fyne.NewSize(width, 120))
		check("after resizing to " + fmtF(width))
	}
}

func fmtF(f float32) string { return fmt.Sprint(f) }

func TestDeskMessageBoxCursorAfterWindowResize(t *testing.T) {
	h := newHarness(t)
	text := "the quick brown fox jumps over the lazy dog and keeps running across the field until the sun goes down behind the hills, then it rests"
	h.ui(func() {
		h.d.animate = true
		h.d.setShare("inputShare", 0.6)
		h.d.root.Resize(fyne.NewSize(1400, 800))
		h.w.Canvas().Focus(h.d.input)
		h.d.input.SetText(text)
		h.d.input.setCursor(len([]rune(text)))
	})
	sizes := []fyne.Size{fyne.NewSize(900, 700), fyne.NewSize(700, 700), fyne.NewSize(2400, 900), fyne.NewSize(1100, 800)}
	for _, sz := range sizes {
		h.ui(func() { h.d.root.Resize(sz) })
		h.eventually("box settles at "+fmtF(sz.Width), func() bool {
			w := h.d.bottomBox.Objects[0].Size().Width
			want := h.d.inputWidth()
			return h.d.resizeFreeze == 0 && w >= want-1 && w <= want+1
		})
		h.ui(func() {
			cur := entryCursor(h.d.input)
			if cur == nil {
				t.Fatal("no cursor drawn")
			}
			if got, want := cur.Position(), h.d.input.CursorPosition(); got != want {
				t.Fatalf("window %v: cursor drawn at %v, text position %v (entry %v)", sz, got, want, h.d.input.Size())
			}
		})
	}
}
