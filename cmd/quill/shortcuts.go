package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

var newTabShortcut = &desktop.CustomShortcut{KeyName: fyne.KeyT, Modifier: fyne.KeyModifierShortcutDefault}

// The assistant box (assistbox.go, §3.6): Ctrl+Shift+O opens or closes the
// Quill operator, Ctrl+Shift+K kmem.
var (
	quillBoxShortcut = &desktop.CustomShortcut{KeyName: fyne.KeyO, Modifier: fyne.KeyModifierShortcutDefault | fyne.KeyModifierShift}
	kmemBoxShortcut  = &desktop.CustomShortcut{KeyName: fyne.KeyK, Modifier: fyne.KeyModifierShortcutDefault | fyne.KeyModifierShift}
)

// boxShortcut: the role a shortcut opens or closes, "" if none.
func boxShortcut(s fyne.Shortcut) string {
	c, ok := s.(*desktop.CustomShortcut)
	if !ok || c.Modifier != fyne.KeyModifierShortcutDefault|fyne.KeyModifierShift {
		return ""
	}
	switch c.KeyName {
	case fyne.KeyO:
		return boxQuill
	case fyne.KeyK:
		return boxKmem
	}
	return ""
}

var tabKeys = []fyne.KeyName{fyne.Key1, fyne.Key2, fyne.Key3, fyne.Key4, fyne.Key5, fyne.Key6, fyne.Key7, fyne.Key8, fyne.Key9}

func tabNumber(s fyne.Shortcut) int {
	c, ok := s.(*desktop.CustomShortcut)
	if !ok || c.Modifier != fyne.KeyModifierShortcutDefault {
		return 0
	}
	for i, k := range tabKeys {
		if c.KeyName == k {
			return i + 1
		}
	}
	return 0
}

func (d *desk) handleShortcut(s fyne.Shortcut) bool {
	if role := boxShortcut(s); role != "" {
		d.toggleAssistBox(role)
		return true
	}
	if isNewTab(s) {
		d.newTabKey()
		return true
	}
	if n := tabNumber(s); n > 0 {
		d.tabNumberKey(n)
		return true
	}
	return false
}

// newTabKey / tabNumberKey: Ctrl+T and Ctrl+1-9 close the box's overlay
// first, then switch (Q16).
func (d *desk) newTabKey() {
	d.closeAssistOverlay()
	d.newSessionDialog()
}

func (d *desk) tabNumberKey(n int) {
	d.closeAssistOverlay()
	d.openTabNumber(n)
}

func isNewTab(s fyne.Shortcut) bool {
	c, ok := s.(*desktop.CustomShortcut)
	return ok && c.KeyName == fyne.KeyT && c.Modifier == fyne.KeyModifierShortcutDefault
}

func (d *desk) copySelection() bool {
	if v := d.viewFor(d.current); v != nil && v.sel != nil && v.sel.copy() {
		return true
	}
	for _, v := range d.allViews() {
		if v.sel != nil && v.sel.copy() {
			return true
		}
	}
	return false
}

func (d *desk) clearSelections() {
	for _, v := range d.allViews() {
		if v.sel != nil {
			v.sel.clear()
		}
	}
}

func (d *desk) setupShortcuts() {
	d.win.Canvas().AddShortcut(&fyne.ShortcutCopy{}, func(fyne.Shortcut) { d.copySelection() })
	d.win.Canvas().AddShortcut(&fyne.ShortcutSelectAll{}, func(fyne.Shortcut) {
		if v := d.viewFor(d.current); v != nil && v.sel != nil {
			v.sel.selectAll()
		}
	})
	d.win.Canvas().SetOnTypedKey(func(k *fyne.KeyEvent) {
		if k.Name == fyne.KeyEscape {
			d.canvasEscape()
		}
	})
	d.win.Canvas().AddShortcut(newTabShortcut, func(fyne.Shortcut) { d.newTabKey() })
	for i, k := range tabKeys {
		n := i + 1
		d.win.Canvas().AddShortcut(&desktop.CustomShortcut{KeyName: k, Modifier: fyne.KeyModifierShortcutDefault}, func(fyne.Shortcut) { d.tabNumberKey(n) })
	}
	d.win.Canvas().AddShortcut(quillBoxShortcut, func(fyne.Shortcut) { d.toggleAssistBox(boxQuill) })
	d.win.Canvas().AddShortcut(kmemBoxShortcut, func(fyne.Shortcut) { d.toggleAssistBox(boxKmem) })
	d.input.onShortcut = d.handleShortcut
}

func (d *desk) escape(s *deskSession) bool {
	if d.boxFocused() {
		return d.assistBoxEscape() // never interrupts the current tab from the box
	}
	if d.escapeOverlay() {
		return true
	}
	for _, v := range d.allViews() {
		if v.sel != nil && v.sel.has {
			d.clearSelections()
			return true
		}
	}
	if s != nil && s.running() && (s.conv.Busy || s.proc.Busy() || hasPendingPlan(s)) {
		d.interrupt(s)
		return true
	}
	return false
}
