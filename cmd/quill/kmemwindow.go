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
// side is the chat of the kmem operator, kmem-lock-audit, which the assistant
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

// kmemWindowOpenHook runs after the window is shown (the assistant starts
// there); kmemWindowCloseHook runs after d.kmemWin is cleared, while w still
// holds the session and view (the assistant saves its handoff there). Both
// run on the UI thread.
var kmemWindowOpenHook, kmemWindowCloseHook func(d *desk, w *kmemWindow)

// kmemChatOffReason is the right side until the assistant puts its chat there.
const kmemChatOffReason = "The kmem assistant is off; the panels on the left work without it."

// openKmemWindow opens the kmem window, or raises it when it is open.
func (d *desk) openKmemWindow() {
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
	w := &kmemWindow{d: d, win: d.app.NewWindow(kmemWinTitle)}
	w.panel = d.newKmemPanel(w)
	w.right = container.NewStack()
	w.split = container.NewHSplit(w.panel.tabs, w.right)
	off := prefs.FloatWithFallback("kmemWinSplit", kmemWinDefaultSplit)
	if off < 0.1 || off > 0.9 {
		off = kmemWinDefaultSplit
	}
	w.split.Offset = off
	w.win.SetContent(w.split)
	w.win.Resize(fyne.NewSize(
		float32(prefs.FloatWithFallback("kmemWinW", kmemWinDefaultW)),
		float32(prefs.FloatWithFallback("kmemWinH", kmemWinDefaultH))))
	w.setupKeys()
	w.win.SetCloseIntercept(w.close)
	d.kmemWin = w
	w.setChatReason(kmemChatOffReason)
	w.win.Show()
	w.win.RequestFocus()
	w.panel.reload()
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
	if sz := w.win.Canvas().Size(); sz.Width > 100 && sz.Height > 100 {
		prefs.SetFloat("kmemWinW", float64(sz.Width))
		prefs.SetFloat("kmemWinH", float64(sz.Height))
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
