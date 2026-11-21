package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

const (
	placeActive   = "active"
	placeInactive = "inactive"
	placeArchive  = "archive"
	placeAuto     = "auto"
	folderPrefix  = "f:"
)

type orgFolder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type sideOrg struct {
	Place   map[string]string  `json:"place"`
	Titles  map[string]string  `json:"titles"`
	Names   map[string]string  `json:"names"`
	Folders []orgFolder        `json:"folders"`
	RC      map[string]rcTitle `json:"rc,omitempty"`
	Kmem    map[string]string  `json:"kmem,omitempty"`
	Ended   map[string]bool    `json:"ended,omitempty"`
}

func (d *desk) loadOrg() {
	d.org = sideOrg{}
	if b := d.app.Preferences().String("sidebarOrg"); b != "" {
		_ = json.Unmarshal([]byte(b), &d.org)
	}
	if d.org.Place == nil {
		d.org.Place = map[string]string{}
	}
	if d.org.Titles == nil {
		d.org.Titles = map[string]string{}
	}
	if d.org.Names == nil {
		d.org.Names = map[string]string{}
	}
	if d.org.Kmem == nil {
		d.org.Kmem = map[string]string{}
	}
	for id, place := range d.org.Place {
		var n int
		if _, err := fmt.Sscanf(d.org.Titles[id], "New session %d", &n); err == nil && place == placeActive {
			delete(d.org.Place, id)
		}
	}
}

func (d *desk) saveOrg() {
	if b, err := json.Marshal(d.org); err == nil {
		d.app.Preferences().SetString("sidebarOrg", string(b))
	}
	d.rebuildRows()
}

func (d *desk) folderByID(id string) (orgFolder, bool) {
	for _, f := range d.org.Folders {
		if f.ID == id {
			return f, true
		}
	}
	return orgFolder{}, false
}

func (d *desk) placement(id string) string {
	p := d.org.Place[id]
	if strings.HasPrefix(p, folderPrefix) {
		if _, ok := d.folderByID(strings.TrimPrefix(p, folderPrefix)); !ok {
			return ""
		}
	}
	return p
}

func (d *desk) setPlacement(id, place string) {
	if place == "" {
		delete(d.org.Place, id)
	} else {
		d.org.Place[id] = place
	}
	d.saveOrg()
}

func (d *desk) folderTitle(key, fallback string) string {
	if n := d.org.Names[key]; n != "" {
		return n
	}
	if strings.HasPrefix(key, folderPrefix) {
		if f, ok := d.folderByID(strings.TrimPrefix(key, folderPrefix)); ok {
			return f.Name
		}
	}
	return fallback
}

func (d *desk) newFolder(onDone func(id string)) {
	d.askName("New folder", "", func(name string) {
		id := fmt.Sprintf("%d", time.Now().UnixNano())
		d.org.Folders = append(d.org.Folders, orgFolder{ID: id, Name: name})
		d.saveOrg()
		if onDone != nil {
			onDone(id)
		}
	})
}

func (d *desk) askName(title, current string, onOK func(string)) {
	e := widget.NewEntry()
	e.SetText(current)
	dlg := dialog.NewForm(title, "Save", "Cancel", []*widget.FormItem{widget.NewFormItem("Name", e)}, func(ok bool) {
		if name := strings.TrimSpace(e.Text); ok && name != "" {
			onOK(name)
		}
	}, d.win)
	dlg.Resize(fyne.NewSize(420, 160))
	dlg.Show()
	d.win.Canvas().Focus(e)
}

func (d *desk) sessionMenu(id string, at fyne.Position, extra ...*fyne.MenuItem) {
	title, _, _ := d.rowText(id)
	move := func(place string) func() { return func() { d.placeSession(id, place) } }
	var moveItems []*fyne.MenuItem
	for _, f := range d.org.Folders {
		moveItems = append(moveItems, fyne.NewMenuItem(f.Name, move(folderPrefix+f.ID)))
	}
	if len(moveItems) > 0 {
		moveItems = append(moveItems, fyne.NewMenuItemSeparator())
	}
	moveItems = append(moveItems, fyne.NewMenuItem("New folder...", func() {
		d.newFolder(func(fid string) { d.setPlacement(id, folderPrefix+fid) })
	}))
	toFolder := fyne.NewMenuItem("Move to folder", nil)
	toFolder.ChildMenu = fyne.NewMenu("", moveItems...)
	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Rename...", func() {
			d.askName("Rename session", title, func(name string) { d.renameSession(id, name) })
		}),
		fyne.NewMenuItem("Open", func() { d.open(id) }),
		fyne.NewMenuItem("Resume", func() { d.resumeSession(id) }),
		fyne.NewMenuItem("Open in split view", func() { d.openInSplit(id) }),
		fyne.NewMenuItem("End session", func() { d.closeTab(id); d.endWithSave(id, placeInactive) }),
		fyne.NewMenuItem("Restart as new session...", func() { d.askRelaunch(id) }),
		d.sessionToolsItem(id),
		fyne.NewMenuItem("Delete session...", func() { d.askDelete(id) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Set active", move(placeActive)),
		fyne.NewMenuItem("Set inactive", move(placeInactive)),
		fyne.NewMenuItem("Set archive", move(placeArchive)),
		fyne.NewMenuItem("Set Created by Claude", move(placeAuto)),
		toFolder,
	}
	if d.org.Place[id] != "" {
		items = append(items, fyne.NewMenuItem("Automatic placement", move("")))
	}
	if _, ok := d.org.Titles[id]; ok {
		items = append(items, fyne.NewMenuItem("Restore original name", func() { d.restoreTitle(id) }))
	}
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), d.win.Canvas(), at)
}

func (d *desk) folderMenu(r sideRow, at fyne.Position) {
	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Rename...", func() {
			d.askName("Rename folder", d.folderTitle(r.key, r.header), func(name string) {
				if strings.HasPrefix(r.key, folderPrefix) {
					fid := strings.TrimPrefix(r.key, folderPrefix)
					for i := range d.org.Folders {
						if d.org.Folders[i].ID == fid {
							d.org.Folders[i].Name = name
						}
					}
				} else {
					d.org.Names[r.key] = name
				}
				d.saveOrg()
			})
		}),
		fyne.NewMenuItem("New folder...", func() { d.newFolder(nil) }),
		fyne.NewMenuItem("Open all in tabs", func() { d.openAllInTabs(r.key) }),
		fyne.NewMenuItem("Open in split view", func() { d.openAllInSplit(r.key) }),
		fyne.NewMenuItem("Delete empty sessions", func() { d.deleteEmptySessions() }),
		fyne.NewMenuItem("Format Rules...", d.rulesDialog),
		fyne.NewMenuItemSeparator(),
	}
	if ra := d.resumeAllMenuItem(r.key); ra != nil {
		items = append([]*fyne.MenuItem{ra, fyne.NewMenuItemSeparator()}, items...)
	}
	if d.layoutMode() == layoutVertical {
		items = append(items,
			fyne.NewMenuItem("Expand all folders", func() { d.setAllFolders(true) }),
			fyne.NewMenuItem("Collapse all folders", func() { d.setAllFolders(false) }),
			fyne.NewMenuItem("Switch to horizontal tabs", func() { d.setLayoutMode(layoutHorizontal) }))
	} else {
		items = append(items, fyne.NewMenuItem("Switch to vertical sidebar", func() { d.setLayoutMode(layoutVertical) }))
	}
	group := fyne.NewMenuItem("Group by project", func() {
		d.groupProjects = !d.groupProjects
		d.app.Preferences().SetBool("groupByProject", d.groupProjects)
		d.rebuildRows()
	})
	group.Checked = d.groupProjects
	items = append(items, group)
	if _, ok := d.org.Names[r.key]; ok {
		items = append(items, fyne.NewMenuItem("Restore original name", func() {
			delete(d.org.Names, r.key)
			d.saveOrg()
		}))
	}
	if strings.HasPrefix(r.key, folderPrefix) {
		fid := strings.TrimPrefix(r.key, folderPrefix)
		items = append(items, fyne.NewMenuItemSeparator(), fyne.NewMenuItem("Delete folder", func() {
			dialog.ShowConfirm("Delete folder?", "Its sessions go back to automatic placement. Nothing is deleted from disk.", func(ok bool) {
				if !ok {
					return
				}
				for i, f := range d.org.Folders {
					if f.ID == fid {
						d.org.Folders = append(d.org.Folders[:i], d.org.Folders[i+1:]...)
						break
					}
				}
				for sid, p := range d.org.Place {
					if p == folderPrefix+fid {
						delete(d.org.Place, sid)
					}
				}
				d.saveOrg()
			}, d.win)
		}))
	}
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), d.win.Canvas(), at)
}

func (d *desk) resumeSession(id string) {
	if d.offerResume(id) {
		return
	}
	d.open(id)
	s := d.sessions[id]
	if s == nil || s.running() || s.takeover {
		return
	}
	if s.terminal != nil {
		dialog.ShowInformation("Open in a terminal", "This session is open in a terminal. Send a message here to take it over.", d.win)
		return
	}
	if err := d.ensureProcess(s); err != nil {
		d.notify(s, "Could not start claude: "+err.Error())
	}
	d.updateChrome()
	d.refreshSidebar()
}

func (d *desk) endSession(id string) {
	s := d.sessions[id]
	if s != nil && s.running() {
		p := s.proc
		s.stopping = true
		go func() { _ = p.Stop(5 * time.Second) }()
		return
	}
	t, held := d.terms[id]
	if !held {
		return
	}
	dialog.ShowConfirm("End session?", fmt.Sprintf("This session is open elsewhere (pid %d). End it there? Its conversation is kept.", t.PID), func(ok bool) {
		if !ok {
			return
		}
		go func() {
			_ = d.finder.Release(t, d.cfg.ClaudeBin, 5*time.Second)
			d.refresh()
		}()
	}, d.win)
}

func (d *desk) dropOn(id string, target sideRow) {
	switch {
	case target.id != "":
		if target.id == id {
			return
		}
		p := d.placement(target.id)
		if p == "" {
			p = d.autoPlace(target.id)
		}
		d.setPlacement(id, p)
	case strings.HasPrefix(target.key, folderPrefix):
		d.setPlacement(id, target.key)
	case target.key == placeInactive || strings.HasPrefix(target.key, placeInactive+":"):
		d.placeSession(id, placeInactive)
	case target.key == placeArchive || strings.HasPrefix(target.key, placeArchive+":"):
		d.placeSession(id, placeArchive)
	case target.key == placeAuto || strings.HasPrefix(target.key, placeAuto+":"):
		d.setPlacement(id, placeAuto)
	case target.key == placeActive || strings.HasPrefix(target.key, "a:"):
		d.setPlacement(id, placeActive)
	}
}

func (d *desk) autoPlace(id string) string {
	for _, r := range d.rows {
		if r.id == id {
			switch {
			case strings.HasPrefix(r.parent, folderPrefix):
				return r.parent
			case r.parent == placeInactive, r.parent == placeArchive, r.parent == placeAuto:
				return r.parent
			}
		}
	}
	return placeActive
}

type sideItem struct {
	widget.BaseWidget
	d       *desk
	rows    *[]sideRow
	idx     int
	content fyne.CanvasObject
	hl      *canvas.Rectangle
	drag    bool
}

func (d *desk) newSideItem(rows *[]sideRow, content fyne.CanvasObject) *sideItem {
	hl := canvas.NewRectangle(colorAlpha(colAccent, 0x40))
	hl.CornerRadius = 6
	hl.Hide()
	it := &sideItem{d: d, rows: rows, content: content, hl: hl, idx: -1}
	it.ExtendBaseWidget(it)
	d.sideItems = append(d.sideItems, it)
	return it
}

func colorAlpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

func (it *sideItem) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(it.hl, it.content))
}

func (it *sideItem) row() (sideRow, bool) {
	if it.idx < 0 || it.idx >= len(*it.rows) {
		return sideRow{}, false
	}
	return (*it.rows)[it.idx], true
}

func (it *sideItem) Tapped(*fyne.PointEvent) {
	r, ok := it.row()
	if !ok {
		return
	}
	if r.header != "" {
		it.d.toggleFolder(r.key)
		return
	}
	it.d.list.Select(it.idx)
}

func (it *sideItem) TappedSecondary(e *fyne.PointEvent) {
	r, ok := it.row()
	if !ok {
		return
	}
	if r.header != "" {
		it.d.folderMenu(r, e.AbsolutePosition)
		return
	}
	it.d.sessionMenu(r.id, e.AbsolutePosition)
}

func (it *sideItem) Dragged(e *fyne.DragEvent) {
	r, ok := it.row()
	if !ok || r.id == "" {
		return
	}
	it.drag = true
	it.d.dragID = r.id
	it.d.dragAt = e.AbsolutePosition
	it.d.markDropTarget()
	it.d.hintDrop(e.AbsolutePosition, true)
}

func (it *sideItem) DragEnd() {
	if !it.drag {
		return
	}
	it.drag = false
	d := it.d
	id := d.dragID
	d.dragID = ""
	target, ok := d.rowAt(d.dragAt)
	d.clearDropTarget()
	d.hintDrop(d.dragAt, false)
	if id == "" {
		return
	}
	if ok {
		d.dropOn(id, target)
		return
	}
	d.dropSession(id, d.dragAt)
}

func (it *sideItem) Cursor() desktop.Cursor {
	return desktop.DefaultCursor
}

func (d *desk) itemAt(pos fyne.Position) *sideItem {
	drv := fyne.CurrentApp().Driver()
	for _, it := range d.sideItems {
		if !it.Visible() || it.idx < 0 || it.idx >= len(d.rows) {
			continue
		}
		p := drv.AbsolutePositionForObject(it)
		s := it.Size()
		if pos.X >= p.X && pos.X < p.X+s.Width && pos.Y >= p.Y && pos.Y < p.Y+s.Height {
			return it
		}
	}
	return nil
}

func (d *desk) rowAt(pos fyne.Position) (sideRow, bool) {
	if it := d.itemAt(pos); it != nil {
		return it.row()
	}
	return sideRow{}, false
}

func (d *desk) markDropTarget() {
	target := d.itemAt(d.dragAt)
	for _, it := range d.sideItems {
		show := it == target
		if show != it.hl.Visible() {
			if show {
				it.hl.Show()
			} else {
				it.hl.Hide()
			}
			it.hl.Refresh()
		}
	}
}

func (d *desk) clearDropTarget() {
	for _, it := range d.sideItems {
		if it.hl.Visible() {
			it.hl.Hide()
			it.hl.Refresh()
		}
	}
}

func (d *desk) setAllFolders(open bool) {
	if d.folderOpen == nil {
		d.folderOpen = map[string]bool{}
	}
	for _, g := range d.groups {
		d.folderOpen[g.key] = open
	}
	if b, err := json.Marshal(d.folderOpen); err == nil {
		d.app.Preferences().SetString("folderOpen", string(b))
	}
	d.rebuildRows()
}

func (d *desk) openAllInSplit(key string) {
	for _, g := range d.groups {
		if g.key != key || len(g.ids) == 0 {
			continue
		}
		ids := g.ids[:min(len(g.ids), maxPanes)]
		if len(ids) == 1 {
			d.openInSplit(ids[0])
			return
		}
		d.setSplit(len(ids))
		for i, id := range ids {
			d.focusPane(d.panes[i])
			d.open(id)
		}
		d.focusPane(d.panes[0])
	}
}

func (d *desk) openAllInTabs(key string) {
	for _, g := range d.groups {
		if g.key != key {
			continue
		}
		ids := g.ids
		if len(ids) > maxTabs {
			ids = ids[:maxTabs]
		}
		for _, id := range ids {
			d.addTab(id)
		}
		if len(ids) > 0 {
			d.open(ids[0])
		}
	}
}
