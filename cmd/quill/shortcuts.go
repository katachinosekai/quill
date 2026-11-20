package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

var newTabShortcut = &desktop.CustomShortcut{KeyName: fyne.KeyT, Modifier: fyne.KeyModifierShortcutDefault}

func isNewTab(s fyne.Shortcut) bool {
	c, ok := s.(*desktop.CustomShortcut)
	return ok && c.KeyName == fyne.KeyT && c.Modifier == fyne.KeyModifierShortcutDefault
}

func (d *desk) setupShortcuts() {
	d.win.Canvas().AddShortcut(newTabShortcut, func(fyne.Shortcut) { d.newSessionDialog() })
	d.input.onShortcut = func(s fyne.Shortcut) bool {
		if isNewTab(s) {
			d.newSessionDialog()
			return true
		}
		return false
	}
}
