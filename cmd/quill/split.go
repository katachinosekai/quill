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
	d       *desk
	view    *chatView
	title   *widget.Label
	outline *canvas.Rectangle
	root    fyne.CanvasObject
	input   *sendEntry
	lastID  string
	picker  *fyne.Container
	choices *fyne.Container
	model   *widget.Select
	mode    *widget.Select
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
	head := container.NewBorder(nil, nil, padLeft(4), closeBtn, p.title)
	p.outline = canvas.NewRectangle(color.Transparent)
	p.outline.CornerRadius = 14
	p.outline.StrokeWidth = 1
	p.outline.StrokeColor = color.NRGBA{0xff, 0xff, 0xff, 0x1c}
	p.input = newSendEntry(func(text string) { d.sendFromPane(p, text) })
	p.input.onSendNow = func(text string) { d.sendNowFromPane(p, text) }
	p.input.SetMinRowsVisible(2)
	p.input.hint = "Message Claude"
	p.input.SetPlaceHolder(p.input.hint)
	p.input.onShortcut = d.handleShortcut
	p.input.onHistory = func(k *fyne.KeyEvent) bool { return d.historyKey(p.input, p.view.s, k) }
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
	openAll := widget.NewButtonWithIcon("", theme.MenuDropDownIcon(), func() {
		d.focusPane(p)
		d.setAllChips(true)
	})
	openAll.Importance = widget.LowImportance
	closeAll := widget.NewButtonWithIcon("", theme.MenuDropUpIcon(), func() {
		d.focusPane(p)
		d.setAllChips(false)
	})
	closeAll.Importance = widget.LowImportance
	controls := container.NewHBox(p.model, p.mode, layout.NewSpacer(), openAll, closeAll)
	side := container.NewVBox(p.stop, layout.NewSpacer(), p.mic, sendBtn)
	card := bg(colInput, 10, container.NewBorder(nil, container.NewThemeOverride(controls, compactTheme{}), nil, side, p.input))
	p.stats = mutedText("", fontMeta)
	inputRow := container.NewVBox(card, container.New(&clipLayout{}, p.stats))
	p.choices = container.NewVBox()
	hint := widget.NewLabelWithStyle(emptyPaneText, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	p.picker = container.NewCenter(container.NewVBox(hint, p.choices))
	body := container.NewBorder(head, container.NewPadded(inputRow), nil, nil, container.NewStack(p.view.root, p.picker))
	box := &paneBox{p: p, content: container.NewStack(p.outline, container.NewPadded(body))}
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
	if s := d.session(id); s != nil {
		if other := d.paneFor(s); other != nil && other != p {
			d.focusPane(other)
			return
		}
	}
	d.focusPane(p)
	d.open(id)
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
		d.chatHost.Objects = []fyne.CanvasObject{d.single.root}
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
	defer d.syncLimitBanner()
	if d.bottomBox != nil {
		if d.split() {
			d.bottomBox.Hide()
			d.sizesCard.Hide()
		} else {
			d.bottomBox.Show()
			d.sizesCard.Show()
		}
	}
	d.syncPanes()
	d.syncSplitButtons()
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
	var btns []*widget.Button
	for n := 1; n <= maxPanes; n++ {
		b := widget.NewButton(splitLabel(n), func() { d.setSplit(n) })
		b.Importance = widget.LowImportance
		btns = append(btns, b)
	}
	d.splitBtns = btns
	d.syncSplitButtons()
	objs := []fyne.CanvasObject{container.NewCenter(mutedText("View", fontMeta))}
	for _, b := range btns {
		objs = append(objs, b)
	}

	return container.NewHBox(objs...)
}

func splitLabel(n int) string {
	return [...]string{"", "Single", "Split 2", "Split 3"}[n]
}

func (d *desk) syncSplitButtons() {
	n := max(1, len(d.panes))
	for i, b := range d.splitBtns {
		imp := widget.LowImportance
		if i+1 == n {
			imp = widget.HighImportance
		}
		if b.Importance != imp {
			b.Importance = imp
			b.Refresh()
		}
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
		if g.key == placeActive {
			want = g.ids
		}
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
		p.stop.Disable()
		p.mic.Disable()
		setText(p.stats, "")
		p.input.setSuggestion("")
		return
	}
	p.model.Enable()
	p.mode.Enable()
	p.mic.Enable()
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
		p.input.setSuggestion(s.conv.Suggestion)
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
		id := ""
		if p.view.s != nil {
			id = p.view.s.id
		}
		st.Panes = append(st.Panes, id)
		st.Drafts = append(st.Drafts, p.input.Text)
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
			d.autoSplitSync()
		}
	})
	d.autoChk.Checked = d.app.Preferences().Bool("autoSplit")
	return d.autoChk
}
