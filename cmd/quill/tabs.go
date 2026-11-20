package main

import (
	"encoding/json"
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	maxTabs       = 12
	tabTitleRunes = 24
	menuMaxHeight = 420
	menuWidth     = 340
)

type bookGroup struct {
	key, title string
	ids        []string
}

func (d *desk) loadTabs() {
	if b := d.app.Preferences().String("tabs"); b != "" {
		_ = json.Unmarshal([]byte(b), &d.tabs)
	}
}

func (d *desk) saveTabs() {
	if b, err := json.Marshal(d.tabs); err == nil {
		d.app.Preferences().SetString("tabs", string(b))
	}
}

func (d *desk) addTab(id string) {
	for _, t := range d.tabs {
		if t == id {
			d.syncTabs()
			return
		}
	}
	d.tabs = append(d.tabs, id)
	if len(d.tabs) > maxTabs {
		d.tabs = d.tabs[len(d.tabs)-maxTabs:]
	}
	d.saveTabs()
	d.syncTabs()
}

func (d *desk) closeTab(id string) {
	idx := -1
	for i, t := range d.tabs {
		if t == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	d.tabs = append(d.tabs[:idx], d.tabs[idx+1:]...)
	d.saveTabs()
	if d.current != nil && d.current.id == id && len(d.tabs) > 0 {
		d.open(d.tabs[min(idx, len(d.tabs)-1)])
	}
	d.syncTabs()
}

func (d *desk) buildTabs() fyne.CanvasObject {
	d.loadTabs()
	d.tabBar = container.NewHBox()
	newTab := widget.NewButtonWithIcon("", theme.ContentAddIcon(), d.newSessionDialog)
	newTab.Importance = widget.LowImportance
	scroll := container.NewHScroll(d.tabBar)
	return container.NewBorder(nil, nil, nil, newTab, scroll)
}

type tabChip struct {
	widget.BaseWidget
	content fyne.CanvasObject
	onTap   func()
	onClose func()
}

func (c *tabChip) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(c.content) }
func (c *tabChip) Tapped(*fyne.PointEvent)             { c.onTap() }
func (c *tabChip) TappedSecondary(*fyne.PointEvent)    { c.onClose() }

func (d *desk) syncTabs() {
	if d.tabBar == nil {
		return
	}
	sig := ""
	if d.current != nil {
		sig = d.current.id
	}
	for _, id := range d.tabs {
		title, _, st := d.rowText(id)
		sig += "|" + id + title + fmt.Sprint(st)
	}
	if sig == d.tabSig {
		return
	}
	d.tabSig = sig
	var objs []fyne.CanvasObject
	var kept []string
	for _, id := range d.tabs {
		if d.session(id) == nil {
			continue
		}
		kept = append(kept, id)
		id := id
		title, _, st := d.rowText(id)
		active := d.current != nil && d.current.id == id
		label := canvas.NewText(oneLine(title, tabTitleRunes), colText)
		label.TextSize = 12
		label.TextStyle.Bold = active
		dt := dot(st.color())
		closeBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() { d.closeTab(id) })
		closeBtn.Importance = widget.LowImportance
		bg := canvas.NewRectangle(colCard)
		bg.CornerRadius = 8
		if active {
			bg.StrokeColor = colText
			bg.StrokeWidth = 2
		}
		row := container.NewHBox(padLeft(4), container.NewCenter(sized(8, 8, dt)), container.NewCenter(label), sized(24, 24, closeBtn))
		chip := &tabChip{content: container.NewStack(bg, row), onTap: func() { d.open(id) }, onClose: func() { d.closeTab(id) }}
		chip.ExtendBaseWidget(chip)
		objs = append(objs, chip)
	}
	if len(kept) != len(d.tabs) {
		d.tabs = kept
	}
	d.tabBar.Objects = objs
	d.tabBar.Refresh()
}

func (d *desk) buildBookmarks() fyne.CanvasObject {
	d.bookBar = container.NewHBox()
	return container.NewHScroll(d.bookBar)
}

func (d *desk) syncBookmarks() {
	if d.bookBar == nil {
		return
	}
	sig := ""
	for _, g := range d.groups {
		sig += fmt.Sprintf("|%s:%s:%d", g.key, g.title, len(g.ids))
	}
	if sig == d.bookSig {
		return
	}
	d.bookSig = sig
	var objs []fyne.CanvasObject
	for _, g := range d.groups {
		g := g
		var btn *widget.Button
		btn = widget.NewButtonWithIcon(fmt.Sprintf("%s (%d)", g.title, len(g.ids)), theme.FolderIcon(), func() {
			pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(btn)
			d.folderMenu2(g, pos.Add(fyne.NewPos(0, btn.Size().Height)))
		})
		btn.Importance = widget.LowImportance
		objs = append(objs, btn)
	}
	d.bookBar.Objects = objs
	d.bookBar.Refresh()
}

func (d *desk) folderMenu2(g bookGroup, at fyne.Position) {
	var pop *widget.PopUp
	for _, x := range d.groups {
		if x.key == g.key {
			g = x
		}
	}
	list := widget.NewList(
		func() int { return len(g.ids) },
		func() fyne.CanvasObject {
			t := widget.NewLabel("")
			t.Truncation = fyne.TextTruncateEllipsis
			return container.NewBorder(nil, nil, container.NewCenter(sized(8, 8, dot(color.Transparent))), nil, t)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(g.ids) {
				return
			}
			title, meta, st := d.rowText(g.ids[i])
			c := o.(*fyne.Container)
			c.Objects[0].(*widget.Label).SetText(title + "   " + meta)
			dt := c.Objects[1].(*fyne.Container).Objects[0].(*fyne.Container).Objects[0].(*canvas.Circle)
			dt.FillColor = st.color()
			dt.Refresh()
		},
	)
	list.OnSelected = func(i widget.ListItemID) {
		if i < len(g.ids) {
			pop.Hide()
			d.open(g.ids[i])
		}
	}
	h := min(float32(menuMaxHeight), float32(len(g.ids))*38+8)
	pop = widget.NewPopUp(sized(menuWidth, h, list), d.win.Canvas())
	pop.ShowAtPosition(at)
}
