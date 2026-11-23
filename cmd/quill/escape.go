package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type escEntry struct {
	widget.Entry
	onEsc func()
}

func (e *escEntry) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyEscape && e.onEsc != nil {
		e.onEsc()
		return
	}
	e.Entry.TypedKey(k)
}

func (d *desk) escClose(o fyne.CanvasObject, closeFn func()) {
	if d.escClosers == nil {
		d.escClosers = map[fyne.CanvasObject]func(){}
	}
	for k := range d.escClosers {
		if !d.overlayShown(k) {
			delete(d.escClosers, k)
		}
	}
	d.escClosers[o] = closeFn
}

func (d *desk) overlayShown(o fyne.CanvasObject) bool {
	for _, x := range d.win.Canvas().Overlays().List() {
		if widget.OverlayContent(x) == o {
			return true
		}
	}
	return false
}

func (d *desk) escapeOverlay() bool {
	top := d.win.Canvas().Overlays().Top()
	if top == nil {
		return false
	}
	top = widget.OverlayContent(top)
	if f := d.escClosers[top]; f != nil {
		delete(d.escClosers, top)
		f()
		return true
	}
	if m, ok := top.(*widget.PopUpMenu); ok {
		m.Dismiss()
		return true
	}
	dialog.DismissOverlay(top)
	return true
}

// canvasEscape: Escape with nothing focused. The box's overlay (or the box
// holding the focus) handles it first: a popup, its own selection, then it
// closes; it never interrupts the assistant or the current tab then
// (§3.6). Otherwise today's order for the current tab.
func (d *desk) canvasEscape() bool {
	if d.assistOverlayOpen() || d.boxFocused() {
		return d.assistBoxEscape()
	}
	return d.escape(d.current)
}
