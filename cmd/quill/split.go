package main

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
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
	p.title = widget.NewLabel("Pick a session in the sidebar")
	p.title.TextStyle.Bold = true
	p.title.Truncation = fyne.TextTruncateEllipsis
	closeBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() { d.closePane(p) })
	closeBtn.Importance = widget.LowImportance
	head := container.NewBorder(nil, nil, padLeft(4), closeBtn, p.title)
	p.outline = canvas.NewRectangle(color.Transparent)
	p.outline.CornerRadius = 14
	p.outline.StrokeWidth = 1
	p.outline.StrokeColor = color.NRGBA{0xff, 0xff, 0xff, 0x1c}
	body := container.NewBorder(head, nil, nil, nil, p.view.root)
	box := &paneBox{p: p, content: container.NewStack(p.outline, container.NewPadded(body))}
	box.ExtendBaseWidget(box)
	p.root = box
	return p
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
	d.syncPanes()
	d.syncSplitButtons()
	if d.bottomBox != nil {
		d.bottomBox.Refresh()
	}
	if d.mainPane != nil {
		d.mainPane.Refresh()
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
	d.win.Canvas().Focus(d.input)
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
	for _, p := range d.panes {
		title := "Pick a session in the sidebar"
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
