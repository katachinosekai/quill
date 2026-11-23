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
// subagents by description (time, tokens, type and model in the tooltip):
//
//	Fix join stall   ELTF build   +3
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
	tips  *agentTipHost // the one tooltip all chips share
}

func newAgentStrip(d *desk) *agentStrip {
	if d.tipLayer == nil {
		d.tipLayer = container.NewWithoutLayout()
	}
	a := &agentStrip{d: d, now: time.Now, tips: newAgentTipHost(d.tipLayer)}
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

// agentDescMax: the description's width in a chip, in characters.
const agentDescMax = 32

// agentName: the agent's full description on one line, the type when the
// call gave none.
func agentName(g chat.AgentUse) string {
	name := strings.Join(strings.Fields(g.Name), " ")
	if name == "" {
		name = g.Type
	}
	if name == "" {
		name = "Subagent"
	}
	return name
}

// cutTo shortens s to n characters, ending in "…" when it was cut.
func cutTo(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return strings.TrimRight(string(r[:n-1]), " ") + "…"
	}
	return s
}

// agentTip: the chip's tooltip: the full description, then type and model,
// then the state, time and tokens.
func agentTip(g chat.AgentUse, now time.Time) string {
	lines := []string{agentName(g)}
	var more []string
	if g.Type != "" {
		more = append(more, g.Type)
	}
	if g.Model != "" {
		more = append(more, g.Model)
	}
	if len(more) > 0 {
		lines = append(lines, strings.Join(more, " · "))
	}
	if g.Running() && g.LastTool != "" {
		lines = append(lines, "now: "+g.LastTool)
	}
	st := "Running for " + agentDur(now.Sub(g.Started))
	if !g.Running() {
		w := g.Status
		if w == "" {
			w = "finished"
		}
		st = strings.ToUpper(w[:1]) + w[1:] + " after " + agentDur(g.Done.Sub(g.Started))
	}
	if g.Tokens > 0 {
		st += " · " + kfmt(g.Tokens) + " tok"
	}
	return strings.Join(append(lines, st), "\n")
}

// agentChipText: the status mark (● running, ✓ done, ✕ failed) and the
// description, cut to the chip width; time and tokens are in the tooltip.
func agentChipText(g chat.AgentUse, _ time.Time) string {
	mark := "●"
	if !g.Running() {
		mark = "✓"
		if g.Status != "" && g.Status != "completed" {
			mark = "✕"
		}
	}
	return mark + " " + cutTo(agentName(g), agentDescMax)
}

func (a *agentStrip) sync() {
	// A hovered chip's tooltip follows time and tokens in place (no resize).
	for _, o := range a.row.Objects {
		if c, ok := o.(*agentChip); ok {
			c.updateTip()
		}
	}
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
			objs = append(objs, a.chip(t, colMuted, func(c fyne.CanvasObject) { a.moreMenu(c, s, rest) }, nil))
			break
		}
		g := list[i]
		col := color.Color(colIdle)
		if !g.Running() {
			col = colMuted
		}
		objs = append(objs, a.chip(t, col, func(c fyne.CanvasObject) { menuAbove(c, a.items(s, g)) }, func() string { return agentTip(g, a.now()) }))
	}
	a.tips.hideNow() // a rebuilt chip must not leave its tooltip up
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

// chip: a chip sharing the strip's tooltip.
func (a *agentStrip) chip(text string, col color.Color, onTap func(fyne.CanvasObject), tip func() string) *agentChip {
	c := newAgentChip(text, col, onTap, tip)
	c.host = a.tips
	return c
}

// items: one agent's menu.
func (a *agentStrip) items(s *deskSession, g chat.AgentUse) []*fyne.MenuItem {
	info := func(t string) *fyne.MenuItem {
		it := fyne.NewMenuItem(t, func() {})
		it.Disabled = true
		return it
	}
	out := []*fyne.MenuItem{info(agentName(g))}
	if g.Type != "" && g.Type != agentName(g) {
		out = append(out, info(g.Type))
	}
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

// agentChip: a small tappable text on a tinted pill. The whole pill, padding
// included, is the hover area; the tooltip lives in a agentTipHost shared by
// the strip's chips.
type agentChip struct {
	widget.BaseWidget
	text  *canvas.Text
	col   color.Color
	onTap func(fyne.CanvasObject)
	tip   func() string // the tooltip text, built on hover (times move)
	host  *agentTipHost
}

func newAgentChip(text string, col color.Color, onTap func(fyne.CanvasObject), tip func() string) *agentChip {
	t := canvas.NewText(text, col)
	t.TextSize = fontMeta
	c := &agentChip{text: t, col: col, onTap: onTap, tip: tip, host: newAgentTipHost(nil)}
	c.ExtendBaseWidget(c)
	return c
}

func (c *agentChip) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(bgPad(fade(c.col, 0.12), 8, 2, 8, c.text))
}

func (c *agentChip) Tapped(*fyne.PointEvent) {
	c.host.hideNow()
	if c.onTap != nil {
		c.onTap(c)
	}
}

// agentTipMinText: the widest state line a tooltip normally shows, so its
// width has a stable floor however short the first text is.
const agentTipMinText = "Completed after 99 h 59 min · 999.9k tok"

// agentTipMaxChars: the longest line a tooltip shows (longer ones are cut).
const agentTipMaxChars = 72

// agentTipFade: the tooltip's fade-in.
const agentTipFade = 150 * time.Millisecond

// agentTipLayout stacks the tooltip's lines at one fixed size: the size is
// set when the tooltip opens and never follows the text afterwards.
type agentTipLayout struct{ size fyne.Size }

func (l *agentTipLayout) MinSize([]fyne.CanvasObject) fyne.Size { return l.size }
func (l *agentTipLayout) Layout(objs []fyne.CanvasObject, _ fyne.Size) {
	var y float32
	for _, o := range objs {
		h := o.MinSize().Height
		o.Move(fyne.NewPos(0, y))
		o.Resize(fyne.NewSize(l.size.Width, h))
		y += h + theme.Padding()/2
	}
}

func tipLines(s string) []string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = cutTo(l, agentTipMaxChars)
	}
	return lines
}

// agentTipGrace: how long the tooltip stays after the pointer leaves the chip
// or the tooltip, so small movements and the way from chip to tooltip (or to
// the next chip) do not flicker it.
const agentTipGrace = 150 * time.Millisecond

// agentTipHost: the one tooltip of a strip. It is anchored to its owner chip
// (just below it, centred on it), hides after agentTipGrace, stays while the
// pointer is on it, and moves to another chip without a new fade.
//
// It is NOT a widget.PopUp: Fyne dispatches hover events only to the top
// overlay while one is up, so with a popup the chip (and the strip) get no
// hover at all and a full-canvas overlay container takes over; moving the
// pointer along the tooltip or back to the chip then counts as "left
// everything", the tooltip hides after the grace, the chip is hovered again
// and reopens it: an open/close loop. The tooltip is instead a child of
// layer, a plain object layer on top of the window content, where hover
// reaches the chip and the tooltip like any other widgets.
type agentTipHost struct {
	layer *fyne.Container // overlay-free tooltip layer (desk.tipLayer)
	pop   *tipHover
	lines []*canvas.Text // the open tooltip's lines, updated in place
	owner *agentChip
	timer *time.Timer
	grace time.Duration
	do    func(func()) // runs on the UI thread
}

func newAgentTipHost(layer *fyne.Container) *agentTipHost {
	return &agentTipHost{layer: layer, grace: agentTipGrace, do: fyne.Do}
}

// tipHover is the tooltip box. It is hoverable so that being on it keeps it
// open (it is not tappable: clicks fall through to what is below).
type tipHover struct {
	widget.BaseWidget
	box *fyne.Container // the lines, padded; replaced when the owner changes
	h   *agentTipHost
}

func newTipHover(h *agentTipHost, content fyne.CanvasObject) *tipHover {
	t := &tipHover{h: h, box: container.NewPadded(content)}
	t.ExtendBaseWidget(t)
	return t
}

func (t *tipHover) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(theme.Color(theme.ColorNameOverlayBackground))
	bg.StrokeColor = theme.Color(theme.ColorNameInputBorder)
	bg.StrokeWidth = 1
	bg.CornerRadius = theme.InputRadiusSize()
	return widget.NewSimpleRenderer(container.NewStack(bg, t.box))
}
func (t *tipHover) MouseIn(*desktop.MouseEvent)    { t.h.keep() }
func (t *tipHover) MouseMoved(*desktop.MouseEvent) { t.h.keep() }
func (t *tipHover) MouseOut()                      { t.h.leave() }

// keep cancels a pending hide.
func (h *agentTipHost) keep() {
	if h.timer != nil {
		h.timer.Stop()
		h.timer = nil
	}
}

// leave starts the grace delay before the tooltip hides.
func (h *agentTipHost) leave() {
	if h.pop == nil {
		return
	}
	h.keep()
	var t *time.Timer
	t = time.AfterFunc(h.grace, func() {
		h.do(func() {
			if h.timer == t { // not cancelled or replaced meanwhile
				h.hideNow()
			}
		})
	})
	h.timer = t
}

func (h *agentTipHost) hideNow() {
	h.keep()
	if h.pop != nil {
		h.layer.Remove(h.pop)
		h.pop = nil
		h.lines = nil
		h.owner = nil
	}
}

// show opens the tooltip for c, or, when one is open for another chip, moves
// and refills the same tooltip (no new fade).
func (h *agentTipHost) show(c *agentChip) {
	h.keep()
	cv := fyne.CurrentApp().Driver().CanvasForObject(c)
	if c.tip == nil || cv == nil || h.layer == nil || (h.pop != nil && h.owner == c) {
		return
	}
	lines := tipLines(c.tip())
	objs := make([]fyne.CanvasObject, len(lines))
	h.lines = h.lines[:0]
	w := canvas.NewText(agentTipMinText, theme.ForegroundColor()).MinSize().Width
	var ht float32
	swap := h.pop != nil
	fg := theme.ForegroundColor()
	for i, l := range lines {
		t := canvas.NewText(l, color.Transparent)
		if swap {
			t.Color = fg
		}
		t.TextSize = fontMeta
		t.Text = l
		ms := t.MinSize()
		w = max(w, ms.Width)
		ht += ms.Height
		if i > 0 {
			ht += theme.Padding() / 2
		}
		h.lines = append(h.lines, t)
		objs[i] = t
	}
	body := container.New(&agentTipLayout{size: fyne.NewSize(w, ht)}, objs...)
	h.owner = c
	if swap {
		h.pop.box.Objects = []fyne.CanvasObject{body}
		h.pop.box.Refresh()
		h.place(c)
		return
	}
	h.pop = newTipHover(h, body)
	h.place(c)
	h.layer.Add(h.pop)
	lns := h.lines
	fyne.NewAnimation(agentTipFade, func(p float32) {
		for _, t := range lns {
			t.Color = fade(fg, float64(p))
			t.Refresh()
		}
	}).Start()
}

// place sizes the tooltip and puts it below c.
func (h *agentTipHost) place(c *agentChip) {
	h.pop.Resize(h.pop.MinSize())
	h.pop.Move(tipPos(c, h.layer, h.pop.Size()))
}

// tipPos: just below the chip, centred on it, kept inside the layer (which
// covers the window content); the result is relative to the layer.
func tipPos(c *agentChip, layer fyne.CanvasObject, tip fyne.Size) fyne.Position {
	d := fyne.CurrentApp().Driver()
	p := d.AbsolutePositionForObject(c).Subtract(d.AbsolutePositionForObject(layer))
	x := p.X + (c.Size().Width-tip.Width)/2
	x = min(x, layer.Size().Width-tip.Width)
	return fyne.NewPos(max(x, 0), p.Y+c.Size().Height)
}

// update refreshes the open tooltip's text in place for c; the popup is not
// rebuilt or resized (the line count and width were fixed on open).
func (h *agentTipHost) update(c *agentChip) {
	if h.pop == nil || h.owner != c || c.tip == nil {
		return
	}
	lines := tipLines(c.tip())
	for i, t := range h.lines {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		if t.Text != l {
			t.Text = l
			t.Refresh()
		}
	}
}

// MouseIn (anywhere on the chip, padding included) shows the tooltip; the
// size is fixed from the first text (and a floor), later ticks only change
// the text (updateTip).
func (c *agentChip) MouseIn(*desktop.MouseEvent) { c.host.show(c) }

func (c *agentChip) updateTip() { c.host.update(c) }

func (c *agentChip) MouseMoved(*desktop.MouseEvent) { c.host.keep() }

// MouseOut hides the tooltip after the grace delay, unless the pointer
// reaches the tooltip or another chip first.
func (c *agentChip) MouseOut() {
	if c.host.owner == c {
		c.host.leave()
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
