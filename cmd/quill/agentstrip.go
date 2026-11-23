package main

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// Subagent strip (topic tabs §3.4, decision 13; kmem task t4cb4ee): a thin
// row under the tab header, in both layouts, listing the current session's
// subagents with their running time and tokens so far:
//
//	● Fix join stall (Explore) · 41k tok · 6 min   ● ELTF build · 2 min   +3
//
// Finished ones stay greyed for a while; the row hides when nothing ran
// lately. A chip opens a menu with the agent's details, its log and "Stop
// this subagent" (TaskStop only, kmem task tf0ac88). The "Background
// Task(s)" button and "Stop everything" stay; the strip complements them.
//
// The data comes from internal/chat (Conversation.Agents, fed by the CLI's
// task_started / task_progress / task_notification events). The strip polls
// it once a second on the UI thread and only touches widgets when the text
// changed, so an idle strip costs a map length check.

const (
	agentStripMax  = 3                // chips before "+N"
	agentStripTick = time.Second      // poll interval
	agentKeepShown = 10 * time.Minute // a finished agent stays greyed beside running ones
	agentKeepAlone = time.Minute      // … and this long once none runs
)

type agentStrip struct {
	d     *desk
	root  *fyne.Container // hidden while nothing is listed
	row   *fyne.Container
	col   *fyne.Container // the row inside the chat-width column
	shown string          // the texts on screen, to skip unchanged refreshes
	now   func() time.Time
}

func newAgentStrip(d *desk) *agentStrip {
	a := &agentStrip{d: d, now: time.Now}
	a.row = container.NewHBox()
	// The chat column's width and shift (chatview.go), so the strip sits over
	// the chat: chips centred with centring on, else on its left edge.
	a.col = windowCentered(d.chatWidth, d.sidebarWidth, container.New(&stripLayout{center: func() bool { return d.center }}, a.row))
	a.root = container.New(layout.NewCustomPaddedLayout(0, 2, 8, 8), a.col)
	a.root.Hide()
	a.schedule()
	return a
}

func (a *agentStrip) schedule() {
	time.AfterFunc(agentStripTick, func() {
		a.d.do(func() {
			a.sync()
			a.schedule()
		})
	})
}

// listed: the agents the strip shows for s, running ones first (oldest
// first), then finished ones (latest first).
func (a *agentStrip) listed(s *deskSession) []chat.AgentUse {
	if s == nil || s.conv == nil {
		return nil
	}
	all := s.conv.Agents()
	if len(all) == 0 {
		return nil
	}
	now := a.now()
	var run, done []chat.AgentUse
	for _, g := range all {
		if g.Running() {
			run = append(run, g)
		}
	}
	keep := agentKeepAlone
	if len(run) > 0 {
		keep = agentKeepShown
	}
	for i := len(all) - 1; i >= 0; i-- {
		if g := all[i]; !g.Running() && now.Sub(g.Done) < keep {
			done = append(done, g)
		}
	}
	return append(run, done...)
}

func agentDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1 min"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d/time.Minute))
	}
	return fmt.Sprintf("%d h %d min", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

func agentName(g chat.AgentUse) string {
	name := strings.TrimSpace(g.Name)
	if name == "" {
		name = "Subagent"
	}
	name = oneLine(name, 40)
	if g.Type != "" && g.Type != "general-purpose" {
		name += " (" + g.Type + ")"
	}
	return name
}

// agentChipText: "● part A (Explore) · 41k tok · 6 min".
func agentChipText(g chat.AgentUse, now time.Time) string {
	mark := "●"
	end := now
	if !g.Running() {
		mark, end = "✓", g.Done
		if g.Status != "completed" {
			mark = "✕"
		}
	}
	parts := []string{mark + " " + agentName(g)}
	if g.Tokens > 0 {
		parts = append(parts, kfmt(g.Tokens)+" tok")
	}
	parts = append(parts, agentDur(end.Sub(g.Started)))
	return strings.Join(parts, " · ")
}

func (a *agentStrip) sync() {
	s := a.d.current
	if (s == nil || s.conv == nil || s.conv.AgentsRev() == 0) && !a.root.Visible() {
		return
	}
	list := a.listed(s)
	now := a.now()
	texts := make([]string, 0, len(list)+1)
	for i, g := range list {
		if i == agentStripMax && len(list) > agentStripMax+1 {
			texts = append(texts, fmt.Sprintf("+%d", len(list)-agentStripMax))
			break
		}
		texts = append(texts, agentChipText(g, now))
	}
	sig := fmt.Sprintf("%p|", s) + strings.Join(texts, "\x00")
	if len(list) == 0 {
		sig = ""
	}
	if sig == a.shown {
		return
	}
	a.shown = sig
	wasVisible := a.root.Visible()
	objs := make([]fyne.CanvasObject, 0, len(texts))
	for i, t := range texts {
		if strings.HasPrefix(t, "+") && i == agentStripMax {
			rest := list[agentStripMax:]
			objs = append(objs, newAgentChip(t, colMuted, func(c fyne.CanvasObject) { a.moreMenu(c, s, rest) }))
			break
		}
		g := list[i]
		col := color.Color(colIdle)
		if !g.Running() {
			col = colMuted
		}
		objs = append(objs, newAgentChip(t, col, func(c fyne.CanvasObject) { menuAbove(c, a.items(s, g)) }))
	}
	a.row.Objects = objs
	a.row.Refresh()
	if len(objs) > 0 {
		a.root.Show()
	} else {
		a.root.Hide()
	}
	if a.root.Visible() != wasVisible && a.d.headerBox != nil {
		a.d.headerBox.Refresh()
		if a.d.mainPane != nil {
			relayout(a.d.mainPane)
		}
	}
}

// items: one agent's menu.
func (a *agentStrip) items(s *deskSession, g chat.AgentUse) []*fyne.MenuItem {
	info := func(t string) *fyne.MenuItem {
		it := fyne.NewMenuItem(t, func() {})
		it.Disabled = true
		return it
	}
	out := []*fyne.MenuItem{info(agentName(g))}
	use := kfmt(g.Tokens) + " tokens"
	if g.ToolUses > 0 {
		use += fmt.Sprintf(" · %d tool uses", g.ToolUses)
	}
	if g.LastTool != "" && g.Running() {
		use += " · last " + g.LastTool
	}
	out = append(out, info(use))
	if g.Running() {
		out = append(out, info("Running for "+agentDur(a.now().Sub(g.Started))))
	} else {
		st := g.Status
		if st == "" {
			st = "finished"
		}
		out = append(out, info(strings.ToUpper(st[:1])+st[1:]+" after "+agentDur(g.Done.Sub(g.Started))))
	}
	if g.Log != "" {
		log := g.Log
		out = append(out, fyne.NewMenuItem("Open log", func() { a.d.openPath(log) }))
	}
	if g.Running() && g.Background && g.ID != g.ToolID {
		id := g.ID
		out = append(out, fyne.NewMenuItemSeparator(), fyne.NewMenuItem("Stop this subagent", func() {
			a.d.sendTo(s, "Please stop background task "+id+" with TaskStop.")
		}))
	}
	return out
}

func (a *agentStrip) moreMenu(c fyne.CanvasObject, s *deskSession, rest []chat.AgentUse) {
	now := a.now()
	var out []*fyne.MenuItem
	for _, g := range rest {
		g := g
		it := fyne.NewMenuItem(agentChipText(g, now), nil)
		it.ChildMenu = fyne.NewMenu("", a.items(s, g)...)
		out = append(out, it)
	}
	menuAbove(c, out)
}

// agentChip: a small tappable text on a tinted pill.
type agentChip struct {
	widget.BaseWidget
	text  *canvas.Text
	col   color.Color
	onTap func(fyne.CanvasObject)
}

func newAgentChip(text string, col color.Color, onTap func(fyne.CanvasObject)) *agentChip {
	t := canvas.NewText(text, col)
	t.TextSize = fontMeta
	c := &agentChip{text: t, col: col, onTap: onTap}
	c.ExtendBaseWidget(c)
	return c
}

func (c *agentChip) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(bgPad(fade(c.col, 0.12), 8, 2, 8, c.text))
}

func (c *agentChip) Tapped(*fyne.PointEvent) {
	if c.onTap != nil {
		c.onTap(c)
	}
}

func (c *agentChip) Cursor() desktop.Cursor { return desktop.PointerCursor }

// stripLayout puts the chip row at its natural width (never wider than the
// column), centred or at the column's left edge.
type stripLayout struct{ center func() bool }

func (*stripLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var h float32
	for _, o := range objs {
		h = max(h, o.MinSize().Height)
	}
	return fyne.NewSize(10, h)
}

func (l *stripLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	pad := theme.Padding()
	for _, o := range objs {
		w := min(o.MinSize().Width, size.Width-2*pad)
		x := pad
		if l.center != nil && l.center() {
			x = (size.Width - w) / 2
		}
		o.Move(fyne.NewPos(x, 0))
		o.Resize(fyne.NewSize(w, size.Height))
	}
}
