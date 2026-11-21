package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

var newTabShortcut = &desktop.CustomShortcut{KeyName: fyne.KeyT, Modifier: fyne.KeyModifierShortcutDefault}

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
	if isNewTab(s) {
		d.newSessionDialog()
		return true
	}
	if n := tabNumber(s); n > 0 {
		d.openTabNumber(n)
		return true
	}
	return false
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
			d.clearSelections()
		}
	})
	d.win.Canvas().AddShortcut(newTabShortcut, func(fyne.Shortcut) { d.newSessionDialog() })
	for i, k := range tabKeys {
		n := i + 1
		d.win.Canvas().AddShortcut(&desktop.CustomShortcut{KeyName: k, Modifier: fyne.KeyModifierShortcutDefault}, func(fyne.Shortcut) { d.openTabNumber(n) })
	}
	d.input.onShortcut = d.handleShortcut
}
