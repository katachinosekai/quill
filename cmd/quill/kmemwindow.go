package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// The kmem operator opens to the assistant, with utility pages in a sidebar.
// The same content can move from the overlay into a separate window.

// kmemWinOperator is the kmem name of the window's assistant.
const kmemWinOperator = "kmem-lock-audit"

const (
	kmemWinTitle    = "kmem — Quill Desktop"
	kmemWinDefaultW = 1280
	kmemWinDefaultH = 800
)

// kmemWindowOpenHook runs after the window is shown (the running assistant's
// chat goes there, or it starts); kmemWindowCloseHook runs after d.kmemWin is
// cleared (closing only hides the assistant: it keeps running). Both run on
// the UI thread.
var kmemWindowOpenHook, kmemWindowCloseHook func(d *desk, w *kmemWindow)

// kmemChatOffReason is the right side until the assistant puts its chat there.
const kmemChatOffReason = "The kmem assistant is off. You can still use the utility pages."

// showKmem is the one entry for the kmem button, the menu and
// `quill --kmem-window`: the kmem box (assistbox.go; its Pop out button
// opens the window, and a popped-out window is raised instead).
func (d *desk) showKmem() {
	d.openAssistBox(boxKmem)
	if w := d.kmemWin; w != nil && !w.closed && w.win == nil {
		w.panel.tabs.SelectIndex(kmemTabAssistant)
	}
}

// openKmemAssistant brings the chat to the front for a notification.
func (d *desk) openKmemAssistant() {
	d.showKmem()
	if w := d.kmemWin; w != nil && !w.closed && w.panel != nil {
		w.panel.tabs.SelectIndex(kmemTabAssistant)
	}
}

// kmemBoxContent keeps the assistant and utility pages mounted in one
// navigation container so moving it into a window preserves the current page.
func (d *desk) kmemBoxContent() fyne.CanvasObject {
	w := d.kmemWin
	if w == nil || w.panel == nil || w.closed {
		w = &kmemWindow{d: d, right: container.NewStack()}
		w.panel = d.newKmemPanel(w)
		d.kmemWin = w
		w.setChatReason(kmemChatOffReason)
		if d.app.Preferences().Bool("kmemAgentCodex") {
			w.showAgent(true)
		}
		w.panel.reload()
	}
	return w.panel.tabs
}

func (w *kmemWindow) assistantSelected() bool {
	return w != nil && !w.closed && (w.panel == nil || w.panel.tabs.SelectedIndex() == kmemTabAssistant)
}

// popOutKmem shows the box content in its own window (raises it when open).
func (d *desk) popOutKmem() {
	if w := d.kmemWin; w != nil && w.win != nil && !w.closed {
		w.win.Show()
		w.win.RequestFocus()
		return
	}
	if !d.kmemAvailable() {
		d.showClaudeMemory() // no kmem: the Memory button (kmemoptional.go)
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

// close remembers the size, clears d.kmemWin and calls the close hook.
func (w *kmemWindow) close() {
	if w == nil || w.closed {
		return
	}
	w.d.cancelQueueEdit(w.selectedAgent().s)
	w.closed = true
	d := w.d
	if w.saveGeom != nil {
		w.saveGeom()
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
	if w.win != nil { // nil while the content shows in the kmem box
		w.win.Close()
	}
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
		if w.assistantSelected() {
			if v := w.selectedAgent().view; v != nil && v.sel != nil {
				v.sel.copy()
			}
		}
	})
	c.AddShortcut(&fyne.ShortcutSelectAll{}, func(fyne.Shortcut) {
		if w.assistantSelected() {
			if v := w.selectedAgent().view; v != nil && v.sel != nil {
				v.sel.selectAll()
			}
		}
	})
	c.SetOnTypedKey(func(k *fyne.KeyEvent) {
		if k.Name == fyne.KeyEscape {
			w.escape()
		}
	})
}

func (d *desk) kmemComposerEscape() bool {
	if w := d.kmemWin; w != nil && w.win != nil && !w.closed {
		w.escape()
		return true
	}
	return d.assistBoxEscape()
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
	if !w.assistantSelected() {
		w.close()
		return
	}
	a := w.selectedAgent()
	if v := a.view; v != nil && v.sel != nil && v.sel.has {
		v.sel.clear()
		return
	}
	if w.d.cancelQueueEdit(a.s) {
		return
	}
	if a.s != nil && len(a.s.conv.PendingPermissions()) > 0 {
		w.d.interrupt(a.s)
		return
	}
	w.close()
}
