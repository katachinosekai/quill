package main

import (
	"fmt"
	"sort"
	"strings"

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
// same session replaces it; an inactive one can be dismissed.

// planMirror: one mirrored card.
type planMirror struct {
	src       *deskSession
	plan      *chat.Item
	view      *chatView // bound to src; keeps the picks across redraws
	dismissed bool
}

// planMirrors: the strip of mirrored cards in the Quill box.
type planMirrors struct {
	box    *fyne.Container // the cards
	scroll *container.Scroll
	list   []*planMirror
	sig    string
}

// planMirrorMaxH: the strip's height cap; longer plans scroll in it.
const planMirrorMaxH = 360

func newPlanMirrors() *planMirrors {
	m := &planMirrors{box: container.NewVBox()}
	m.scroll = container.NewVScroll(m.box)
	m.scroll.Hide()
	return m
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
	} else {
		m.scroll.SetMinSize(fyne.NewSize(0, min(m.box.MinSize().Height, planMirrorMaxH)))
		m.scroll.Show()
	}
	if p := d.quillChat.root; p != nil {
		p.Refresh()
	}
}

// planMirrorCard: one mirrored card: "Plan card from <name>", the plan as
// the source tab draws it (overlay, or the reply then the card), and the
// actions (open the tab; dismiss once inactive).
func (d *desk) planMirrorCard(x *planMirror) fyne.CanvasObject {
	s := x.src
	if x.view == nil {
		x.view = newChatViewWith(d, func() float32 { return chatMaxWidth }, func() float32 { return 0 })
		x.view.mirrorRedraw = d.drawPlanMirrors // a pick redraws the strip
	}
	v := x.view
	v.s = s
	state := planState(s.conv, x.plan)
	active := x.plan.PermState == chat.PermPending && state == planActive
	head := widget.NewLabelWithStyle("Plan card from "+d.mirrorName(s), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	head.Wrapping = fyne.TextWrapWord
	open := widget.NewButtonWithIcon("Open tab", theme.NavigateNextIcon(), func() { d.open(s.id) })
	open.Importance = widget.LowImportance
	actions := container.NewHBox(open)
	if !active {
		dismiss := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
			x.dismissed = true
			d.quillChat.mirrors.sig = "\x00" // redraw, also when the strip ends up empty
			d.syncPlanMirrors()
		})
		dismiss.Importance = widget.LowImportance
		actions.Add(dismiss)
	}
	parts := []fyne.CanvasObject{container.NewBorder(nil, nil, nil, actions, head)}
	body := v.planMirrorBody(x.plan)
	parts = append(parts, body)
	return bgPad(colCard, 10, 4, 10, container.NewVBox(parts...))
}

// planMirrorBody: the plan as the source tab shows it: the reply with the
// controls in place, or the reply as written followed by the card.
func (v *chatView) planMirrorBody(plan *chat.Item) fyne.CanvasObject {
	reply := planReply(v.s.conv, plan)
	if reply != nil && !v.planPlain[plan] {
		if o := v.renderPlanReply(reply); o != nil {
			return o
		}
	}
	box := container.NewVBox()
	if reply != nil {
		if v.planPlain[plan] && planOverlayQs(v.s, plan, reply) != nil {
			box.Add(v.planToggle(plan, reply))
		}
		box.Add(msgText(v.markdownBlocks(reply.Text)))
	}
	box.Add(v.renderPlan(plan))
	return box
}
