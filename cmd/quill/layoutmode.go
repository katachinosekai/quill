package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	layoutHorizontal = "horizontal"
	layoutVertical   = "vertical"
)

func (d *desk) layoutMode() string {
	if d.app.Preferences().StringWithFallback("tabLayout", layoutHorizontal) == layoutVertical {
		return layoutVertical
	}
	return layoutHorizontal
}

func (d *desk) layoutButtons() fyne.CanvasObject {
	d.layoutSel = widget.NewRadioGroup([]string{"Horizontal", "Vertical"}, func(v string) {
		if v == "" || d.suppressLayout {
			return
		}
		mode := layoutHorizontal
		if v == "Vertical" {
			mode = layoutVertical
		}
		d.setLayoutMode(mode)
	})
	d.layoutSel.Horizontal = true
	d.layoutSel.Required = true
	d.settingsBtn = widget.NewButtonWithIcon("Settings", theme.SettingsIcon(), d.showSettings)
	d.settingsBtn.Importance = widget.LowImportance
	return container.NewHBox(container.NewCenter(mutedText("Tabs", 11)), d.layoutSel, d.settingsBtn)
}

func (d *desk) setLayoutMode(mode string) {
	d.app.Preferences().SetString("tabLayout", mode)
	d.applyLayoutMode()
}

func (d *desk) applyLayoutMode() {
	mode := d.layoutMode()
	d.suppressLayout = true
	if mode == layoutVertical {
		d.layoutSel.SetSelected("Vertical")
	} else {
		d.layoutSel.SetSelected("Horizontal")
	}
	d.suppressLayout = false
	if mode == layoutVertical {
		d.bookRow.Hide()
		d.tabRow.Hide()
		d.settingsBtn.Hide()
		d.sideToggle.Show()
		if d.settingsPop != nil {
			d.settingsPop.Hide()
		}
		d.settingsSlot.Objects = []fyne.CanvasObject{d.settingsBox}
		d.settingsSlot.Refresh()
		if d.sideOff {
			d.setSidebar(true)
		}
	} else {
		d.bookRow.Show()
		d.tabRow.Show()
		d.settingsBtn.Show()
		d.sideToggle.Hide()
		d.settingsSlot.Objects = nil
		d.settingsSlot.Refresh()
		if !d.sideOff {
			d.setSidebar(false)
		}
		d.syncBookmarks()
		d.syncTabs()
	}
	if d.headerBox != nil {
		d.headerBox.Refresh()
	}
}

func (d *desk) showSettings() {
	if d.settingsPop == nil {
		d.settingsPop = widget.NewPopUp(container.NewPadded(d.settingsBox), d.win.Canvas())
	} else {
		d.settingsPop.Content = container.NewPadded(d.settingsBox)
	}
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(d.settingsBtn)
	size := d.settingsBox.MinSize()
	x := min(pos.X, d.win.Canvas().Size().Width-size.Width-24)
	d.settingsPop.ShowAtPosition(fyne.NewPos(max(8, x), pos.Y+d.settingsBtn.Size().Height))
}
