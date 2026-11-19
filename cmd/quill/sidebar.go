package main

import (
	"fmt"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	sideW        = 330
	chatShare    = 0.65
	chatMinWidth = 560
	sideAnimTime = 220 * time.Millisecond
)

type slideLayout struct{ d *desk }

func (l slideLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return objs[1].MinSize()
}

func (l slideLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	side, main, edge := objs[0], objs[1], objs[2]
	w := float32(sideW) * l.d.sideFrac
	side.Resize(fyne.NewSize(sideW, size.Height))
	side.Move(fyne.NewPos(w-sideW, 0))
	edge.Resize(fyne.NewSize(1, size.Height))
	edge.Move(fyne.NewPos(w, 0))
	main.Resize(fyne.NewSize(size.Width-w, size.Height))
	main.Move(fyne.NewPos(w, 0))
	if l.d.sideFrac <= 0 {
		side.Hide()
		edge.Hide()
	} else {
		side.Show()
		edge.Show()
	}
}

func (d *desk) build() fyne.CanvasObject {
	d.chat = newChatView(d)
	d.sidebar = d.buildSidebar()
	main := d.buildMain()
	edge := canvas.NewRectangle(color.NRGBA{0x3a, 0x3a, 0x37, 0xff})
	d.sideFrac = 1
	d.root = container.New(slideLayout{d}, d.sidebar, main, edge)
	if d.app.Preferences().Bool("sidebarHidden") {
		d.sideFrac = 0
		d.sideOff = true
		d.expandBtn.Show()
	}
	return d.root
}

func (d *desk) sidebarWidth() float32 {
	return float32(sideW) * d.sideFrac
}

func (d *desk) chatWidth() float32 {
	if d.root == nil || d.root.Size().Width <= 0 {
		return chatMaxWidth
	}
	free := d.root.Size().Width - d.sidebarWidth()
	return max(chatMinWidth, free*chatShare)
}

func (d *desk) setSidebar(show bool) {
	d.sideOff = !show
	d.app.Preferences().SetBool("sidebarHidden", !show)
	if show {
		d.expandBtn.Hide()
	}
	from, to := d.sideFrac, float32(0)
	if show {
		to = 1
	}
	if d.sideAnim != nil {
		d.sideAnim.Stop()
	}
	step := func(p float32) {
		d.sideFrac = from + (to-from)*p
		d.root.Refresh()
		d.mainPane.Refresh()
		d.chat.relayout()
		if p >= 1 && !show {
			d.expandBtn.Show()
		}
	}
	if !d.animate {
		step(1)
		return
	}
	d.sideAnim = fyne.NewAnimation(sideAnimTime, step)
	d.sideAnim.Curve = fyne.AnimationEaseInOut
	d.sideAnim.Start()
}

func smallArrow(res fyne.Resource, onTap func()) *tapRow {
	icon := widget.NewIcon(theme.NewPrimaryThemedResource(res))
	ring := canvas.NewRectangle(color.NRGBA{0xd9, 0x77, 0x57, 0x30})
	ring.CornerRadius = 6
	r := newTapRow(container.NewCenter(sized(22, 22, container.NewStack(ring, container.NewPadded(icon)))), onTap)
	r.icon = icon
	return r
}

const inactiveAfter = 7 * 24 * time.Hour

type foldSection struct {
	d     *desk
	title string
	rows  []sideRow
	list  *widget.List
	btn   *widget.Button
	area  *fyne.Container
	box   *fyne.Container
	open  bool
}

func (d *desk) newFold(title string) *foldSection {
	f := &foldSection{title: title, d: d}
	f.list = d.newSessionList(&f.rows)
	f.btn = widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() {
		f.open = !f.open
		d.rebuildRows()
	})
	f.btn.Alignment = widget.ButtonAlignLeading
	f.btn.Importance = widget.LowImportance
	f.area = container.NewStack(sized(0, 220, canvas.NewRectangle(color.Transparent)), f.list)
	f.area.Hide()
	f.box = container.NewVBox(f.btn, f.area)
	return f
}

func (f *foldSection) refresh() {
	icon := theme.NavigateNextIcon()
	if f.open {
		icon = theme.MenuDropDownIcon()
	}
	f.btn.SetIcon(icon)
	f.btn.SetText(fmt.Sprintf("%s (%d)", f.title, len(f.rows)))
	was := f.box.Visible() && f.area.Visible()
	if len(f.rows) == 0 {
		f.box.Hide()
	} else {
		f.box.Show()
		if f.open {
			f.area.Show()
		} else {
			f.area.Hide()
		}
	}
	if now := f.box.Visible() && f.area.Visible(); now != was && f.d.sideBody != nil {
		f.d.sideBody.Refresh()
	}
}

func (d *desk) isInactive(id string, last time.Time) bool {
	if last.IsZero() || time.Since(last) < inactiveAfter {
		return false
	}
	if d.current != nil && d.current.id == id {
		return false
	}
	if s := d.sessions[id]; s != nil && (s.running() || s.terminal != nil) {
		return false
	}
	_, held := d.terms[id]
	return !held
}
