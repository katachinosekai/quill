package main

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Bar menus (user 2026-11-22): every button under the message box opens a
// menu above itself, like the Online button: Model, Mode, Voice, Tasks and
// Chips (Show chips / Open chips / Collapse chips, replacing the two chip
// buttons). The model and mode Selects stay as the hidden state holders, so
// picking from the menu goes through their OnChanged as before.

// menuAbove opens items as a pop-up menu above b (below it when there is no
// room above).
func menuAbove(b fyne.CanvasObject, items []*fyne.MenuItem) *widget.PopUpMenu {
	c := fyne.CurrentApp().Driver().CanvasForObject(b)
	if c == nil {
		return nil
	}
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(b)
	m := widget.NewPopUpMenu(fyne.NewMenu("", items...), c)
	y := pos.Y - m.MinSize().Height
	if y < 0 {
		y = pos.Y + b.Size().Height
	}
	m.ShowAtPosition(fyne.NewPos(pos.X, y))
	return m
}

// pickItems: one checked menu item per option; picking one calls pick.
func pickItems(opts []string, current string, pick func(string)) []*fyne.MenuItem {
	var out []*fyne.MenuItem
	for _, o := range opts {
		o := o
		it := fyne.NewMenuItem(o, func() { pick(o) })
		it.Checked = o == current
		out = append(out, it)
	}
	return out
}

func (d *desk) modelItems(s *deskSession) []*fyne.MenuItem {
	return pickItems(d.modelOptions(s), s.model, func(v string) { d.modelSel.SetSelected(v) })
}

func (d *desk) modeItems(s *deskSession) []*fyne.MenuItem {
	return pickItems(modeOptions(s), s.mode, func(v string) { d.modeSel.SetSelected(v) })
}

func (d *desk) chipItems() []*fyne.MenuItem {
	show := fyne.NewMenuItem("Show chips", func() { d.setShowChips(d.hideChips) })
	show.Checked = !d.hideChips
	open := fyne.NewMenuItem("Open chips", func() { d.setAllChips(true) })
	open.Icon = theme.MenuDropDownIcon()
	collapse := fyne.NewMenuItem("Collapse chips", func() { d.setAllChips(false) })
	collapse.Icon = theme.MenuDropUpIcon()
	startCollapsed := fyne.NewMenuItem("New chips start collapsed", func() { d.setCollapseChips(!d.collapseChips) })
	startCollapsed.Checked = d.collapseChips
	return []*fyne.MenuItem{show, open, collapse, fyne.NewMenuItemSeparator(), startCollapsed}
}

// setShowChips shows or hides the tool chips without opening or closing
// them, and redraws the chats.
func (d *desk) setShowChips(show bool) {
	if d.hideChips == !show {
		return
	}
	d.hideChips = !show
	d.app.Preferences().SetBool("hideChips", d.hideChips)
	d.redrawChats()
}

func (d *desk) voiceItems() []*fyne.MenuItem {
	sp := d.stt
	name := strings.ToUpper(sp.lang())
	for _, l := range languages {
		if l.code == sp.lang() {
			name = l.name + " (" + l.code + ")"
		}
	}
	lang := fyne.NewMenuItem("Language: "+name+"...", func() {
		d.pickLanguage(sp.lang(), func(code string) { sp.setLang(code) })
	})
	on := fyne.NewMenuItem("Voice input", func() {
		d.app.Preferences().SetBool(prefSTTOff, true)
		sp.syncOff()
	})
	on.Checked = true // the button only shows while voice input is on
	return []*fyne.MenuItem{lang, fyne.NewMenuItemSeparator(), on}
}

const stopEverythingLabel = "Stop everything (reply and background tasks)"

// taskItems: the "Show background tasks..." entry and, when it applies, Stop
// everything; used by the subagent strip's context menu and the session/tab
// menu.
func (d *desk) taskItems(s *deskSession) []*fyne.MenuItem {
	out := []*fyne.MenuItem{fyne.NewMenuItem("Show background tasks...", func() { d.cmdTasks(s) })}
	// Stop keeps background subagents running; this is the way to end them all.
	if len(s.conv.BackgroundTasks()) > 0 && s.remote == nil && s.running() {
		out = append(out, fyne.NewMenuItem(stopEverythingLabel, func() { d.interruptAll(s) }))
	}
	return out
}

// menuButton: a bar button that opens items() above itself.
func menuButton(text string, icon fyne.Resource, items func() []*fyne.MenuItem) *widget.Button {
	var b *widget.Button
	b = widget.NewButtonWithIcon(text, icon, func() {
		if it := items(); len(it) > 0 {
			menuAbove(b, it)
		}
	})
	b.Importance = widget.MediumImportance
	return b
}

// syncBarButtons shows the current model and mode on their buttons.
func (d *desk) syncBarButtons(s *deskSession) bool {
	changed := false
	set := func(b *widget.Button, text string) {
		if b != nil && b.Text != text {
			b.SetText(text)
			changed = true
		}
	}
	if s != nil {
		set(d.modelBtn, s.model)
		set(d.modeBtn, s.mode)
	}
	return changed
}
