package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"time"
)

const (
	layoutHorizontal = "horizontal"
	layoutVertical   = "vertical"
	labelLandscape   = "Landscape"
	labelPortrait    = "Portrait"
)

func (d *desk) layoutMode() string {
	if d.app.Preferences().StringWithFallback("tabLayout", layoutHorizontal) == layoutVertical {
		return layoutVertical
	}
	return layoutHorizontal
}

func (d *desk) layoutButtons() fyne.CanvasObject {
	d.layoutBtn = widget.NewButton(labelLandscape, func() {
		if d.layoutMode() == layoutVertical {
			d.setLayoutMode(layoutHorizontal)
		} else {
			d.setLayoutMode(layoutVertical)
		}
	})
	d.layoutBtn.Importance = widget.MediumImportance
	d.settingsBtn = widget.NewButtonWithIcon("Settings", theme.SettingsIcon(), d.showSettings)
	d.settingsBtn.Importance = widget.MediumImportance
	// Settings · Quill · kmem: the assistants open in the box (assistbox.go).
	d.kmemBtn = widget.NewButtonWithIcon("kmem", theme.StorageIcon(), func() { d.toggleAssistBox(boxKmem) })
	d.pcsBtn = d.pcsButton()
	d.refreshPCsButton()
	d.kmemBtn.Importance = widget.MediumImportance
	d.kmemBtnBox = d.kmemButtonBox()
	d.quillBtnBox = d.quillButton()
	d.attachQuillBox()
	d.syncAssistButtons()
	return d.layoutBtn
}

func (d *desk) setLayoutMode(mode string) {
	if d.animate && !d.reduceMotion() {
		glideUntil = time.Now().Add(glideWindow)
	}
	d.app.Preferences().SetString("tabLayout", mode)
	d.applyLayoutMode()
}

func (d *desk) applyLayoutMode() {
	mode := d.layoutMode()
	if mode == layoutVertical {
		d.layoutBtn.SetText(labelPortrait)
	} else {
		d.layoutBtn.SetText(labelLandscape)
	}
	if mode == layoutVertical {
		d.bookRow.Hide()
		d.tabRow.Hide()
		d.usageRow.Hide()
		d.sideToggle.Show()
		d.settingsSlot.Objects = []fyne.CanvasObject{d.settingsBox}
		d.settingsSlot.Refresh()
		if d.sideOff {
			d.setSidebar(true)
		}
	} else {
		d.bookRow.Hide()
		d.tabRow.Show()
		d.usageRow.Show()
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
	if d.usageBox != nil {
		relayout(d.usageBox)
	}
	d.syncRowCaptions()
}

func (d *desk) settingsCwd() string {
	if d.current != nil && d.current.cwd != "" {
		return d.current.cwd
	}
	return homeDir()
}

func (d *desk) toolsPane() fyne.CanvasObject {
	heading := func(t string) fyne.CanvasObject {
		l := widget.NewLabel(t)
		l.TextStyle = fyne.TextStyle{Bold: true}
		return l
	}
	var mcp fyne.CanvasObject = widget.NewLabel("Open a session to see its MCP servers.")
	if d.current != nil {
		mcp = d.mcpPane(d.current)
	}
	return container.NewVScroll(container.NewVBox(heading("MCP servers"), mcp, widget.NewSeparator(),
		heading("Subagents"), d.agentsPane(d.current, d.settingsCwd()), widget.NewSeparator(), heading("Hooks"), d.hooksPane(d.settingsCwd())))
}

func (d *desk) showSettings() {
	d.loadPolicyRow()
	d.syncNewSessionSetting()
	tabs := container.NewAppTabs(
		container.NewTabItem("General", container.NewVScroll(d.setGeneral)),
		container.NewTabItem("Setup", d.setupPane()),
		container.NewTabItem("Sessions", container.NewVScroll(d.setSessions)),
		container.NewTabItem("Permissions", d.permissionsPane(d.settingsCwd())),
		container.NewTabItem("Tools", d.toolsPane()),
		container.NewTabItem("Memory", container.NewVScroll(d.memoryPane(d.current, d.settingsCwd()))),
	)
	d.settingsDlg = dialog.NewCustom("Settings", "Close", tabs, d.win)
	d.settingsDlg.SetOnClosed(func() { d.settingsDlg = nil })
	d.settingsDlg.Resize(fyne.NewSize(820, 580))
	d.settingsDlg.Show()
}

func (d *desk) closeSettings() {
	if d.settingsDlg != nil {
		dlg := d.settingsDlg
		d.settingsDlg = nil
		dlg.Hide()
	}
}

func (d *desk) viewToolbar() fyne.CanvasObject {
	d.toolbarRow = container.NewHBox(d.settingsBtn, d.quillBtnBox, d.kmemBtnBox, d.pcsBtn, layout.NewSpacer(), d.splitButtons())
	return d.toolbarRow
}

func (d *desk) placeToolbar() {
	if d.toolbarRow == nil {
		return
	}
	for _, p := range d.panes {
		if p.slot != nil {
			p.slot.Objects = nil
			p.slot.Refresh()
		}
	}
	if !d.split() {
		d.toolbarRow.Objects = []fyne.CanvasObject{d.settingsBtn, d.quillBtnBox, d.kmemBtnBox, d.pcsBtn, layout.NewSpacer(), d.viewBox}
		d.toolbarRow.Refresh()
		if d.toolbarBox != nil {
			d.toolbarBox.Show()
		}
		return
	}
	d.toolbarRow.Objects = nil
	d.toolbarRow.Refresh()
	if d.toolbarBox != nil {
		d.toolbarBox.Hide()
	}
	first, last := d.panes[0], d.panes[len(d.panes)-1]
	first.slot.Objects = []fyne.CanvasObject{d.settingsBtn, d.quillBtnBox, d.kmemBtnBox, d.pcsBtn}
	last.slot.Objects = append(last.slot.Objects, layout.NewSpacer(), d.viewBox)
	if first == last {
		first.slot.Objects = []fyne.CanvasObject{d.settingsBtn, d.quillBtnBox, d.kmemBtnBox, d.pcsBtn, layout.NewSpacer(), d.viewBox}
	}
	first.slot.Layout = layout.NewHBoxLayout()
	last.slot.Layout = layout.NewHBoxLayout()
	first.slot.Refresh()
	last.slot.Refresh()
}
