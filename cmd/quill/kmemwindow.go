package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// The kmem operations window (docs/design/kmem-window.md §3, user
// 2026-11-22): a second fyne window opened by the toolbar's kmem button. The
// left side is the kmem panel (Overview, Handoffs, Tasks, Memory); the right
// side is the chat of the kmem assistant, kmem-lock-audit, which the assistant
// lifecycle (kmemassist.go) puts there through setChat, or a one-line reason
// through setChatReason when the window runs panels-only. Wayland allows no
// positioning, so only the size and the split are remembered.

// kmemWinOperator is the kmem name of the window's assistant.
const kmemWinOperator = "kmem-lock-audit"

const (
	kmemWinTitle        = "kmem — Quill Desktop"
	kmemWinDefaultW     = 1280
	kmemWinDefaultH     = 800
	kmemWinDefaultSplit = 0.45
)

// kmemWindowOpenHook runs after the window is shown (the running assistant's
// chat goes there, or it starts); kmemWindowCloseHook runs after d.kmemWin is
// cleared (closing only hides the assistant: it keeps running). Both run on
// the UI thread.
var kmemWindowOpenHook, kmemWindowCloseHook func(d *desk, w *kmemWindow)

// kmemChatOffReason is the right side until the assistant puts its chat there.
const kmemChatOffReason = "The kmem assistant is off; the panels on the left work without it."

// showKmem is the one entry for the kmem button, the menu and
// `quill --kmem-window`. The kmem box will route here; until then it opens
// the pop-out window (the old kmem window).
func (d *desk) showKmem() { d.popOutKmem() }

// kmemBoxContent is the kmem box: the kmem panels as tabs on the left rail
// and the assistant's chat (own message box and status line, put there by
// setChat) on the right. It is built once; asking again returns the same
// object, so a Pop out keeps the selected tab and the scroll positions.
func (d *desk) kmemBoxContent() fyne.CanvasObject {
	w := d.kmemWin
	if w == nil || w.split == nil || w.closed {
		w = &kmemWindow{d: d}
		w.panel = d.newKmemPanel(w)
		w.panel.tabs.SetTabLocation(container.TabLocationLeading)
		w.right = container.NewStack()
		w.split = container.NewHSplit(w.panel.tabs, w.right)
		off := d.app.Preferences().FloatWithFallback("kmemWinSplit", kmemWinDefaultSplit)
		if off < 0.1 || off > 0.9 {
			off = kmemWinDefaultSplit
		}
		w.split.Offset = off
		d.kmemWin = w
		w.setChatReason(kmemChatOffReason)
		w.panel.reload()
	}
	return w.split
}

// popOutKmem shows the box content in its own window (raises it when open).
func (d *desk) popOutKmem() {
	if w := d.kmemWin; w != nil && w.win != nil && !w.closed {
		w.win.Show()
		w.win.RequestFocus()
		return
	}
	if !d.kmemAvailable() {
		dialog.ShowInformation("kmem", "kmem is not available on this computer. Set it up in Settings → General → Memory (kmem).", d.win)
		return
	}
	prefs := d.app.Preferences()
	content := d.kmemBoxContent()
	w := d.kmemWin
	w.win = d.app.NewWindow(kmemWinTitle)
	w.win.SetContent(content)
	setAppID(w.win, appIDKmem)
	saved := loadGeom(prefs, geomKmem)
	if !saved.valid() { // sizes saved before the geometry keys existed
		saved.W = float32(prefs.FloatWithFallback("kmemWinW", kmemWinDefaultW))
		saved.H = float32(prefs.FloatWithFallback("kmemWinH", kmemWinDefaultH))
	}
	g := geomOr(nil, saved, winGeom{W: kmemWinDefaultW, H: kmemWinDefaultH})
	applyGeom(w.win, g)
	w.saveGeom = trackGeom(w.win, prefs, geomKmem, g, func() bool { return w.closed })
	w.setupKeys()
	w.win.SetCloseIntercept(w.close)
	w.win.Show()
	w.win.RequestFocus()
	if kmemWindowOpenHook != nil {
		kmemWindowOpenHook(d, w)
	}
}

// close is the one close flow for X, Escape and code: it remembers the size
// and split, clears d.kmemWin, calls the close hook and destroys the window.
func (w *kmemWindow) close() {
	if w == nil || w.closed {
		return
	}
	w.closed = true
	d := w.d
	prefs := d.app.Preferences()
	if w.saveGeom != nil {
		w.saveGeom()
	}
	if w.split != nil {
		prefs.SetFloat("kmemWinSplit", w.split.Offset)
	}
	if d.kmemWin == w {
		d.kmemWin = nil
	}
	if d.kmemPanelNow == w.panel {
		d.kmemPanelNow = nil
	}
	if kmemWindowCloseHook != nil {
		kmemWindowCloseHook(d, w)
	}
	w.win.Close()
}

// parent: the window dialogs opened from the kmem window belong to.
func (w *kmemWindow) parent() fyne.Window {
	if w == nil || w.win == nil || w.closed {
		return nil
	}
	return w.win
}

// setChat puts the assistant's chat (view plus input) on the right side.
func (w *kmemWindow) setChat(obj fyne.CanvasObject) {
	w.right.Objects = []fyne.CanvasObject{obj}
	w.right.Refresh()
}

// setChatReason replaces the chat with one line saying why there is none;
// the panel buttons that fill the chat input hide.
func (w *kmemWindow) setChatReason(reason string) {
	w.setChatReasonWith(reason, "", nil)
}

// setChatReasonWith is setChatReason with one button after the line, e.g.
// "Open it" when kmem-lock-audit is live in a tab.
func (w *kmemWindow) setChatReasonWith(reason, button string, tap func()) {
	line := wrapLabel(reason)
	var obj fyne.CanvasObject = line
	if button != "" && tap != nil {
		b := widget.NewButton(button, tap)
		obj = container.NewVBox(line, container.NewHBox(b))
	}
	w.setChat(container.NewPadded(container.NewVBox(obj)))
	w.setPrefill(nil)
}

// setPrefill sets how the panel's Explain, Suggest split and Ask the
// operator buttons fill the chat input (they never send); nil hides them.
func (w *kmemWindow) setPrefill(f func(text string)) {
	w.prefill = f
	if w.panel != nil {
		w.panel.render()
	}
}

// setupKeys: the window's own Escape, Ctrl+C and Ctrl+A. The main window's
// shortcuts (new tab, tab numbers) are not bound here.
func (w *kmemWindow) setupKeys() {
	c := w.win.Canvas()
	c.AddShortcut(&fyne.ShortcutCopy{}, func(fyne.Shortcut) {
		if v := w.view; v != nil && v.sel != nil {
			v.sel.copy()
		}
	})
	c.AddShortcut(&fyne.ShortcutSelectAll{}, func(fyne.Shortcut) {
		if v := w.view; v != nil && v.sel != nil {
			v.sel.selectAll()
		}
	})
	c.SetOnTypedKey(func(k *fyne.KeyEvent) {
		if k.Name == fyne.KeyEscape {
			w.escape()
		}
	})
}

// escape closes the top overlay, then a selection in the chat, then the
// window (the same close flow as X).
func (w *kmemWindow) escape() {
	if w.closed {
		return
	}
	if top := w.win.Canvas().Overlays().Top(); top != nil {
		top = widget.OverlayContent(top)
		if f := w.d.escClosers[top]; f != nil {
			delete(w.d.escClosers, top)
			f()
			return
		}
		if m, ok := top.(*widget.PopUpMenu); ok {
			m.Dismiss()
			return
		}
		dialog.DismissOverlay(top)
		return
	}
	if v := w.view; v != nil && v.sel != nil && v.sel.has {
		v.sel.clear()
		return
	}
	w.close()
}
