package main

import (
	"encoding/json"
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
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
	d.strip = &tabStrip{d: d, cur: map[*tabChip]float32{}}
	d.strip.box = container.New(d.strip)
	d.tabBar = container.New(layout.NewCustomPaddedLayout(4, 4, 8, 8), d.strip.box)
	d.tabCenter = container.NewStack(d.tabBar)
	d.applyTabCentering()
	newTab := widget.NewButtonWithIcon("", theme.ContentAddIcon(), d.newSessionDialog)
	newTab.Importance = widget.LowImportance
	d.strip.plus = newTab
	d.strip.setChips(nil)
	return container.NewHScroll(d.tabCenter)
}

type tabChip struct {
	widget.BaseWidget
	content  fyne.CanvasObject
	onTap    func()
	onMenu   func(at fyne.Position)
	onDrag   func(e *fyne.DragEvent)
	onDrop   func()
	onMiddle func()
}

func (c *tabChip) MouseDown(*desktop.MouseEvent) {}

func (c *tabChip) MouseUp(e *desktop.MouseEvent) {
	if e.Button == desktop.MouseButtonTertiary && c.onMiddle != nil {
		c.onMiddle()
	}
}

func (c *tabChip) Dragged(e *fyne.DragEvent) {
	if c.onDrag != nil {
		c.onDrag(e)
	}
}

func (c *tabChip) DragEnd() {
	if c.onDrop != nil {
		c.onDrop()
	}
}

func newChip(content fyne.CanvasObject, onTap func(), onMenu func(fyne.Position)) *tabChip {
	c := &tabChip{content: content, onTap: onTap, onMenu: onMenu}
	c.ExtendBaseWidget(c)
	return c
}

func (c *tabChip) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(c.content) }
func (c *tabChip) Tapped(*fyne.PointEvent)             { c.onTap() }
func (c *tabChip) TappedSecondary(e *fyne.PointEvent)  { c.onMenu(e.AbsolutePosition) }

func (d *desk) followDraft() {
	if d.strip == nil || d.split() {
		return
	}
	cur := ""
	if d.current != nil {
		cur = d.current.id
	}
	if cur == d.strip.lastCur {
		return
	}
	d.strip.keepDraft(d.strip.lastCur, d.input.Text)
	d.strip.lastCur = cur
	if text := d.strip.draft(cur); text != d.input.Text {
		d.input.SetText(text)
	}
}

func (d *desk) syncTabs() {
	if d.tabBar == nil {
		return
	}
	d.followDraft()
	sig := ""
	if d.current != nil {
		sig = d.current.id
	}
	for _, id := range d.tabs {
		title, _, st := d.rowText(id)
		sig += "|" + id + title + fmt.Sprint(st) + fmt.Sprint(d.session(id) != nil)
	}
	if sig == d.tabSig {
		return
	}
	d.tabSig = sig
	if d.tabChips == nil {
		d.tabChips = map[string]*tabChip{}
	}
	var chips []*tabChip
	seen := map[string]bool{}
	for _, id := range d.tabs {
		if d.session(id) == nil {
			continue
		}
		id := id
		seen[id] = true
		title, _, st := d.rowText(id)
		active := d.current != nil && d.current.id == id
		label := canvas.NewText(oneLine(title, tabTitleRunes), colText)
		label.TextSize = 13
		label.TextStyle.Bold = active
		if !active {
			label.Color = colMuted
		}
		dt := dot(st.color())
		closeBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() { d.closeTab(id) })
		closeBtn.Importance = widget.LowImportance
		bg := canvas.NewRectangle(colCard)
		bg.CornerRadius = 10
		if active {
			bg.StrokeColor = colText
			bg.StrokeWidth = 2
		}
		row := container.NewHBox(padLeft(10), container.NewCenter(sized(9, 9, dt)), padLeft(2), container.NewCenter(label), padLeft(2), container.NewCenter(sized(22, 22, closeBtn)), padLeft(4))
		inner := []fyne.CanvasObject{bg, container.New(layout.NewCustomPaddedLayout(5, 5, 0, 0), row)}
		chip := d.tabChips[id]
		if chip == nil {
			holder := container.NewStack(inner...)
			chip = newChip(holder, func() { d.open(id) }, func(at fyne.Position) { d.tabMenu(id, at) })
			chip.onDrag = func(e *fyne.DragEvent) { d.strip.drag(chip, id, e) }
			chip.onDrop = func() { d.strip.drop() }
			chip.onMiddle = func() { d.closeTab(id) }
			d.tabChips[id] = chip
		} else {
			holder := chip.content.(*fyne.Container)
			holder.Objects = inner
			holder.Refresh()
		}
		chips = append(chips, chip)
	}
	for id := range d.tabChips {
		if !seen[id] {
			delete(d.tabChips, id)
		}
	}
	d.strip.setChips(chips)
	if d.split() {
		d.syncPanes()
	}
}

func (d *desk) buildBookmarks() fyne.CanvasObject {
	d.bookBar = container.NewHBox()
	d.bookCenter = container.NewStack(container.New(layout.NewCustomPaddedLayout(0, 0, 8, 8), d.bookBar))
	d.applyTabCentering()
	return container.NewHScroll(d.bookCenter)
}

func (d *desk) applyTabCentering() {
	on := d.app.Preferences().Bool("centerTabs")
	set := func(c *fyne.Container) {
		if c == nil || len(c.Objects) == 0 {
			return
		}
		inner := c.Objects[0]
		if on {
			c.Layout = layout.NewCenterLayout()
		} else {
			c.Layout = layout.NewStackLayout()
		}
		c.Objects = []fyne.CanvasObject{inner}
		c.Refresh()
	}
	set(d.tabCenter)
	set(d.bookCenter)
}

func (d *desk) setTabCenter(on bool) {
	d.app.Preferences().SetBool("centerTabs", on)
	d.applyTabCentering()
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
		icon := widget.NewIcon(theme.FolderIcon())
		label := canvas.NewText(fmt.Sprintf("%s (%d)", g.title, len(g.ids)), colText)
		label.TextSize = 13
		label.TextStyle.Bold = true
		row := container.NewHBox(padLeft(6), container.NewCenter(sized(18, 18, icon)), container.NewCenter(label), padLeft(6))
		var chip *tabChip
		chip = newChip(container.New(layout.NewCustomPaddedLayout(4, 4, 0, 0), row), func() {
			pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(chip)
			d.folderMenu2(g, pos.Add(fyne.NewPos(0, chip.Size().Height)))
		}, func(at fyne.Position) {
			d.folderMenu(sideRow{header: g.title, key: g.key}, at)
		})
		objs = append(objs, chip, padLeft(4))
	}
	d.bookBar.Objects = objs
	d.bookBar.Refresh()
	d.autoSplitSync()
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
			c := container.NewBorder(nil, nil, container.NewCenter(sized(8, 8, dot(color.Transparent))), nil, t)
			return newChip(c, func() {}, func(fyne.Position) {})
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(g.ids) {
				return
			}
			id := g.ids[i]
			chip := o.(*tabChip)
			chip.onTap = func() {
				pop.Hide()
				d.open(id)
			}
			chip.onMenu = func(at fyne.Position) {
				pop.Hide()
				d.sessionMenu(id, at, fyne.NewMenuItem("Open in new tab", func() { d.addTab(id) }))
			}
			title, meta, st := d.rowText(id)
			c := chip.content.(*fyne.Container)
			c.Objects[0].(*widget.Label).SetText(title + "   " + meta)
			dt := c.Objects[1].(*fyne.Container).Objects[0].(*fyne.Container).Objects[0].(*canvas.Circle)
			dt.FillColor = st.color()
			dt.Refresh()
		},
	)
	h := min(float32(menuMaxHeight), float32(len(g.ids))*38+8)
	pop = widget.NewPopUp(sized(menuWidth, h, list), d.win.Canvas())
	pop.ShowAtPosition(at)
}

func (d *desk) tabMenu(id string, at fyne.Position) {
	closeOthers := func(keep func(i, at int) bool) {
		pos := -1
		for i, t := range d.tabs {
			if t == id {
				pos = i
			}
		}
		var kept []string
		for i, t := range d.tabs {
			if keep(i, pos) {
				kept = append(kept, t)
			}
		}
		d.tabs = kept
		d.saveTabs()
		d.tabSig = ""
		d.syncTabs()
	}
	d.sessionMenu(id, at,
		fyne.NewMenuItem("Close tab", func() { d.closeTab(id) }),
		fyne.NewMenuItem("Close other tabs", func() { closeOthers(func(i, at int) bool { return i == at }) }),
		fyne.NewMenuItem("Close tabs to the right", func() { closeOthers(func(i, at int) bool { return i <= at }) }),
	)
}

func (d *desk) reopenLastTab() {
	if d.current != nil {
		return
	}
	id := d.app.Preferences().String("lastTab")
	if _, ok := d.infos[id]; ok {
		d.open(id)
		return
	}
	for i := len(d.tabs) - 1; i >= 0; i-- {
		if _, ok := d.infos[d.tabs[i]]; ok {
			d.open(d.tabs[i])
			return
		}
	}
}
