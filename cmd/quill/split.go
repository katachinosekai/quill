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
	picker  *fyne.Container
	choices *fyne.Container

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
	p.input.onSendNow = func(text string) { d.sendFromPane(p, text) }
	p.input.SetMinRowsVisible(2)
	p.input.hint = "Message Claude"
	p.input.SetPlaceHolder(p.input.hint)
	sendBtn := widget.NewButtonWithIcon("", theme.MailSendIcon(), func() { d.sendFromPane(p, p.input.Text) })
	sendBtn.Importance = widget.HighImportance
	inputRow := bg(colInput, 10, container.NewBorder(nil, nil, nil, container.NewVBox(layout.NewSpacer(), sendBtn), p.input))
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
		d.panes = nil
		d.chat = d.single
		d.chatHost.Objects = []fyne.CanvasObject{d.single.root}
		d.chatHost.Refresh()
		d.focused = nil
		if cur != nil {
			d.chat.show(cur)
		} else {
			d.chat.show(nil)
		}
		d.afterSplit()
		return
	}
	if !d.split() {
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
	objs := []fyne.CanvasObject{container.NewCenter(mutedText("View", 11))}
	for _, b := range btns {
		objs = append(objs, b)
	}
	d.autoChk = widget.NewCheck("Auto split", func(on bool) {
		d.app.Preferences().SetBool("autoSplit", on)
		if on {
			d.autoSplitSync()
		}
	})
	d.autoChk.Checked = d.app.Preferences().Bool("autoSplit")
	objs = append(objs, d.autoChk)
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
