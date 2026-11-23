package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strings"

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
	keep := []string{}
	for _, id := range d.tabs {
		if !strings.HasPrefix(id, remotePrefix) { // other PCs: memory only
			keep = append(keep, id)
		}
	}
	if b, err := json.Marshal(keep); err == nil {
		d.app.Preferences().SetString("tabs", string(b))
	}
	// The sidebar shows live sessions only, so opening or closing a tab
	// changes it.
	if d.list != nil && d.search != nil && d.listTheme != nil {
		d.rebuildRows()
	}
}

func (d *desk) addTab(id string) {
	if d.hiddenID(id, "") {
		return // the kmem window's assistant never gets a tab
	}
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
	wasCurrent := d.current != nil && d.current.id == id
	if wasCurrent && len(d.tabs) > 0 {
		d.open(d.tabs[min(idx, len(d.tabs)-1)])
	}
	if d.isEmptySession(id) {
		d.purgeEmpty(id)
	}
	d.syncTabs()
}

func (d *desk) isEmptySession(id string) bool {
	s := d.sessions[id]
	if s == nil || s.persisted || s.running() || s.loading || s.takeover {
		return false
	}
	if _, ok := d.infos[id]; ok {
		return false
	}
	return countMessages(s.conv) == 0 && strings.TrimSpace(d.strip.draft(id)) == "" && !(d.current == s && strings.TrimSpace(d.input.Text) != "")
}

func (d *desk) purgeEmpty(id string) {
	if p := d.paneFor(d.sessions[id]); p != nil && len(d.panes) > 1 {
		p.view.show(nil)
	}
	if d.current != nil && d.current.id == id {
		d.current = nil
		d.chat.show(nil)
	}
	delete(d.sessions, id)
	delete(d.created, id)
	delete(d.org.Titles, id)
	delete(d.org.Place, id)
	for i, t := range d.tabs {
		if t == id {
			d.tabs = append(d.tabs[:i], d.tabs[i+1:]...)
			break
		}
	}
	d.saveTabs()
	d.saveOrg()
	d.updateChrome()
}

func (d *desk) deleteEmptySessions() int {
	var ids []string
	for id := range d.sessions {
		if d.current != nil && d.current.id == id || d.hiddenID(id, "") {
			continue // its history is in kmem, not a tab
		}
		if d.isEmptySession(id) {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		d.purgeEmpty(id)
	}
	d.tabSig = ""
	d.syncTabs()
	return len(ids)
}

func (d *desk) openTabNumber(n int) {
	if len(d.tabs) == 0 {
		return
	}
	i := n - 1
	if n == 9 || i >= len(d.tabs) {
		i = len(d.tabs) - 1
	}
	id := d.tabs[i]
	if d.split() {
		if s := d.session(id); s != nil {
			if p := d.paneFor(s); p != nil {
				d.focusPane(p)
				return
			}
		}
	}
	d.open(id)
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
	glow     *canvas.Rectangle
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
	if d.current != nil {
		defer d.syncQueue(d.current)
	} else if d.chat != nil && d.chat.queueBox != nil {
		d.chat.queueBox.Objects = nil
		d.chat.queueBox.Refresh()
	}
	first := d.strip.lastCur == ""
	if prev := d.sessions[d.strip.lastCur]; prev != nil && prev.qedit != nil {
		d.cancelQueueEdit(prev)
	}
	d.strip.keepDraft(d.strip.lastCur, d.input.Text)
	d.strip.lastCur = cur
	text := d.strip.draft(cur)
	if first && text == "" {
		d.strip.keepDraft(cur, d.input.Text)
		return
	}
	if text != d.input.Text {
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
		if d.session(id) == nil || d.hiddenID(id, "") {
			continue
		}
		id := id
		seen[id] = true
		title, _, st := d.rowText(id)
		active := d.current != nil && d.current.id == id
		label := canvas.NewText(d.tabLabel(id, title), colText)
		label.TextSize = fontBody
		label.TextStyle.Bold = active
		if !active {
			label.Color = colMuted
		}
		dt := statusMark(st)
		closeBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() { d.closeSessionTab(id) })
		closeBtn.Importance = widget.LowImportance
		bg := canvas.NewRectangle(colCard)
		bg.CornerRadius = 10
		if active {
			bg.StrokeColor = colText
			bg.StrokeWidth = 2
		}
		row := container.NewHBox(padLeft(10), container.NewCenter(sized(14, 14, dt)), padLeft(2), container.NewCenter(label), padLeft(2), container.NewCenter(sized(22, 22, closeBtn)), padLeft(4))
		if s := d.sessions[id]; s != nil && s.remote != nil {
			// A session on another PC: computer icon in front of its title.
			row.Objects = append([]fyne.CanvasObject{row.Objects[0], row.Objects[1], padLeft(4), container.NewCenter(sized(16, 16, widget.NewIcon(theme.ComputerIcon())))}, row.Objects[2:]...)
		}
		chip := d.tabChips[id]
		if chip == nil {
			chip = newChip(container.NewStack(), func() { d.open(id) }, func(at fyne.Position) { d.tabMenu(id, at) })
			chip.onDrag = func(e *fyne.DragEvent) { d.strip.drag(chip, id, e) }
			chip.onDrop = func() { d.strip.drop() }
			chip.onMiddle = func() { d.closeSessionTab(id) }
			chip.glow = canvas.NewRectangle(color.Transparent)
			chip.glow.CornerRadius = 10
			chip.glow.StrokeWidth = 1.5
			d.tabChips[id] = chip
		}
		if active {
			d.setGlow(chip.glow, nil, 0)
		} else {
			d.setGlow(chip.glow, st.color(), glowStrength(st))
		}
		holder := chip.content.(*fyne.Container)
		holder.Objects = []fyne.CanvasObject{bg, chip.glow, container.New(layout.NewCustomPaddedLayout(5, 5, 0, 0), row)}
		holder.Refresh()
		chips = append(chips, chip)
	}
	for id, chip := range d.tabChips {
		if !seen[id] {
			d.setGlow(chip.glow, nil, 0)
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
	// The chips row only shows in Landscape, where the open sessions are the
	// tabs right below it, so the Active folder gets no chip (user 2026-11-21).
	var chips []bookGroup
	sig := ""
	for _, g := range d.groups {
		if g.key == placeActive {
			continue
		}
		chips = append(chips, g)
		sig += fmt.Sprintf("|%s:%s:%d", g.key, g.title, len(g.ids))
	}
	if sig == d.bookSig {
		return
	}
	d.bookSig = sig
	var objs []fyne.CanvasObject
	for _, g := range chips {
		g := g
		icon := widget.NewIcon(theme.FolderIcon())
		label := canvas.NewText(fmt.Sprintf("%s (%d)", g.title, len(g.ids)), colText)
		label.TextSize = fontBody
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
	for _, x := range d.groups {
		if x.key == g.key {
			g = x
		}
	}
	var closeMenu func()
	list := widget.NewList(
		func() int { return len(g.ids) },
		func() fyne.CanvasObject {
			title := widget.NewLabel("")
			title.TextStyle.Bold = true
			title.Truncation = fyne.TextTruncateEllipsis
			meta := canvas.NewText("", colMuted)
			meta.TextSize = fontMeta
			text := container.New(&tightVBox{}, title, container.New(layout.NewCustomPaddedLayout(0, 0, 8, 0), meta))
			row := container.NewBorder(nil, nil, container.NewHBox(padLeft(4), container.NewCenter(sized(9, 9, dot(color.Transparent)))), nil, text)
			return newChip(row, func() {}, func(fyne.Position) {})
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(g.ids) {
				return
			}
			id := g.ids[i]
			chip := o.(*tabChip)
			chip.onTap = func() {
				closeMenu()
				d.open(id)
			}
			chip.onMenu = func(at fyne.Position) {
				closeMenu()
				d.sessionMenu(id, at, fyne.NewMenuItem("Open in new tab", func() { d.addTab(id) }))
			}
			title, meta, st := d.rowText(id)
			row := chip.content.(*fyne.Container)
			text := row.Objects[0].(*fyne.Container)
			text.Objects[0].(*widget.Label).SetText(title)
			m := text.Objects[1].(*fyne.Container).Objects[0].(*canvas.Text)
			m.Text = meta
			m.Refresh()
			dt := row.Objects[1].(*fyne.Container).Objects[1].(*fyne.Container).Objects[0].(*fyne.Container).Objects[0].(*canvas.Circle)
			dt.FillColor = st.color()
			dt.Refresh()
		},
	)
	list.HideSeparators = true
	for i := range g.ids {
		list.SetItemHeight(i, 50)
	}
	head := canvas.NewText(fmt.Sprintf("%s · %d", g.title, len(g.ids)), colText)
	head.TextStyle.Bold = true
	head.TextSize = fontBody
	bgr := canvas.NewRectangle(colCard)
	bgr.CornerRadius = 14
	bgr.StrokeColor = color.NRGBA{0xff, 0xff, 0xff, 0x22}
	bgr.StrokeWidth = 1
	top := container.New(layout.NewCustomPaddedLayout(4, 4, 10, 10), head)
	h := min(float32(menuMaxHeight), top.MinSize().Height+float32(len(g.ids))*50+4)
	body := container.NewBorder(top, nil, nil, nil, container.NewThemeOverride(list, compactTheme{}))
	card := container.NewStack(bgr, container.New(layout.NewCustomPaddedLayout(6, 8, 6, 6), sized(menuWidth, h, body)))
	closeMenu = d.showDropdown(card, at, func(p fyne.Position) { d.folderMenu(sideRow{header: g.title, key: g.key}, p) })
}

type dropdown struct {
	widget.BaseWidget
	card     fyne.CanvasObject
	at       fyne.Position
	close    func()
	onMenu   func(fyne.Position)
	onClosed func()
}

func (dd *dropdown) CreateRenderer() fyne.WidgetRenderer {
	return &dropdownRenderer{dd: dd, card: newAbsorb(dd.card)}
}

func (dd *dropdown) Tapped(*fyne.PointEvent) { dd.close() }

func (dd *dropdown) TappedSecondary(e *fyne.PointEvent) {
	dd.close()
	if dd.onMenu != nil {
		dd.onMenu(e.AbsolutePosition)
	}
}

type dropdownRenderer struct {
	dd   *dropdown
	card fyne.CanvasObject
}

func (r *dropdownRenderer) Layout(size fyne.Size) {
	m := r.card.MinSize()
	x := min(r.dd.at.X, size.Width-m.Width-8)
	y := min(r.dd.at.Y+4, size.Height-m.Height-8)
	r.card.Resize(m)
	r.card.Move(fyne.NewPos(max(8, x), max(8, y)))
}

func (r *dropdownRenderer) MinSize() fyne.Size           { return fyne.NewSize(0, 0) }
func (r *dropdownRenderer) Refresh()                     { r.card.Refresh() }
func (r *dropdownRenderer) Destroy()                     {}
func (r *dropdownRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.card} }

func (d *desk) showDropdown(card fyne.CanvasObject, at fyne.Position, onMenu func(fyne.Position)) func() {
	return d.showDropdownWith(&dropdown{card: card, at: at, onMenu: onMenu})
}

func (d *desk) showDropdownWith(dd *dropdown) func() {
	c := d.win.Canvas()
	dd.ExtendBaseWidget(dd)
	prev := c.OnTypedKey()
	closed := false
	dd.close = func() {
		if closed {
			return
		}
		closed = true
		c.Overlays().Remove(dd)
		c.SetOnTypedKey(prev)
		if dd.onClosed != nil {
			dd.onClosed()
		}
	}
	c.Unfocus()
	c.SetOnTypedKey(func(k *fyne.KeyEvent) {
		if k.Name == fyne.KeyEscape {
			dd.close()
			return
		}
		if prev != nil {
			prev(k)
		}
	})
	c.Overlays().Add(dd)
	d.escClose(dd, dd.close)
	dd.Resize(c.Size())
	return dd.close
}

func (d *desk) tabMenu(id string, at fyne.Position) {
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", d.tabMenuItems(id)...), d.win.Canvas(), at)
}

// tabMenuItems is the session menu; a topic tab (docs/design/topic-tabs.md
// §3.3) has no Rename or Restore original name, its title is the topic, and
// gets "Switch topic..." instead. An old-name tab (merging into a topic) also
// loses them: its title is the old name until it ends.
func (d *desk) tabMenuItems(id string) []*fyne.MenuItem {
	items := d.sessionMenuItems(id)
	_, old := d.mergeTarget(id)
	if d.topicOf(id) == "" && !old {
		return items
	}
	var out []*fyne.MenuItem
	for _, it := range items {
		switch it.Label {
		case "Rename...", "Restore original name":
			continue
		}
		out = append(out, it)
		if it.Label == "Open in split view" && !old {
			out = append(out, fyne.NewMenuItem("Switch topic...", func() { d.switchTopic(id) }))
		}
	}
	return out
}

// switchTopic ends the topic's session the way End session does today (the
// handoff save is the end-check until kmem's K4 and sub C replace it), then
// opens a new tab on the "Open a topic" card.
func (d *desk) switchTopic(id string) {
	d.closeTab(id)
	d.endWithSave(id, d.endPlace())
	d.newSessionDialog()
}

// topicForName: the topic a kmem name belongs to. A name that is itself a
// topic maps to itself; an old name only through kmem's config
// "sessions": {"NAME": {"topic": ...}}, never by guessing from its words.
func topicForName(name string) string {
	if name == "" {
		return ""
	}
	if _, ok := cachedTopic(name); ok {
		return name
	}
	return strings.TrimSpace(kmemSessions()[name].Topic)
}

// mergeTarget: the topic an old-name session (a kmem name that is not a
// topic) will be merged into: the explicit mapping in kmem's config
// "sessions" only. Only in kmem memory mode, only for live sessions; ok is
// false when the name has no mapping or the mapped topic is unknown.
func (d *desk) mergeTarget(id string) (topicInfo, bool) {
	k := d.org.Kmem[id]
	if k == "" || d.sessions[id] == nil || d.topicOf(id) != "" || !d.kmemMemory() {
		return topicInfo{}, false
	}
	if t := topicForName(k); t != "" {
		if ti, ok := cachedTopic(t); ok && ti.Labeled && ti.Slug != "kmem" {
			return ti, true
		}
	}
	return topicInfo{}, false
}

const mergeMark = " → merging into "

// topicRowTitle: a topic coordinator is shown as its topic's title; an old
// name as "<its title> → merging into <Topic>" until it ends.
func (d *desk) topicRowTitle(id, title string) string {
	if s := d.sessions[id]; s == nil || s.remote != nil {
		return title
	}
	if slug := d.topicOf(id); slug != "" {
		if t, ok := cachedTopic(slug); ok && t.Title != "" {
			return t.Title
		}
		return title
	}
	if t, ok := d.mergeTarget(id); ok {
		return title + mergeMark + t.Title
	}
	return title
}

// tabLabel shortens a tab title but keeps the "merging into" tail visible.
func (d *desk) tabLabel(id, title string) string {
	if i := strings.Index(title, mergeMark); i >= 0 {
		return oneLine(title[:i], 14) + title[i:]
	}
	return oneLine(title, tabTitleRunes)
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
