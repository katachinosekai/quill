package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type sortRules struct {
	ActiveMinutes int  `json:"activeMinutes"`
	ArchiveDays   int  `json:"archiveDays"`
	ClaudeFolder  bool `json:"claudeFolder"`
}

func defaultRules() sortRules {
	return sortRules{ActiveMinutes: 30, ArchiveDays: 7, ClaudeFolder: true}
}

func (r sortRules) activeFor() time.Duration {
	return time.Duration(max(r.ActiveMinutes, 1)) * time.Minute
}

func (r sortRules) archiveAfter() time.Duration {
	return time.Duration(max(r.ArchiveDays, 1)) * 24 * time.Hour
}

func (d *desk) loadRules() {
	d.rules = defaultRules()
	if b := d.app.Preferences().String("sortRules"); b != "" {
		_ = json.Unmarshal([]byte(b), &d.rules)
	}
}

func (d *desk) setRules(r sortRules) {
	d.rules = r
	if b, err := json.Marshal(r); err == nil {
		d.app.Preferences().SetString("sortRules", string(b))
	}
	d.rebuildRows()
}

func (d *desk) rulesDialog() {
	r := d.rules
	activeE := widget.NewEntry()
	activeE.SetText(strconv.Itoa(r.ActiveMinutes))
	archiveE := widget.NewEntry()
	archiveE.SetText(strconv.Itoa(r.ArchiveDays))
	claudeChk := widget.NewCheck("Put sessions started by tools in Created by Claude", nil)
	claudeChk.Checked = r.ClaudeFolder
	explain := widget.NewLabel("Active: open, running, in a terminal, or used in the last N minutes.\n" +
		"Inactive: everything else used in the last N days.\n" +
		"Archive: not used for N days or more.\n" +
		"Sessions you move by hand or drag into a folder stay there.")
	explain.Wrapping = fyne.TextWrapWord
	explain.SizeName = theme.SizeNameCaptionText
	form := widget.NewForm(
		widget.NewFormItem("Active for (minutes)", activeE),
		widget.NewFormItem("Archive after (days)", archiveE),
	)
	reset := widget.NewButton("Reset to defaults", func() {
		def := defaultRules()
		activeE.SetText(strconv.Itoa(def.ActiveMinutes))
		archiveE.SetText(strconv.Itoa(def.ArchiveDays))
		claudeChk.SetChecked(def.ClaudeFolder)
	})
	reset.Importance = widget.LowImportance
	body := container.NewVBox(form, claudeChk, explain, container.NewHBox(reset))
	dlg := dialog.NewCustomConfirm("Sorting rules", "Save", "Cancel", body, func(ok bool) {
		if !ok {
			return
		}
		a, err1 := strconv.Atoi(strings.TrimSpace(activeE.Text))
		b, err2 := strconv.Atoi(strings.TrimSpace(archiveE.Text))
		if err1 != nil || err2 != nil || a < 1 || b < 1 {
			dialog.ShowError(fmt.Errorf("enter whole numbers of 1 or more"), d.win)
			return
		}
		d.setRules(sortRules{ActiveMinutes: a, ArchiveDays: b, ClaudeFolder: claudeChk.Checked})
	}, d.win)
	dlg.Resize(fyne.NewSize(520, 360))
	dlg.Show()
}
