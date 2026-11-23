package main

import (
	"regexp"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// Plan overlay (user 2026-11-22): when a pending plan's reply ends with
// numbered questions, those lines in the reply get their controls right
// under them, and the card's caption and buttons close the reply; there is
// no separate card. A toggle at the reply's top left shows the reply as
// written with today's card instead (to check the parsing); both views share
// one answers map. Anything that doesn't line up falls back to the card.

// planReply: the reply a plan item's card reads, the last non-empty
// assistant reply before it in its turn (nil if none).
func planReply(c *chat.Conversation, plan *chat.Item) *chat.Item {
	for i := len(c.Items) - 1; i >= 0; i-- {
		if c.Items[i] != plan {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			x := c.Items[j]
			if x.Kind == chat.User || x.Kind == chat.Plan {
				return nil
			}
			if x.Kind == chat.Assistant && strings.TrimSpace(x.Text) != "" {
				return x
			}
		}
		return nil
	}
	return nil
}

// replyPlan: the plan item whose reply this is (nil if none).
func replyPlan(c *chat.Conversation, reply *chat.Item) *chat.Item {
	var plan *chat.Item
	for i := len(c.Items) - 1; i >= 0; i-- {
		x := c.Items[i]
		if x == reply {
			if plan != nil && planReply(c, plan) == reply {
				return plan
			}
			return nil
		}
		switch x.Kind {
		case chat.User:
			plan = nil
		case chat.Plan:
			plan = x
		}
	}
	return nil
}

// planOverlayQs: the plan's questions with their lines in reply, when the
// overlay can show them there: the plan is pending, every question is
// numbered, and the reply alone yields the same questions as the card.
func planOverlayQs(s *deskSession, plan, reply *chat.Item) []planQ {
	if s == nil || plan == nil || reply == nil || plan.PermState != chat.PermPending || reply.Streaming {
		return nil
	}
	qs := planItemQuestions(s, plan)
	rq := planQuestionsFor(planQuestion(s.conv, plan), reply.Text)
	if len(qs) == 0 || len(rq) != len(qs) {
		return nil
	}
	for i, q := range qs {
		r := rq[i]
		if q.Num == 0 || r.Num != q.Num || r.Text != q.Text || r.Kind != q.Kind || r.Recommended != q.Recommended || strings.Join(r.Options, "\x00") != strings.Join(q.Options, "\x00") {
			return nil
		}
		qs[i].line, qs[i].end = r.line, r.end
	}
	return qs
}

// planOverlaid: the plan's controls are in its reply (its card draws nothing).
func (v *chatView) planOverlaid(plan *chat.Item) bool {
	if v.s == nil || v.planPlain[plan] {
		return false
	}
	return planOverlayQs(v.s, plan, planReply(v.s.conv, plan)) != nil
}

// planToggle: the reply's top-left switch between the overlay and the plain
// view (the reply as written plus the card).
func (v *chatView) planToggle(plan, reply *chat.Item) fyne.CanvasObject {
	label, icon := "Show as written", theme.DocumentIcon()
	if v.planPlain[plan] {
		label, icon = "Answer in place", theme.ListIcon()
	}
	b := widget.NewButtonWithIcon(label, icon, func() {
		if v.planPlain == nil {
			v.planPlain = map[*chat.Item]bool{}
		}
		v.planPlain[plan] = !v.planPlain[plan]
		v.redrawPlan(plan, reply)
	})
	b.Importance = widget.LowImportance
	return container.NewHBox(b)
}

// redrawPlan redraws a plan item and its reply.
func (v *chatView) redrawPlan(plan, reply *chat.Item) {
	for _, it := range []*chat.Item{reply, plan} {
		if it == nil {
			continue
		}
		if iv, ok := v.views[it.Seq]; ok {
			v.refreshItem(it, iv)
		}
	}
}

var numLeadMD = regexp.MustCompile(`^(\s*)(\d+)([.)])\s+`)

// renderPlanReply draws a reply with a pending plan: the overlay, or the
// plain reply under the toggle; nil when the reply isn't a plan's.
func (v *chatView) renderPlanReply(reply *chat.Item) fyne.CanvasObject {
	if v.s == nil || reply.Streaming {
		return nil
	}
	plan := replyPlan(v.s.conv, reply)
	if plan == nil || plan.PermState != chat.PermPending {
		return nil
	}
	qs := planOverlayQs(v.s, plan, reply)
	if qs == nil {
		return nil
	}
	if v.planPlain[plan] {
		return container.NewVBox(v.planToggle(plan, reply), msgText(v.markdownBlocks(reply.Text)))
	}
	owner := v.s
	ans := v.planAnswers(plan, qs)
	lines := strings.Split(reply.Text, "\n")
	// The approval question is the Approve button.
	skip := map[int]bool{}
	if title := normQ(planQuestion(owner.conv, plan)); title != "" {
		for i := len(lines) - 1; i >= 0; i-- {
			if n := normQ(lines[i]); n != "" && (n == title || strings.Contains(n, title)) {
				skip[i] = true
				break
			}
		}
	}
	box := container.NewVBox(v.planToggle(plan, reply))
	prose := func(from, to int) {
		var keep []string
		for i := from; i < to && i < len(lines); i++ {
			if !skip[i] {
				keep = append(keep, lines[i])
			}
		}
		if t := strings.Join(keep, "\n"); strings.TrimSpace(t) != "" {
			box.Add(msgText(v.markdownBlocks(t)))
		}
	}
	at := 0
	for i := 0; i < len(qs); {
		q := qs[i]
		if q.line < at || q.end > len(lines) {
			return nil
		}
		prose(at, q.line)
		// The question as written; "2." stays 2, not a list restarting at 1
		// (a no-break space after the number is no list marker).
		text := strings.Join(lines[q.line:q.end], "\n")
		text = numLeadMD.ReplaceAllString(text, "$1$2$3\u00a0")
		box.Add(msgText(v.markdown(strings.TrimRight(text, "\n "))))
		// Every question on this line gets its control under it.
		for ; i < len(qs) && qs[i].line == q.line; i++ {
			ctl := planControl(qs[i], recommendedLabels(qs[i]), ans)
			if _, entry := ctl.(*widget.Entry); !entry {
				ctl = v.centered(ctl)
			}
			box.Add(ctl)
		}
		at = q.end
	}
	prose(at, len(lines))
	msg := widget.NewLabel("Approving runs this plan: every command and change, including its background jobs and subagents, is approved until the plan is done. Unanswered questions are sent as \"no answer\".")
	msg.Wrapping = fyne.TextWrapWord
	msg.Alignment = v.align()
	msg.SizeName = theme.SizeNameCaptionText
	run := glowButton("Approve plan & send answers", theme.ConfirmIcon(), colIdle, v.d.canAnswer(owner), func() {
		v.d.answerPlanAnswers(owner, plan, qs, ans)
	})
	no := glowButton("Don't run", theme.CancelIcon(), colError, true, func() { v.d.answerPlan(owner, plan, false) })
	box.Add(msg)
	box.Add(v.buttonRow(run, no))
	return box
}
