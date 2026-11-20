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
	RecentActive  bool `json:"recentActive"`
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
	fixedChk := widget.NewCheck("Keep Active sessions in a fixed order (new ones join at the bottom)", nil)
	fixedChk.Checked = !r.RecentActive
	warnChk := widget.NewCheck("Warn before deleting a session", nil)
	warnChk.Checked = !d.app.Preferences().Bool("skipDeleteWarning")
	explain := widget.NewLabel("Active: open, running, in a terminal, or used in the last N minutes.\n" +
		"Inactive: everything else used in the last N days.\n" +
		"Archive: not used for N days or more.\n" +
		"Sessions move up to Active right away; moves down to Inactive or Archive happen at most every 30 minutes.\n" +
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
		fixedChk.SetChecked(!def.RecentActive)
	})
	reset.Importance = widget.LowImportance
	body := container.NewVBox(form, claudeChk, fixedChk, warnChk, explain, container.NewHBox(reset))
	dlg := dialog.NewCustomConfirm("Format Rules", "Save", "Cancel", body, func(ok bool) {
		if !ok {
			return
		}
		a, err1 := strconv.Atoi(strings.TrimSpace(activeE.Text))
		b, err2 := strconv.Atoi(strings.TrimSpace(archiveE.Text))
		if err1 != nil || err2 != nil || a < 1 || b < 1 {
			dialog.ShowError(fmt.Errorf("enter whole numbers of 1 or more"), d.win)
			return
		}
		d.app.Preferences().SetBool("skipDeleteWarning", !warnChk.Checked)
		d.setRules(sortRules{ActiveMinutes: a, ArchiveDays: b, ClaudeFolder: claudeChk.Checked, RecentActive: !fixedChk.Checked})
		d.resortNow()
	}, d.win)
	dlg.Resize(fyne.NewSize(520, 360))
	dlg.Show()
}

const demoteEvery = 30 * time.Minute

type sortState struct {
	Place  map[string]string `json:"place"`
	At     int64             `json:"at"`
	Active []string          `json:"active"`
}

func placeRank(p string) int {
	switch p {
	case placeActive:
		return 0
	case placeArchive:
		return 2
	}
	return 1
}

func (d *desk) loadSortState() {
	d.sorted = sortState{Place: map[string]string{}}
	if b := d.app.Preferences().String("sortState"); b != "" {
		_ = json.Unmarshal([]byte(b), &d.sorted)
	}
	if d.sorted.Place == nil {
		d.sorted.Place = map[string]string{}
	}
	if b := d.app.Preferences().String("folderOpen"); b != "" {
		_ = json.Unmarshal([]byte(b), &d.folderOpen)
	}
}

func (d *desk) saveSortState() {
	if b, err := json.Marshal(d.sorted); err == nil {
		d.app.Preferences().SetString("sortState", string(b))
	}
}

func (d *desk) demoteDue() bool {
	return time.Since(time.Unix(d.sorted.At, 0)) >= demoteEvery
}

func (d *desk) rulePlace(e sideEntry) string {
	switch {
	case d.isActive(e.id, e.last):
		return placeActive
	case d.isInactive(e.id, e.last):
		return placeArchive
	}
	return placeInactive
}

func (d *desk) stickyPlace(e sideEntry, demote bool) string {
	want := d.rulePlace(e)
	prev, ok := d.sorted.Place[e.id]
	if ok && placeRank(want) > placeRank(prev) && !demote {
		return prev
	}
	if prev != want {
		d.sorted.Place[e.id] = want
		d.sortDirty = true
	}
	return want
}

func (d *desk) resortNow() {
	d.sorted.At = 0
	d.rebuildRows()
}

func (d *desk) fixedActive(es []sideEntry) []sideEntry {
	in := map[string]sideEntry{}
	for _, e := range es {
		in[e.id] = e
	}
	var order []string
	var out []sideEntry
	for _, id := range d.sorted.Active {
		if e, ok := in[id]; ok {
			order = append(order, id)
			out = append(out, e)
			delete(in, id)
		}
	}
	for _, e := range sortRecent(es) {
		if _, ok := in[e.id]; ok {
			order = append(order, e.id)
			out = append(out, e)
		}
	}
	if !equalIDs(order, d.sorted.Active) {
		d.sorted.Active = order
		d.sortDirty = true
	}
	return out
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
