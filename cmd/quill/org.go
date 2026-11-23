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
	"fyne.io/fyne/v2/theme"
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
	// Hidden: ids of the kmem window's assistant sessions, kept out of the
	// sidebar even after they end (docs/design/kmem-window.md §4).
	Hidden map[string]bool `json:"hidden,omitempty"`
	// Projects: kmem name (or session id) -> project shown in the sidebar.
	Projects map[string]string `json:"projects,omitempty"`
	// Dirs: project -> extra working folders every session of it gets
	// (/add-dir "every session of this project", user 2026-11-21).
	Dirs map[string][]string `json:"dirs,omitempty"`
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
	if d.org.Hidden == nil {
		d.org.Hidden = map[string]bool{}
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
	d.putPlace(id, place)
	d.saveOrg()
}

// putPlace records a session's sidebar place (Inactive, Archive, ...) without
// saving. After the topic migration (topicmigrate.go) there are no places:
// it only clears one.
func (d *desk) putPlace(id, place string) {
	if place == "" || d.placesRemoved() {
		delete(d.org.Place, id)
		return
	}
	d.org.Place[id] = place
}

// endPlace: the place an ended session goes to; none after the topic
// migration.
func (d *desk) endPlace() string {
	if d.placesRemoved() {
		return ""
	}
	return placeInactive
}

// movedTo: " and moved to <place>" for end notices; "" without places.
func (d *desk) movedTo(place string) string {
	if place == "" || d.placesRemoved() {
		return ""
	}
	return " and moved to " + d.folderTitle(place, place)
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
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", d.sessionMenuItems(id)...), d.win.Canvas(), at)
}

// sessionMenuItems: one session = one tab = one kmem name (user 2026-11-21).
// No folders or placements; ended sessions are resumed from the kmem panel.
func (d *desk) sessionMenuItems(id string) []*fyne.MenuItem {
	if s := d.sessions[id]; s != nil && s.remote != nil {
		return []*fyne.MenuItem{
			fyne.NewMenuItem("Open", func() { d.open(id) }),
			fyne.NewMenuItem("Open in split view", func() { d.openInSplit(id) }),
			fyne.NewMenuItem("Stop the current turn", func() { d.interrupt(s) }),
		}
	}
	title, _, _ := d.rowText(id)
	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Rename...", func() {
			d.askName("Rename session", title, func(name string) { d.renameSession(id, name) })
		}),
	}
	if _, ok := d.org.Titles[id]; ok {
		items = append(items, fyne.NewMenuItem("Restore original name", func() { d.restoreTitle(id) }))
	}
	items = append(items,
		fyne.NewMenuItem("Project...", func() { d.askProject(id) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Open", func() { d.open(id) }),
		fyne.NewMenuItem("Open in split view", func() { d.openInSplit(id) }),
	)
	if s := d.sessions[id]; s == nil || !s.running() {
		items = append(items, fyne.NewMenuItem("Start", func() { d.resumeSession(id) }))
	}
	items = append(items,
		d.sessionToolsItem(id),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Restart as new session...", func() { d.askRelaunch(id) }),
		fyne.NewMenuItem("End session", func() { d.closeTab(id); d.endWithSave(id, d.endPlace()) }),
		fyne.NewMenuItem("Delete session...", func() { d.askDelete(id) }),
	)
	return items
}

func (d *desk) askProject(id string) {
	e := widget.NewSelectEntry(d.projectNames())
	e.SetText(d.projectOf(id))
	note := widget.NewLabel("Pick a project or type a new one. Empty = automatic (from kmem, else the session's name).")
	note.Wrapping = fyne.TextWrapWord
	note.SizeName = theme.SizeNameCaptionText
	dlg := dialog.NewForm("Project", "Save", "Cancel", []*widget.FormItem{widget.NewFormItem("Project", e), widget.NewFormItem("", note)}, func(ok bool) {
		if ok {
			d.setProject(id, e.Text)
		}
	}, d.win)
	dlg.Resize(fyne.NewSize(440, 220))
	dlg.Show()
}

func (d *desk) folderMenu(r sideRow, at fyne.Position) {
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", d.projectMenuItems(r)...), d.win.Canvas(), at)
}

func (d *desk) projectMenuItems(r sideRow) []*fyne.MenuItem {
	name := strings.TrimPrefix(r.key, projectPrefix)
	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Open all in tabs", func() { d.openAllInTabs(r.key) }),
		fyne.NewMenuItem("Open all in split view", func() { d.openAllInSplit(r.key) }),
		fyne.NewMenuItem("Rename project...", func() {
			d.askName("Rename project", name, func(to string) { d.renameProject(name, to) })
		}),
		fyne.NewMenuItem("End all...", func() { d.askEndProject(r.key, name) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("kmem...", d.showKmemPanel),
	}
	if d.layoutMode() == layoutVertical {
		items = append(items,
			fyne.NewMenuItem("Expand all projects", func() { d.setAllFolders(true) }),
			fyne.NewMenuItem("Collapse all projects", func() { d.setAllFolders(false) }),
			fyne.NewMenuItem("Switch to horizontal tabs", func() { d.setLayoutMode(layoutHorizontal) }))
	} else {
		items = append(items, fyne.NewMenuItem("Switch to vertical sidebar", func() { d.setLayoutMode(layoutVertical) }))
	}
	return items
}

func (d *desk) askEndProject(key, name string) {
	var ids []string
	for _, g := range d.groups {
		if g.key == key {
			ids = append(ids, g.ids...)
		}
	}
	if len(ids) == 0 {
		return
	}
	msg := fmt.Sprintf("%d session(s) save their kmem handoffs and close. Resume them later from the kmem panel.", len(ids))
	if d.placesRemoved() {
		msg = fmt.Sprintf("%d session(s) close: topic sessions after their end-of-session check, older ones after saving their kmem handoffs. Their conversations stay on disk.", len(ids))
	}
	dialog.ShowConfirm("End all in “"+name+"”?", msg, func(ok bool) {
		if !ok {
			return
		}
		for _, id := range ids {
			d.closeTab(id)
			d.endWithSave(id, d.endPlace())
		}
	}, d.win)
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

// dropOn: a session dropped on a project header or on another session's row
// joins that project (the sidebar has no places or folders since 135bd4b).
func (d *desk) dropOn(id string, target sideRow) {
	switch {
	case target.id != "" && target.id != id && strings.HasPrefix(target.parent, projectPrefix):
		d.setProject(id, d.projectOf(target.id))
	case strings.HasPrefix(target.key, projectPrefix):
		d.setProject(id, strings.TrimPrefix(target.key, projectPrefix))
	}
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
