package main

import (
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

const (
	maxPanes = 3
	paneGap  = 10
	fill     = 1e6
)

type pane struct {
	slot    *fyne.Container
	tasks   *tasksChip
	d       *desk
	view    *chatView
	title   *widget.Label
	reload  *widget.Button // Reload for Other PCs sessions (remotereload.go)
	outline *canvas.Rectangle
	dropHL  *canvas.Rectangle
	root    fyne.CanvasObject
	input   *sendEntry
	lastID  string
	picker  *fyne.Container
	choices *fyne.Container
	model   *widget.Select
	mode    *widget.Select
	backend *widget.Button
	stop    *widget.Button
	mic     *widget.Button
	stats   *canvas.Text
	quiet   bool

	pending []*chat.Item
	timer   *time.Timer
}

type paneBox struct {
	widget.BaseWidget
	p       *pane
	content fyne.CanvasObject
}

func (b *paneBox) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(b.content) }
func (b *paneBox) Tapped(*fyne.PointEvent)             { b.p.d.focusPane(b.p) }

type evenColumns struct{}

func (evenColumns) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h float32
	n := 0
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		w = max(w, m.Width)
		h = max(h, m.Height)
		n++
	}
	if n == 0 {
		return fyne.Size{}
	}
	return fyne.NewSize(w*float32(n)+paneGap*float32(n-1), h)
}

func (evenColumns) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	var shown []fyne.CanvasObject
	for _, o := range objs {
		if o.Visible() {
			shown = append(shown, o)
		}
	}
	n := float32(len(shown))
	if n == 0 {
		return
	}
	w := (size.Width - paneGap*(n-1)) / n
	for i, o := range shown {
		o.Move(fyne.NewPos(float32(i)*(w+paneGap), 0))
		o.Resize(fyne.NewSize(w, size.Height))
	}
}

func (d *desk) newPane() *pane {
	p := &pane{d: d}
	p.view = newChatViewWith(d, func() float32 { return fill }, nil)
	p.title = widget.NewLabel(emptyPaneText)
	p.title.TextStyle.Bold = true
	p.title.Truncation = fyne.TextTruncateEllipsis
	closeBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() { d.closePane(p) })
	closeBtn.Importance = widget.LowImportance
	p.reload = d.newRemoteReloadButton("", func() *deskSession { return p.view.s })
	head := container.NewBorder(nil, nil, padLeft(4), container.NewHBox(p.reload, closeBtn), p.title)
	p.outline = canvas.NewRectangle(color.Transparent)
	p.outline.CornerRadius = 14
	p.outline.StrokeWidth = 1
	p.outline.StrokeColor = color.NRGBA{0xff, 0xff, 0xff, 0x1c}
	p.input = newSendEntry(func(text string) { d.sendFromPane(p, text) })
	p.input.later = d.afterUI
	p.input.ac = d.newAutocorrector()
	p.input.known = func() func(string) bool { return d.knownWords(p.view.s) }
	p.input.onFix = func(msg string) { d.flashHint(p.stats, msg) }
	p.input.onSendNow = func(text string) { d.sendNowFromPane(p, text) }
	p.input.SetMinRowsVisible(2)
	p.input.hint = "Message Claude"
	p.input.SetPlaceHolder(p.input.hint)
	p.input.onShortcut = d.handleShortcut
	p.input.onHistory = func(k *fyne.KeyEvent) bool { return d.historyKey(p.input, p.view.s, k) }
	p.input.onActivity = func() { d.noteActivity(p.view.s) }
	p.input.onFocus = d.clearSelections
	p.input.onEscape = func() bool { return d.escape(p.view.s) }
	sendBtn := widget.NewButtonWithIcon("", theme.MailSendIcon(), func() { d.sendFromPane(p, p.input.Text) })
	sendBtn.Importance = widget.HighImportance
	p.stop = widget.NewButtonWithIcon("", theme.MediaStopIcon(), func() {
		if p.view.s != nil {
			d.interrupt(p.view.s)
		}
	})
	p.stop.Importance = widget.DangerImportance
	p.mic = widget.NewButtonWithIcon("", theme.MediaRecordIcon(), func() {
		d.focusPane(p)
		d.stt.entry = p.input
		d.stt.click()
	})
	p.mic.Importance = widget.LowImportance
	p.model = widget.NewSelect(modelChoices, func(v string) {
		if !p.quiet && p.view.s != nil {
			d.app.Preferences().SetString("newModel", v)
			d.setModel(p.view.s, v)
		}
	})
	p.mode = widget.NewSelect(modeChoices, func(v string) {
		if !p.quiet && p.view.s != nil {
			d.app.Preferences().SetString("newMode", v)
			d.setMode(p.view.s, v)
		}
	})
	p.backend = widget.NewButton(backendOnline, func() {
		if p.view.s != nil {
			d.focusPane(p)
			d.showBackendMenu(p.view.s, p.backend, func() { d.syncPaneControls(p) })
		}
	})
	openAll := widget.NewButtonWithIcon("", theme.MenuDropDownIcon(), func() {
		d.focusPane(p)
		d.setAllChips(true)
	})
	openAll.Importance = widget.MediumImportance
	closeAll := widget.NewButtonWithIcon("", theme.MenuDropUpIcon(), func() {
		d.focusPane(p)
		d.setAllChips(false)
	})
	closeAll.Importance = widget.MediumImportance
	p.tasks = newTasksButton(func() *deskSession { return p.view.s }, d.cmdTasks)
	controls := container.NewBorder(nil, nil, container.NewHBox(p.backend, boxed(p.model), boxed(p.mode), p.tasks), container.NewHBox(openAll, closeAll))
	side := container.NewVBox(p.stop, layout.NewSpacer(), p.mic, sendBtn)
	card := bg(colInput, 10, container.NewBorder(nil, container.NewThemeOverride(controls, compactTheme{}), nil, side, p.input))
	p.stats = mutedText("", fontMeta)
	p.slot = container.NewHBox()
	slotPad := canvas.NewRectangle(color.Transparent)
	slotPad.SetMinSize(fyne.NewSize(1, d.settingsBtn.MinSize().Height))
	inputRow := container.NewVBox(container.NewStack(slotPad, p.slot), card, container.New(&clipLayout{}, p.stats))
	p.choices = container.NewVBox()
	hint := widget.NewLabelWithStyle(emptyPaneText, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	p.picker = container.NewCenter(container.NewVBox(hint, p.choices))
	body := container.NewBorder(head, container.NewPadded(inputRow), nil, nil, container.NewStack(p.view.root, p.picker))
	p.dropHL = canvas.NewRectangle(fade(colAccent, 0.14))
	p.dropHL.CornerRadius = 14
	p.dropHL.StrokeColor = colAccent
	p.dropHL.StrokeWidth = 2
	p.dropHL.Hide()
	box := &paneBox{p: p, content: container.NewStack(p.outline, container.NewPadded(body), p.dropHL)}
	box.ExtendBaseWidget(box)
	p.root = box
	return p
}

const emptyPaneText = "Drag and drop a session"

func (d *desk) sendFromPane(p *pane, text string) {
	if p.view.s == nil || strings.TrimSpace(text) == "" {
		return
	}
	d.focusPane(p)
	d.send(text)
	if p.view.s != nil && strings.TrimSpace(p.input.Text) == strings.TrimSpace(text) {
		p.input.SetText("")
	}
	d.syncPaneControls(p)
}

func (d *desk) showInPane(p *pane, id string) {
	s := d.session(id)
	if s == nil || p == nil {
		return
	}
	other := d.paneFor(s)
	if other == p {
		d.focusPane(p)
		return
	}
	prev := p.view.s
	if other != nil {
		other.view.show(nil)
	}
	d.focusPane(p)
	d.open(id)
	if prev == nil || prev == s {
		return
	}
	dest := other
	if dest == nil {
		for _, q := range d.panes {
			if q != p && q.view.s == nil {
				dest = q
				break
			}
		}
	}
	if dest != nil {
		d.focusPane(dest)
		d.open(prev.id)
		d.focusPane(p)
	}
}

func (d *desk) hintDrop(abs fyne.Position, on bool) {
	var target *pane
	if on {
		target = d.paneAt(abs)
	}
	for _, p := range d.panes {
		if p.dropHL == nil {
			continue
		}
		if show := p == target; show != p.dropHL.Visible() {
			if show {
				p.dropHL.Show()
			} else {
				p.dropHL.Hide()
			}
			p.dropHL.Refresh()
		}
	}
	if d.split() || d.chatDropHL == nil {
		return
	}
	show := on && d.overChat(abs)
	if show != d.chatDropHL.Visible() {
		if show {
			d.chatDropHL.Show()
		} else {
			d.chatDropHL.Hide()
		}
		d.chatDropHL.Refresh()
	}
}

func (d *desk) overChat(abs fyne.Position) bool {
	if d.chatHost == nil {
		return false
	}
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(d.chatHost)
	sz := d.chatHost.Size()
	return abs.X >= pos.X && abs.X < pos.X+sz.Width && abs.Y >= pos.Y && abs.Y < pos.Y+sz.Height
}

func (d *desk) dropSession(id string, abs fyne.Position) bool {
	d.hintDrop(abs, false)
	if d.split() {
		if p := d.paneAt(abs); p != nil {
			d.showInPane(p, id)
			return true
		}
		return false
	}
	if !d.overChat(abs) || d.current == nil || d.current.id == id {
		return false
	}
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(d.chatHost)
	left := abs.X < pos.X+d.chatHost.Size().Width/2
	d.setSplit(2)
	if len(d.panes) < 2 {
		return false
	}
	target := d.panes[1]
	if left {
		target = d.panes[0]
	}
	d.showInPane(target, id)
	return true
}

func (d *desk) paneAt(abs fyne.Position) *pane {
	for _, p := range d.panes {
		pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(p.root)
		sz := p.root.Size()
		if abs.X >= pos.X && abs.X < pos.X+sz.Width && abs.Y >= pos.Y && abs.Y < pos.Y+sz.Height {
			return p
		}
	}
	return nil
}

func padLeft(w float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(w, 1))
	return r
}

func (d *desk) split() bool { return len(d.panes) > 1 }

func (d *desk) paneFor(s *deskSession) *pane {
	for _, p := range d.panes {
		if p.view.s == s {
			return p
		}
	}
	return nil
}

func (d *desk) setSplit(n int) {
	n = max(1, min(maxPanes, n))
	if n == 1 {
		if !d.split() {
			return
		}
		cur := d.current
		for _, p := range d.panes {
			if p.view.s != nil {
				d.strip.keepDraft(p.view.s.id, p.input.Text)
			}
		}
		d.panes = nil
		d.chat = d.single
		d.chatHost.Objects = []fyne.CanvasObject{d.single.root, d.chatDropHL}
		d.chatHost.Refresh()
		d.focused = nil
		if cur != nil {
			d.chat.show(cur)
			d.input.SetText(d.strip.draft(cur.id))
			d.strip.lastCur = cur.id
		} else {
			d.chat.show(nil)
		}
		d.afterSplit()
		return
	}
	if !d.split() {
		if d.current != nil {
			d.strip.keepDraft(d.current.id, d.input.Text)
		}
		d.input.SetText("")
		first := d.newPane()
		d.panes = []*pane{first}
		d.focused = first
		d.chat = first.view
		if d.current != nil {
			first.view.show(d.current)
		} else {
			first.view.show(nil)
		}
	}
	for len(d.panes) < n {
		p := d.newPane()
		p.view.show(nil)
		d.panes = append(d.panes, p)
	}
	for len(d.panes) > n {
		last := d.panes[len(d.panes)-1]
		d.panes = d.panes[:len(d.panes)-1]
		if last == d.focused {
			d.focusPane(d.panes[len(d.panes)-1])
		}
	}
	roots := make([]fyne.CanvasObject, len(d.panes))
	for i, p := range d.panes {
		roots[i] = p.root
	}
	d.chatHost.Objects = []fyne.CanvasObject{container.New(evenColumns{}, roots...)}
	d.chatHost.Refresh()
	d.afterSplit()
}

func (d *desk) afterSplit() {
	if d.bottomBox != nil {
		if d.split() {
			d.bottomBox.Hide()
		} else {
			d.bottomBox.Show()
		}
	}
	d.syncPanes()
	d.syncSplitButtons()
	d.placeToolbar()
	d.syncVisibleQueues()
	if d.bottomBox != nil {
		relayout(d.bottomBox)
	}
	if d.mainPane != nil {
		relayout(d.mainPane)
		relayout(d.usageBox)
	}
	d.updateChrome()
	d.refreshSidebar()
}

func (d *desk) focusPane(p *pane) {
	if p == nil || p == d.focused {
		return
	}
	d.flushUI()
	d.focused = p
	d.chat = p.view
	d.current = p.view.s
	d.syncPanes()
	d.updateChrome()
	d.refreshSidebar()
	d.win.Canvas().Focus(p.input)
}

func (d *desk) closePane(p *pane) {
	if len(d.panes) <= 2 {
		keep := d.panes[0]
		if keep == p {
			keep = d.panes[1]
		}
		d.focusPane(keep)
		d.setSplit(1)
		return
	}
	for i, x := range d.panes {
		if x == p {
			d.panes = append(d.panes[:i], d.panes[i+1:]...)
			break
		}
	}
	if d.focused == p {
		d.focusPane(d.panes[0])
	}
	d.setSplit(len(d.panes))
}

func (d *desk) openInSplit(id string) {
	s := d.session(id)
	if s == nil {
		return
	}
	if p := d.paneFor(s); p != nil {
		d.focusPane(p)
		return
	}
	if !d.split() {
		d.setSplit(2)
	} else if len(d.panes) < maxPanes {
		d.setSplit(len(d.panes) + 1)
	}
	target := d.panes[len(d.panes)-1]
	for _, p := range d.panes {
		if p.view.s == nil {
			target = p
			break
		}
	}
	d.focusPane(target)
	d.open(id)
}

func (d *desk) syncPanes() {
	shown := map[string]bool{}
	for _, p := range d.panes {
		if p.view.s != nil {
			shown[p.view.s.id] = true
		}
	}
	for _, p := range d.panes {
		id := ""
		if p.view.s != nil {
			id = p.view.s.id
		}
		if id != p.lastID {
			d.strip.keepDraft(p.lastID, p.input.Text)
			p.input.SetText(d.strip.draft(id))
			p.lastID = id
		}
		d.syncPaneControls(p)
		if p.view.s != nil {
			d.syncQueue(p.view.s)
		}
		if p.view.s == nil {
			var btns []fyne.CanvasObject
			for _, id := range d.tabs {
				if shown[id] || d.session(id) == nil {
					continue
				}
				id := id
				title, _, _ := d.rowText(id)
				b := widget.NewButton(oneLine(title, 40), func() { d.showInPane(p, id) })
				b.Importance = widget.LowImportance
				btns = append(btns, b)
			}
			p.choices.Objects = btns
			p.choices.Refresh()
			p.picker.Show()
			p.input.Disable()
		} else {
			p.picker.Hide()
			p.input.Enable()
		}
		title := emptyPaneText
		if p.view.s != nil {
			title, _, _ = d.rowText(p.view.s.id)
		}
		if p.title.Text != title {
			p.title.SetText(title)
		}
		syncRemoteReload(p.reload, p.view.s)
		if p == d.focused {
			p.outline.StrokeWidth = 2.5
			p.outline.StrokeColor = colText
		} else {
			p.outline.StrokeWidth = 1
			p.outline.StrokeColor = color.NRGBA{0xff, 0xff, 0xff, 0x1c}
		}
		p.outline.Refresh()
		if p.view.s != nil && p != d.focused {
			p.view.setLive(liveText(p.view.s))
		}
	}
}

func (p *pane) queue(items []*chat.Item) {
	p.pending = append(p.pending, items...)
	if p.timer != nil {
		return
	}
	p.timer = time.AfterFunc(uiFrame, func() {
		p.d.do(func() {
			p.timer = nil
			items := p.pending
			p.pending = nil
			if p.view.s != nil {
				p.view.update(items)
				p.view.setLive(liveText(p.view.s))
				p.d.syncPaneControls(p)
			}
		})
	})
}

func (d *desk) splitButtons() fyne.CanvasObject {
	d.autoSplitCheck()
	d.viewBtn = widget.NewButtonWithIcon("", theme.GridIcon(), d.showViewMenu)
	d.viewBtn.Importance = widget.MediumImportance
	d.syncSplitButtons()
	return d.viewBtn
}

func (d *desk) showViewMenu() {
	cur := max(1, len(d.panes))
	auto := d.autoChk != nil && d.autoChk.Checked
	item := func(label string, checked bool, pick func()) *fyne.MenuItem {
		it := fyne.NewMenuItem(label, pick)
		it.Checked = checked
		return it
	}
	var items []*fyne.MenuItem
	for n := 1; n <= maxPanes; n++ {
		n := n
		items = append(items, item(splitLabel(n), !auto && cur == n, func() { d.pickView(n, false) }))
	}
	items = append(items, item("Auto split", auto, func() { d.pickView(0, true) }))
	d.viewItems = items
	c := d.win.Canvas()
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(d.viewBtn)
	d.viewMenu = widget.NewPopUpMenu(fyne.NewMenu("", items...), c)
	d.viewMenu.ShowAtPosition(pos.Add(fyne.NewPos(0, d.viewBtn.Size().Height)))
}

func (d *desk) pickView(n int, auto bool) {
	if auto {
		d.autoChk.SetChecked(true)
	} else {
		d.autoChk.SetChecked(false)
		d.setSplit(n)
	}
	d.syncSplitButtons()
}

func splitLabel(n int) string {
	return [...]string{"", "Single", "Split 2", "Split 3"}[n]
}

func (d *desk) syncSplitButtons() {
	if d.viewBtn == nil {
		return
	}
	icon := theme.ViewFullScreenIcon()
	if len(d.panes) > 1 {
		icon = theme.GridIcon()
	}
	if d.autoChk != nil && d.autoChk.Checked {
		icon = theme.ViewRefreshIcon()
	}
	if d.viewBtn.Icon != icon {
		d.viewBtn.SetIcon(icon)
	}
}

func (d *desk) autoSplitSync() {
	if d.autoChk == nil || !d.autoChk.Checked || d.autoSyncing {
		return
	}
	d.autoSyncing = true
	defer func() { d.autoSyncing = false }()
	var want []string
	for _, g := range d.groups {
		want = append(want, g.ids...)
	}
	if len(want) > maxPanes {
		want = want[:maxPanes]
	}
	if len(want) < 2 {
		return
	}
	if len(d.panes) != len(want) {
		d.setSplit(len(want))
	}
	wanted := map[string]bool{}
	for _, id := range want {
		wanted[id] = true
	}
	shown := map[string]bool{}
	for _, p := range d.panes {
		if p.view.s != nil && wanted[p.view.s.id] {
			shown[p.view.s.id] = true
		}
	}
	keep := d.focused
	for _, id := range want {
		if shown[id] {
			continue
		}
		for _, p := range d.panes {
			if p.view.s == nil || !wanted[p.view.s.id] {
				d.showInPane(p, id)
				shown[id] = true
				break
			}
		}
	}
	if keep != nil {
		d.focusPane(keep)
	}
}

func (d *desk) sendNowFromPane(p *pane, text string) {
	if p.view.s == nil || strings.TrimSpace(text) == "" && len(p.view.s.queue) == 0 {
		return
	}
	d.focusPane(p)
	d.sendNow(text)
	p.input.SetText("")
}

func (d *desk) syncPaneControls(p *pane) {
	s := p.view.s
	p.quiet = true
	defer func() { p.quiet = false }()
	if s == nil {
		p.model.Disable()
		p.mode.Disable()
		p.backend.Disable()
		p.stop.Disable()
		p.mic.Disable()
		setText(p.stats, "")
		p.input.setSuggestion("")
		return
	}
	p.model.Enable()
	p.mode.Enable()
	p.backend.Enable()
	p.mic.Enable()
	if syncTasksButton(p.tasks, s) && d.mainPane != nil {
		relayoutTree(d.mainPane)
	}
	setOptions(p.model, d.modelOptions(s))
	setOptions(p.mode, modeOptions(s))
	if setBackendButton(p.backend, s) && d.mainPane != nil {
		relayoutTree(d.mainPane)
	}
	if p.model.Selected != s.model {
		p.model.SetSelected(s.model)
	}
	if p.mode.Selected != s.mode {
		p.mode.SetSelected(s.mode)
	}
	if s.running() && (s.conv.Busy || s.proc.Busy()) {
		p.stop.Enable()
	} else {
		p.stop.Disable()
	}
	stats := s.model
	if c := s.conv.Stats; c.ContextUsed > 0 {
		stats = "context " + kfmt(c.ContextUsed)
		if c.ContextMax > 0 {
			stats += " / " + kfmt(c.ContextMax)
		}
		stats += " · " + s.model
	}
	setText(p.stats, stats)
	if p.input.Text == "" {
		p.input.setSuggestion(d.shownSuggestion(s))
	}
}

type splitState struct {
	Panes  []string `json:"panes"`
	Drafts []string `json:"drafts"`
	Focus  int      `json:"focus"`
}

func (d *desk) saveSplit() *splitState {
	if !d.split() {
		return nil
	}
	st := &splitState{}
	for i, p := range d.panes {
		id, draft := "", ""
		if s := p.view.s; s != nil && s.remote == nil { // other PCs: memory only
			id, draft = s.id, d.boxDraft(s, p.input)
		}
		st.Panes = append(st.Panes, id)
		st.Drafts = append(st.Drafts, draft)
		if p == d.focused {
			st.Focus = i
		}
	}
	return st
}

func (d *desk) restoreSplit(st *splitState) {
	if st == nil || len(st.Panes) < 2 {
		return
	}
	d.setSplit(len(st.Panes))
	for i, id := range st.Panes {
		if i >= len(d.panes) {
			break
		}
		p := d.panes[i]
		if id != "" && d.session(id) != nil {
			d.showInPane(p, id)
		}
		if i < len(st.Drafts) && st.Drafts[i] != "" {
			p.input.SetText(st.Drafts[i])
		}
	}
	if st.Focus >= 0 && st.Focus < len(d.panes) {
		d.focusPane(d.panes[st.Focus])
	}
}

func (d *desk) autoSplitCheck() fyne.CanvasObject {
	d.autoChk = widget.NewCheck("Auto split", func(on bool) {
		d.app.Preferences().SetBool("autoSplit", on)
		if on {
			d.preAuto = ""
			if !d.split() && d.current != nil {
				d.preAuto = d.current.id
			}
			d.autoSplitSync()
			d.syncSplitButtons()
			return
		}
		d.leaveAutoSplit()
		d.syncSplitButtons()
	})
	d.autoChk.Checked = d.app.Preferences().Bool("autoSplit")
	return d.autoChk
}

func (d *desk) leaveAutoSplit() {
	back := d.preAuto
	d.preAuto = ""
	if !d.split() {
		return
	}
	d.setSplit(1)
	if back != "" && d.session(back) != nil {
		d.open(back)
	}
}
