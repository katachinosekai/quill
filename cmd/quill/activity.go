package main

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

const activityHeight = 260

type activity struct {
	d      *desk
	v      *chatView
	root   *fyne.Container
	body   *fyne.Container
	scroll *container.Scroll
	status *widget.Label
	arrow  *tapRow
	open   bool
	from   int
}

func isStep(it *chat.Item) bool {
	return it.Kind == chat.Tool
}

func newActivity(d *desk) *activity {
	a := &activity{d: d, open: d.app.Preferences().Bool("activityOpen")}
	a.v = &chatView{d: d, views: map[int]*itemView{}, steps: true}
	a.v.box = container.NewVBox()
	a.scroll = container.NewVScroll(a.v.box)
	a.scroll.SetMinSize(fyne.NewSize(0, activityHeight))
	a.status = widget.NewLabel("")
	a.status.Importance = widget.MediumImportance
	a.status.Truncation = fyne.TextTruncateEllipsis
	a.arrow = smallArrow(theme.MenuDropDownIcon(), func() { a.setOpen(!a.open) })
	a.body = container.NewStack(a.scroll)
	head := container.NewBorder(nil, nil, a.arrow, nil, a.status)
	r := canvas.NewRectangle(colCard)
	r.CornerRadius = 12
	a.root = container.NewStack(container.NewThemeOverride(container.NewStack(r, container.NewPadded(container.NewBorder(head, nil, nil, nil, a.body))), smallTheme{}))
	a.root.Hide()
	if !a.open {
		a.body.Hide()
		a.arrow.icon.SetResource(theme.NewPrimaryThemedResource(theme.NavigateNextIcon()))
	}
	a.v.scroll = a.scroll
	return a
}

func (a *activity) setOpen(open bool) {
	a.open = open
	a.d.app.Preferences().SetBool("activityOpen", open)
	a.layout()
}

func (a *activity) layout() {
	icon := theme.MenuDropDownIcon()
	if !a.open {
		icon = theme.NavigateNextIcon()
	}
	a.arrow.icon.SetResource(theme.NewPrimaryThemedResource(icon))
	if a.open {
		a.body.Show()
	} else {
		a.body.Hide()
	}
	a.d.mainPane.Refresh()
}

func turnStart(c *chat.Conversation) int {
	for i := len(c.Items) - 1; i >= 0; i-- {
		if c.Items[i].Kind == chat.User {
			return i
		}
	}
	return 0
}

func (a *activity) show(s *deskSession) {
	a.v.s = s
	a.v.views = map[int]*itemView{}
	a.v.box.RemoveAll()
	if s == nil {
		a.root.Hide()
		return
	}
	a.from = turnStart(s.conv)
	a.fill(s.conv.Items[a.from:])
}

func (a *activity) fill(items []*chat.Item) {
	for _, it := range items {
		if isStep(it) {
			a.v.box.Add(a.v.viewFor(it).holder)
		}
	}
	a.v.box.Refresh()
	a.refreshStatus()
	a.scroll.ScrollToBottom()
}

func (a *activity) update(items []*chat.Item) {
	s := a.v.s
	if s == nil {
		return
	}
	for _, it := range items {
		if it.Kind == chat.User {
			a.show(s)
			return
		}
	}
	stick := a.scroll.Offset.Y+a.scroll.Size().Height >= a.v.box.MinSize().Height-40
	var fresh []*chat.Item
	for _, it := range items {
		if !isStep(it) {
			continue
		}
		if iv, ok := a.v.views[it.Seq]; ok {
			a.v.refreshItem(it, iv)
		} else {
			fresh = append(fresh, it)
		}
	}
	for _, it := range fresh {
		a.v.box.Add(a.v.viewFor(it).holder)
	}
	if len(fresh) > 0 {
		a.v.box.Refresh()
	}
	a.refreshStatus()
	if stick {
		a.scroll.ScrollToBottom()
	}
}

func (a *activity) focus(it *chat.Item) {
	s := a.v.s
	if s == nil {
		return
	}
	start := 0
	for i, x := range s.conv.Items {
		if x.Kind == chat.User && x.Seq <= it.Seq {
			start = i
		}
	}
	a.v.views = map[int]*itemView{}
	a.v.box.RemoveAll()
	a.from = start
	a.fill(s.conv.Items[start:nextUser(s.conv, start)])
	if !a.open {
		a.setOpen(true)
	}
	if iv, ok := a.v.views[it.Seq]; ok {
		pos := iv.holder.Position()
		a.scroll.Offset = fyne.NewPos(0, pos.Y)
		a.scroll.Refresh()
	}
}

func nextUser(c *chat.Conversation, start int) int {
	for i := start + 1; i < len(c.Items); i++ {
		if c.Items[i].Kind == chat.User {
			return i
		}
	}
	return len(c.Items)
}

func (a *activity) refreshStatus() {
	s := a.v.s
	n := len(a.v.views)
	if s == nil || n == 0 && !(s.conv.Busy && s.conv.Thinking) {
		a.root.Hide()
		a.d.mainPane.Refresh()
		return
	}
	var last *chat.Item
	for _, it := range s.conv.Items[a.from:] {
		if isStep(it) {
			last = it
		}
	}
	text := fmt.Sprintf("%d steps", n)
	switch {
	case s.conv.Busy && s.conv.Thinking:
		text = "✻ Thinking...  ·  " + text
	case s.conv.Busy && last != nil && last.Kind == chat.Tool && !last.HasResult:
		text = "● " + oneLine(toolHeader(last), 90) + "  ·  " + text
	case s.conv.Busy:
		text = "Working...  ·  " + text
	}
	a.status.SetText(text)
	if !a.root.Visible() {
		a.root.Show()
		a.d.mainPane.Refresh()
	}
}
