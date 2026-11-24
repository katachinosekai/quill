package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// Plan card mirror (user 2026-11-24, guard audit friction 4): a plan card
// another session (a worker or peer tab) waits on is mirrored into the Quill
// operator's box, labelled with that session's name. The mirror is drawn by a
// chat view bound to the SOURCE session, so its buttons run the very code the
// source tab's card runs (answerPlan, answerPlanAnswers, answerPlanApproval
// with the source session as owner): a click there is the user's own click on
// the source card and approves that session, never the operator. Text the
// operator or any peer writes still never approves (planGate, notFromUser).
// The mirror goes inactive (greyed, with the card's status) once the source
// card is answered there or here, replaced or moved on; a newer card of the
// same session replaces it; an inactive one can be dismissed, and hides by
// itself planMirrorHide later (user 2026-11-24).
//
// The strip (user 2026-11-24): only the card (its questions, controls,
// approval and status; never the source's reply prose), centred in the chat
// column, at most planMirrorMaxH or planMirrorMaxFrac of the box tall and
// scrolling inside; with several cards each is a one-line row (name and
// state) that opens on a click. The chat below keeps its layout; with no
// card the box is laid out as without the strip (c.top hidden).
//
// The Quill button's badge counts the mirrors that wait for the user
// (waitingMirrors), added to the operator's own count; while the Quill box
// isn't showing they turn the button amber as the operator's approvals do.

// planMirror: one mirrored card.
type planMirror struct {
	src       *deskSession
	plan      *chat.Item
	view      *chatView // bound to src; keeps the picks across redraws
	dismissed bool
	expanded  bool // with several cards: this row is open
	hideArmed bool // inactive: the auto-hide timer runs
	hideGen   int  // the timer's generation: a stale timer does nothing
}

// waiting: the mirrored card waits for the user (live in its tab).
func (x *planMirror) waiting() bool {
	return !x.dismissed && x.plan.PermState == chat.PermPending && planState(x.src.conv, x.plan) == planActive
}

// planMirrors: the strip of mirrored cards in the Quill box.
type planMirrors struct {
	box     *fyne.Container // the cards
	scroll  *container.Scroll
	wrap    *fyne.Container // the scroll at its capped height (what c.top holds)
	list    []*planMirror
	sig     string
	waiting int            // mirrors waiting for the user, as last badged
	boxH    func() float32 // the box content's height (the cap's fraction)
}

const (
	planMirrorMaxH    = 220             // the strip's height cap; longer cards scroll in it
	planMirrorMaxFrac = 0.3             // ... and at most this part of the box
	planMirrorHide    = 5 * time.Second // an inactive mirror hides by itself after this
)

func newPlanMirrors(boxH func() float32) *planMirrors {
	m := &planMirrors{box: container.NewVBox(), boxH: boxH}
	m.scroll = container.NewVScroll(m.box)
	m.wrap = container.New(mirrorCapLayout{m}, m.scroll)
	m.scroll.Hide()
	m.wrap.Hide()
	return m
}

// capH: the strip's height for content this tall.
func (m *planMirrors) capH(content float32) float32 {
	c := float32(planMirrorMaxH)
	if m.boxH != nil {
		if h := m.boxH(); h > 0 {
			c = min(c, h*planMirrorMaxFrac)
		}
	}
	return min(content, c)
}

// mirrorCapLayout: the strip as tall as its cards up to the cap (read at
// layout time, so it follows the box's height), the scroll filling it.
type mirrorCapLayout struct{ m *planMirrors }

func (l mirrorCapLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, l.m.capH(l.m.box.MinSize().Height))
}

func (l mirrorCapLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

// waitingMirrors: the mirrored cards waiting for the user (the Quill
// button's badge adds them).
func (d *desk) waitingMirrors() int {
	if d.quillChat == nil || d.quillChat.mirrors == nil {
		return 0
	}
	n := 0
	for _, x := range d.quillChat.mirrors.list {
		if x.waiting() {
			n++
		}
	}
	return n
}

// mirrorAlerting: role is the Quill button and a mirrored card waits while
// the Quill box isn't showing (amber, as the operator's own approvals).
func (d *desk) mirrorAlerting(role string) bool {
	return role == roleQuill && !d.assistBoxShowing(roleQuill) && d.waitingMirrors() > 0
}

// armMirrorHide starts x's auto-hide once it is inactive (once per
// inactive spell); waiting again disarms it.
func (d *desk) armMirrorHide(x *planMirror) {
	if x.waiting() {
		x.hideArmed = false
		return
	}
	if x.hideArmed || x.dismissed {
		return
	}
	x.hideArmed = true
	x.hideGen++
	gen := x.hideGen
	after := d.after
	if after == nil {
		after = func(t time.Duration, f func()) { time.AfterFunc(t, f) }
	}
	after(planMirrorHide, func() { d.do(func() { d.autoHideMirror(x, gen) }) })
}

// autoHideMirror: x's timer fired: hide it if it is still the same inactive
// mirror in the strip (not dismissed, replaced or live again since).
func (d *desk) autoHideMirror(x *planMirror, gen int) {
	c := d.quillChat
	if c == nil || c.mirrors == nil || x.dismissed || !x.hideArmed || x.hideGen != gen || x.waiting() {
		return
	}
	for _, y := range c.mirrors.list {
		if y == x {
			x.dismissed = true
			c.mirrors.sig = "\x00" // redraw, also when the strip ends up empty
			d.syncPlanMirrors()
			return
		}
	}
}

// mirrorSources: the sessions whose cards are mirrored, in tab order: every
// open session but the hidden roles (the operator itself, the kmem
// assistant).
func (d *desk) mirrorSources() []*deskSession {
	var out []*deskSession
	seen := map[*deskSession]bool{}
	add := func(s *deskSession) {
		if s == nil || seen[s] || hiddenRole(s) != "" || s.conv == nil {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, id := range d.tabs {
		add(d.sessions[id])
	}
	var rest []string
	for id := range d.sessions {
		rest = append(rest, id)
	}
	sort.Strings(rest)
	for _, id := range rest {
		add(d.sessions[id])
	}
	return out
}

// mirrorName: the source session's name as its tab shows it.
func (d *desk) mirrorName(s *deskSession) string {
	title, _, _ := d.rowText(s.id)
	if strings.TrimSpace(title) == "" {
		title = s.id
	}
	return title
}

// syncPlanMirrors brings the Quill box's mirrored cards up to date: a card
// for each session's live plan, the last one kept (inactive) once it is no
// longer live. Rebuilt only when something it shows changed.
func (d *desk) syncPlanMirrors() {
	c := d.quillChat
	if c == nil || c.mirrors == nil {
		return
	}
	m := c.mirrors
	op := d.operatorSession()
	bySrc := map[*deskSession]*planMirror{}
	for _, x := range m.list {
		bySrc[x.src] = x
	}
	var list []*planMirror
	for _, s := range d.mirrorSources() {
		if s == op {
			continue
		}
		x := bySrc[s]
		if lp := livePlan(s.conv); lp != nil && (x == nil || x.plan != lp) {
			// A new card: the older mirror (if any) is gone with it.
			x = &planMirror{src: s, plan: lp}
		}
		if x == nil || x.dismissed || !convHas(s.conv, x.plan) {
			continue
		}
		list = append(list, x)
	}
	m.list = list
	for _, x := range list {
		d.armMirrorHide(x)
	}
	if w := d.waitingMirrors(); w != m.waiting {
		// The Quill button's badge and amber follow, also with the box closed.
		m.waiting = w
		if a := d.quillAssist(); a != nil {
			d.updateAssistBadge(a)
		}
		d.syncAssistButtons()
	}
	var sig []string
	for _, x := range list {
		s := x.src
		sig = append(sig, fmt.Sprint(s.id, x.plan.Seq, planState(s.conv, x.plan), x.plan.PermState, d.canAnswer(s), s.conv.Busy, s.inTurn, replied(s, x.plan), d.mirrorName(s)))
	}
	j := strings.Join(sig, "\n")
	if j == m.sig {
		return
	}
	m.sig = j
	d.drawPlanMirrors()
}

// drawPlanMirrors rebuilds the strip from m.list.
func (d *desk) drawPlanMirrors() {
	m := d.quillChat.mirrors
	var objs []fyne.CanvasObject
	for _, x := range m.list {
		objs = append(objs, d.planMirrorCard(x))
	}
	m.box.Objects = objs
	m.box.Refresh()
	if len(objs) == 0 {
		m.scroll.Hide()
		m.wrap.Hide()
	} else {
		m.scroll.Show()
		m.wrap.Show()
		m.wrap.Refresh()
	}
	d.quillChat.syncTop()
	if p := d.quillChat.root; p != nil {
		p.Refresh()
	}
}

// planMirrorCard: one mirrored card: "Plan card from <name>", the card as
// the source tab draws it (no reply prose), and the actions (open the tab;
// dismiss once inactive). With several cards it is a one-line row (name and
// state) that opens on a click. Centred in the chat column.
func (d *desk) planMirrorCard(x *planMirror) fyne.CanvasObject {
	s := x.src
	if x.view == nil {
		x.view = newChatViewWith(d, func() float32 { return chatMaxWidth }, func() float32 { return 0 })
		x.view.mirrorRedraw = d.drawPlanMirrors // a pick redraws the strip
		x.view.cardOnly = true
	}
	v := x.view
	v.s = s
	state := planState(s.conv, x.plan)
	active := x.plan.PermState == chat.PermPending && state == planActive
	title := "Plan card from " + d.mirrorName(s)
	open := widget.NewButtonWithIcon("Open tab", theme.NavigateNextIcon(), func() { d.open(s.id) })
	open.Importance = widget.LowImportance
	actions := container.NewHBox(open)
	if !active {
		dismiss := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
			x.dismissed = true
			x.hideArmed = false
			d.quillChat.mirrors.sig = "\x00" // redraw, also when the strip ends up empty
			d.syncPlanMirrors()
		})
		dismiss.Importance = widget.LowImportance
		actions.Add(dismiss)
	}
	compact := d.quillChat != nil && d.quillChat.mirrors != nil && len(d.quillChat.mirrors.list) > 1
	var head fyne.CanvasObject
	if compact {
		stateText := "waiting for you"
		if !active {
			stateText = planStateLabel(state)
		}
		icon := theme.MenuExpandIcon()
		if x.expanded {
			icon = theme.MenuDropDownIcon()
		}
		row := widget.NewButtonWithIcon(title+" · "+stateText, icon, func() {
			x.expanded = !x.expanded
			d.drawPlanMirrors()
		})
		row.Alignment = widget.ButtonAlignLeading
		row.Importance = widget.LowImportance
		head = row
	} else {
		l := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		l.Truncation = fyne.TextTruncateEllipsis
		head = l
	}
	parts := []fyne.CanvasObject{container.NewBorder(nil, nil, nil, actions, head)}
	if !compact || x.expanded {
		parts = append(parts, v.planMirrorBody(x.plan))
	}
	card := bgPad(colCard, 10, 4, 10, container.NewVBox(parts...))
	return windowCentered(func() float32 { return chatMaxWidth }, func() float32 { return 0 }, card)
}

// planMirrorBody: the card alone, as the source tab shows it: the
// questions and controls of the reply (cardOnly: no prose), or the card.
func (v *chatView) planMirrorBody(plan *chat.Item) fyne.CanvasObject {
	if reply := planReply(v.s.conv, plan); reply != nil {
		if o := v.renderPlanReply(reply); o != nil {
			return o
		}
	}
	return v.renderPlan(plan)
}
