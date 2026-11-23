package main

import (
	"regexp"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
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
// overlay can show them there: the reply alone yields the same questions as
// the card. An answered, rejected, replaced or moved-on plan keeps them there
// too, greyed with the pick marked (user 2026-11-23). A plan without questions (just the approval
// buttons) gives an empty, non-nil list when the reply holds its approval
// question.
func planOverlayQs(s *deskSession, plan, reply *chat.Item) []planQ {
	if s == nil || plan == nil || reply == nil || reply.Streaming {
		return nil
	}
	qs := planItemQuestions(s, plan)
	rq := planQuestionsFor(planQuestion(s.conv, plan), reply.Text)
	if len(rq) != len(qs) {
		return nil
	}
	if len(qs) == 0 {
		if start, _ := approvalSpan(reply.Text, planQuestion(s.conv, plan)); start < 0 && strings.TrimSpace(s.conv.PlanText(plan)) != strings.TrimSpace(reply.Text) {
			return nil // the plan's text is elsewhere
		}
		return []planQ{} // the approval question, or the buttons closing the reply
	}
	n, at, prev := len(strings.Split(reply.Text, "\n")), 0, -1
	for i, q := range qs {
		r := rq[i]
		if r.Num != q.Num || r.Text != q.Text || r.Kind != q.Kind || r.Recommended != q.Recommended || strings.Join(r.Options, "\x00") != strings.Join(q.Options, "\x00") {
			return nil
		}
		// Two questions on one numbered line share its lines.
		if r.line == prev && (r.end != at || r.Num == 0) || r.line != prev && r.line < at || r.end > n || r.line >= r.end {
			return nil
		}
		prev = r.line
		at = r.end
		qs[i].line, qs[i].end = r.line, r.end
	}
	return qs
}

// approvalSpan: the original lines of text (first, last) that hold the
// approval question title (its options may be on the next line;
// chat.JoinOptionLineSpans); -1 if none does.
func approvalSpan(text, title string) (int, int) {
	lines := strings.Split(text, "\n")
	if title = normQ(title); title == "" {
		return -1, -1
	}
	joined, first := chat.JoinOptionLineSpans(text)
	for i := len(joined) - 1; i >= 0; i-- {
		if n := normQ(joined[i]); n != "" && (n == title || strings.Contains(n, title)) {
			end := len(lines) - 1
			if i+1 < len(first) {
				end = first[i+1] - 1
			}
			for end > first[i] && strings.TrimSpace(lines[end]) == "" {
				end--
			}
			return first[i], end
		}
	}
	return -1, -1
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

// keepNumbers: lines as one markdown block. A numbered list that doesn't
// start at 1 (a step after a question: "3. Then run x.") would restart at 1
// as a block of its own, so its numbers get a no-break space (no list
// marker) and a blank line between them.
func keepNumbers(lines []string) string {
	first := -1
	for i, l := range lines {
		if m := numLeadMD.FindStringSubmatch(l); m != nil {
			first = i
			if m[2] == "1" {
				first = -1
			}
			break
		}
	}
	if first < 0 {
		return strings.Join(lines, "\n")
	}
	var out []string
	for i, l := range lines {
		if numLeadMD.MatchString(l) {
			l = numLeadMD.ReplaceAllString(l, "$1$2$3\u00a0")
			if i > 0 && strings.TrimSpace(lines[i-1]) != "" {
				out = append(out, "")
			}
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

var numLeadMD = regexp.MustCompile(`^(\s*)(\d+)([.)])\s+`)

// planStatus: the small caption an inactive plan shows under its controls.
func planStatus(plan *chat.Item, state string) string {
	text := "Not run"
	switch plan.PermState {
	case chat.PermAllowed:
		text = "Approved: running the whole plan"
	case chat.PermCancelled, chat.PermPending:
		text = "No longer needed"
	}
	return text + " (" + planStateLabel(state) + ")"
}

// greyed disables a control (the radio group, check group or text field of
// an answered plan); the pick stays marked.
func greyed(o fyne.CanvasObject) fyne.CanvasObject {
	if d, ok := o.(fyne.Disableable); ok {
		d.Disable()
	}
	return o
}

// renderPlanReply draws a reply with a plan: the overlay, or the plain reply
// under the toggle; nil when the reply isn't a plan's. A plan that is
// answered, rejected, replaced or moved on keeps its controls here, greyed
// with the pick marked, and has no card below (user 2026-11-23).
func (v *chatView) renderPlanReply(reply *chat.Item) fyne.CanvasObject {
	if v.s == nil || reply.Streaming {
		return nil
	}
	plan := replyPlan(v.s.conv, reply)
	if plan == nil {
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
	state := planState(owner.conv, plan)
	active := plan.PermState == chat.PermPending && state == planActive
	ans := v.planAnswers(plan, qs)
	lines := strings.Split(reply.Text, "\n")
	// The approval question is shown once, as a radio group (planapproval.go).
	appr := planItemApproval(owner, plan)
	skip := map[int]bool{}
	// apprStart..apprLine: the original lines of the approval question.
	apprStart, apprLine := approvalSpan(reply.Text, planQuestion(owner.conv, plan))
	if appr == nil && len(qs) > 0 { // without questions the approval question stays as the card's title was
		for i := apprStart; i >= 0 && i <= apprLine; i++ {
			skip[i] = true
		}
	}
	box := container.New(settledVBox{}, v.planToggle(plan, reply))
	caption := func(text string) fyne.CanvasObject {
		msg := widget.NewLabel(text)
		msg.Wrapping = fyne.TextWrapWord
		msg.Alignment = v.align()
		msg.SizeName = theme.SizeNameCaptionText
		return msg
	}
	// The approval block is one box (accent border while the card waits):
	// the warning, the approval question as written, the radio group.
	apprControl := func(question string) {
		var parts []fyne.CanvasObject
		if active {
			c := approvalCaption(appr)
			if len(qs) > 0 {
				c += " Unanswered questions are sent as \"no answer\"."
			}
			parts = append(parts, caption(c))
		}
		if strings.TrimSpace(question) != "" {
			parts = append(parts, msgText(v.markdownBlocks(keepNumbers(strings.Split(strings.TrimRight(question, "\n "), "\n")))))
		}
		parts = append(parts, v.fadeIn(plan.Seq, v.centered(v.approvalRadio(owner, plan, appr, active))))
		box.Add(questionCard(parts, active))
	}
	prose := func(from, to int) {
		var keep []string
		flush := func() {
			if t := keepNumbers(keep); strings.TrimSpace(t) != "" {
				box.Add(msgText(v.markdownBlocks(t)))
			}
			keep = nil
		}
		for i := from; i < to && i < len(lines); i++ {
			inAppr := appr != nil && apprLine >= from && apprLine < to && i >= apprStart && i <= apprLine
			if !skip[i] && !inAppr {
				// Numbers stay as written: keepNumbers keeps a list from 1 a
				// real list and puts a later piece ("3.") on lines of its own.
				keep = append(keep, lines[i])
			}
			if appr != nil && i == apprLine {
				flush()
				apprControl(strings.Join(lines[max(apprStart, 0):apprLine+1], "\n"))
			}
		}
		flush()
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
		text = numLeadMD.ReplaceAllString(text, "$1$2$3 ")
		box.Add(msgText(v.markdown(strings.TrimRight(text, "\n "))))
		// Every question on this line gets its control under it.
		for ; i < len(qs) && qs[i].line == q.line; i++ {
			ctl := planControl(qs[i], recommendedLabels(qs[i]), ans)
			if !active {
				greyed(ctl)
			}
			if _, entry := ctl.(*widget.Entry); !entry {
				ctl = v.centered(ctl)
			}
			box.Add(v.fadeIn(plan.Seq, ctl))
		}
		if appr != nil && apprLine >= q.line && apprLine < q.end {
			apprControl("") // the approval shares this question's lines (shown above)
		}
		at = q.end
	}
	prose(at, len(lines))
	status := func() { box.Add(v.muted(planStatus(plan, state), fontBody)) }
	resend := func() {
		if plan.PermState == chat.PermAllowed && !owner.conv.Busy && !owner.inTurn && !replied(owner, plan) {
			box.Add(v.fadeIn(plan.Seq, v.buttonRow(glowButton("Resend approval", theme.MailSendIcon(), colIdle, true, v.guarded(func() { v.d.answerPlan(owner, plan, true) })))))
			v.noteButtons(plan.Seq, "resend")
		}
	}
	if appr != nil {
		if apprLine < 0 {
			apprControl("") // the question wasn't found in the reply: its box closes it
		}
		for _, o := range v.approvalSend(owner, plan, appr, qs, ans, active) {
			box.Add(v.fadeIn(plan.Seq, o))
		}
		if active {
			v.noteButtons(plan.Seq, approvalKind(appr, qs))
		} else {
			status()
			if plan.PermState == chat.PermAllowed && !owner.conv.Busy && !owner.inTurn && !replied(owner, plan) {
				resend()
			} else {
				v.noteButtons(plan.Seq, "none")
			}
		}
		return box
	}
	label := "Approve plan & send answers"
	text := "Approving runs this plan: every command and change, including its background jobs and subagents, is approved until the plan is done. Unanswered questions are sent as \"no answer\"."
	if len(qs) == 0 {
		label = "Approve plan & run all"
		text = "Approving runs this plan: every command and change, including its background jobs and subagents, is approved until the plan is done."
	}
	run := glowButton(label, theme.ConfirmIcon(), colIdle, active && v.d.canAnswer(owner), v.guarded(func() {
		if len(qs) == 0 {
			v.d.answerPlan(owner, plan, true)
			return
		}
		v.d.answerPlanAnswers(owner, plan, qs, ans)
	}))
	no := glowButton("Don't run", theme.CancelIcon(), colError, active, v.guarded(func() { v.d.answerPlan(owner, plan, false) }))
	if active {
		box.Add(questionCard([]fyne.CanvasObject{caption(text), v.fadeIn(plan.Seq, v.buttonRow(run, no))}, true))
	} else {
		box.Add(questionCard([]fyne.CanvasObject{v.fadeIn(plan.Seq, v.buttonRow(run, no))}, false))
	}
	if active {
		v.noteButtons(plan.Seq, "pending")
		return box
	}
	status()
	if plan.PermState == chat.PermAllowed && !owner.conv.Busy && !owner.inTurn && !replied(owner, plan) {
		resend()
	} else {
		v.noteButtons(plan.Seq, "none")
	}
	return box
}

// settledVBox is a VBox that lays out again when a block's height changed
// while it was being laid out. A wrapped RichText (a list, a paragraph in a
// column) only reports its real MinSize after it has been resized to its
// column, so a plain VBox placed the next block by the height from before the
// wrap and the approval box drew over the last wrapped list item.
type settledVBox struct{}

func (settledVBox) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return layout.NewVBoxLayout().MinSize(objs)
}

func (settledVBox) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	vb := layout.NewVBoxLayout()
	for pass := 0; pass < 4; pass++ {
		before := vb.MinSize(objs)
		vb.Layout(objs, size)
		if vb.MinSize(objs) == before {
			return
		}
	}
}
