package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
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
	return container.NewHBox(container.NewCenter(mutedText("Tabs", fontMeta)), d.layoutSel)
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
		d.usageRow.Hide()
		d.sideToggle.Show()
		d.settingsSlot.Objects = []fyne.CanvasObject{d.settingsBox}
		d.settingsSlot.Refresh()
		if d.sideOff {
			d.setSidebar(true)
		}
	} else {
		d.bookRow.Show()
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
	hooks := widget.NewLabel(hooksText(d.settingsCwd()))
	hooks.TextStyle = fyne.TextStyle{Monospace: true}
	hooks.Wrapping = fyne.TextWrapBreak
	return container.NewVScroll(container.NewVBox(heading("MCP servers"), mcp, widget.NewSeparator(),
		heading("Subagents"), d.agentsPane(d.settingsCwd()), widget.NewSeparator(), heading("Hooks"), hooks))
}

func (d *desk) showSettings() {
	tabs := container.NewAppTabs(
		container.NewTabItem("General", container.NewVScroll(d.setGeneral)),
		container.NewTabItem("Sessions", container.NewVScroll(d.setSessions)),
		container.NewTabItem("Permissions", d.permissionsPane(d.settingsCwd())),
		container.NewTabItem("Tools", d.toolsPane()),
		container.NewTabItem("Memory", container.NewVScroll(d.memoryPane(d.settingsCwd()))),
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
	return container.NewHBox(d.settingsBtn, layout.NewSpacer(), d.splitButtons())
}
