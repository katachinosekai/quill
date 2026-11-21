package main

import (
	"image/color"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	sideW            = 330
	defaultChatShare = 0.65
	chatMinWidth     = 560
	sideAnimTime     = 220 * time.Millisecond
)

type slideLayout struct{ d *desk }

func (l slideLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return objs[1].MinSize()
}

func (l slideLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	l.d.noteResize(size)
	side, main, edge, toggle := objs[0], objs[1], objs[2], objs[3]
	w := float32(sideW) * l.d.sideFrac
	ts := toggle.MinSize()
	toggle.Resize(ts)
	toggle.Move(fyne.NewPos(max(8, w-ts.Width-10), 18))
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
	d.single = d.chat
	d.sidebar = d.buildSidebar()
	main := d.buildMain()
	edge := canvas.NewRectangle(color.NRGBA{0x3a, 0x3a, 0x37, 0xff})
	d.sideFrac = 1
	d.sideToggle = smallArrow(theme.NavigateBackIcon(), func() { d.setSidebar(d.sideOff) })
	d.root = container.New(slideLayout{d}, d.sidebar, main, edge, d.sideToggle)
	defer d.applyLayoutMode()
	if d.app.Preferences().Bool("sidebarHidden") {
		d.sideFrac = 0
		d.sideOff = true
		d.syncToggle()
	}
	return container.NewStack(newBgTap(d.clearSelections), d.root)
}

func (d *desk) syncToggle() {
	res := theme.NavigateBackIcon()
	pad := float32(0)
	if d.sideOff {
		res = theme.NavigateNextIcon()
		pad = 34
	}
	d.sideToggle.icon.SetResource(theme.NewPrimaryThemedResource(res))
	d.headPad.SetMinSize(fyne.NewSize(pad, 1))
}

func (d *desk) sidebarWidth() float32 {
	return float32(sideW) * d.sideFrac
}

func (d *desk) chatWidth() float32 {
	if d.chatFreeze > 0 {
		return d.chatFreeze
	}
	if d.resizeFreeze > 0 {
		return d.resizeFreeze
	}
	if d.root == nil || d.root.Size().Width <= 0 {
		return chatMaxWidth
	}
	return d.chatWidthFor(d.sidebarWidth())
}

const (
	defaultInputShare = 0.45
	inputMinWidth     = 640
	minChatShare      = 0.4
	minInputShare     = 0.25
)

func (d *desk) inputWidth() float32 {
	if d.split() {
		return fill
	}
	chat := d.chatWidth()
	if d.root == nil || d.root.Size().Width <= 0 {
		return chat
	}
	return min(chat, max(d.root.Size().Width*d.inputShare(), inputMinWidth))
}

func (d *desk) chatWidthFor(side float32) float32 {
	return max(chatMinWidth, (d.root.Size().Width-side)*d.chatShare())
}

func (d *desk) setSidebar(show bool) {
	d.sideOff = !show
	if d.layoutMode() == layoutVertical {
		d.app.Preferences().SetBool("sidebarHidden", !show)
	}
	from, to := d.sideFrac, float32(0)
	if show {
		to = 1
	}
	if d.sideAnim != nil {
		d.sideAnim.Stop()
	}
	d.syncToggle()
	if d.root.Size().Width > 0 {
		d.chatFreeze = d.chatWidthFor(float32(sideW) * to)
	}
	relayout(d.mainPane)
	d.chat.relayout()
	step := func(p float32) {
		d.sideFrac = from + (to-from)*p
		relayout(d.root)
		if p >= 1 {
			d.chatFreeze = 0
			relayout(d.mainPane)
			d.chat.relayout()
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

func (d *desk) isInactive(id string, last time.Time) bool {
	if last.IsZero() || time.Since(last) < d.rules.archiveAfter() {
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

func (d *desk) isActive(id string, last time.Time) bool {
	if d.current != nil && d.current.id == id {
		return true
	}
	if s := d.sessions[id]; s != nil && (s.running() || s.terminal != nil || s.takeover) {
		return true
	}
	if _, held := d.terms[id]; held {
		return true
	}
	return !last.IsZero() && time.Since(last) < d.rules.activeFor()
}

type sideEntry struct {
	id   string
	cwd  string
	last time.Time
}

type folderGroup struct {
	cwd   string
	items []sideEntry
}

func groupByFolder(es []sideEntry) []folderGroup {
	idx := map[string]int{}
	var groups []folderGroup
	for _, e := range es {
		i, ok := idx[e.cwd]
		if !ok {
			i = len(groups)
			idx[e.cwd] = i
			groups = append(groups, folderGroup{cwd: e.cwd})
		}
		groups[i].items = append(groups[i].items, e)
	}
	latest := func(g folderGroup) time.Time {
		var t time.Time
		for _, e := range g.items {
			if e.last.After(t) {
				t = e.last
			}
		}
		return t
	}
	for _, g := range groups {
		sort.SliceStable(g.items, func(i, j int) bool { return g.items[i].last.After(g.items[j].last) })
	}
	sort.SliceStable(groups, func(i, j int) bool {
		a, b := latest(groups[i]), latest(groups[j])
		if !a.Equal(b) {
			return a.After(b)
		}
		return groups[i].cwd < groups[j].cwd
	})
	return groups
}

func sortRecent(es []sideEntry) []sideEntry {
	sort.SliceStable(es, func(i, j int) bool { return es[i].last.After(es[j].last) })
	return es
}

func (d *desk) sortedFolders(custom map[string][]sideEntry) []orgFolder {
	latest := func(f orgFolder) time.Time {
		var t time.Time
		for _, e := range custom[folderPrefix+f.ID] {
			if e.last.After(t) {
				t = e.last
			}
		}
		return t
	}
	out := append([]orgFolder(nil), d.org.Folders...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := latest(out[i]), latest(out[j])
		if !a.Equal(b) {
			return a.After(b)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (d *desk) chatShare() float32 {
	return d.widthShare("chatShare", defaultChatShare, minChatShare)
}

func (d *desk) inputShare() float32 {
	return d.widthShare("inputShare", defaultInputShare, minInputShare)
}

func (d *desk) widthShare(pref string, def, lo float32) float32 {
	if v, ok := d.shares[pref]; ok {
		return v
	}
	v := float32(d.app.Preferences().FloatWithFallback(pref, float64(def)))
	v = max(lo, min(1, v))
	if d.shares == nil {
		d.shares = map[string]float32{}
	}
	d.shares[pref] = v
	return v
}

func (d *desk) setShare(pref string, v float32) {
	if d.shares == nil {
		d.shares = map[string]float32{}
	}
	d.shares[pref] = v
	if d.chat != nil {
		d.chat.relayout()
	}
	if d.bottomBox != nil {
		relayout(d.bottomBox)
	}
	if d.mainPane != nil {
		relayout(d.mainPane)
		relayout(d.usageBox)
	}
}

const resizeSettle = 180 * time.Millisecond

func (d *desk) noteResize(size fyne.Size) {
	if d.lastRootSize == size {
		return
	}
	first := d.lastRootSize.IsZero()
	d.lastRootSize = size
	if first || !d.animate || d.root == nil || d.chatFreeze > 0 {
		return
	}
	if d.resizeFreeze == 0 {
		d.resizeFreeze = d.chatWidthFor(d.sidebarWidth())
	}
	if d.resizeTimer != nil {
		d.resizeTimer.Stop()
	}
	d.resizeTimer = time.AfterFunc(resizeSettle, func() {
		d.do(func() {
			d.resizeFreeze = 0
			d.resizeTimer = nil
			if d.mainPane != nil {
				relayout(d.mainPane)
			}
			d.chat.relayout()
		})
	})
}
