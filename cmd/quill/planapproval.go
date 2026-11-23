package main

import (
	"regexp"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// Approval questions with their own options (user 2026-11-22, tfd6ed1): a
// plan that ends "Can I run steps 1–3? Yes (recommended) or no?" shows the
// question's own buttons, not "Approve plan & run all" / "Don't run". A
// go-ahead option (Yes, or any option that isn't a way to stop) approves
// the plan exactly as the Approve button does (answerPlanMsg: startPlan, the
// plan's background work and subagents covered, the approval message); a
// stop option (No, Stop, Don't …) is "Don't run". With other questions on
// the card, the approval pick waits for "Send answers" with the rest.

// stopOption: an option that turns the plan down.
var stopOption = regexp.MustCompile(`(?i)^\W*(?:no|nope|not (?:now|yet)|stop|don'?t|do not|cancel|abort|hold off|leave it|skip (?:it|this|them|the plan))\b`)

// approvalOpts: the approval question's own options.
type approvalOpts struct {
	Options     []string `json:"options"`
	Stop        []bool   `json:"stop"`        // the option turns the plan down
	Recommended int      `json:"recommended"` // -1 = none
	// DontRun: no option stops, so the card adds its own "Don't run" (the
	// options are planChoices', in its order: request.choices).
	DontRun bool `json:"dontRun,omitempty"`
}

// dontRunPick: answerPlanApproval's index for the card's own "Don't run".
const dontRunPick = -2

// planApprovalOptions: the options the approval question (the plan card's
// title) offers: a go-ahead and a way to stop (the last options sentence of
// the title), or else the picks between things to approve that
// planChoices reads ("Ship as v0.1.18, or v0.2.0?"; user 2026-11-22,
// f4684ce), which get a "Don't run" of their own. nil: the card keeps its
// Approve button.
func planApprovalOptions(title string) *approvalOpts {
	sents := questionSentences(strings.TrimSpace(strings.Trim(mdNoise.Replace(title), "*_ ")))
	for i := len(sents) - 1; i >= 0; i-- {
		_, opts, ok := parseChoices(sents[i])
		if !ok {
			continue
		}
		for j, o := range opts {
			opts[j] = dropQuestionWord(o)
		}
		if isYesNoPair(opts) {
			opts = []string{"Yes", "No"}
		}
		a := &approvalOpts{Options: opts, Stop: make([]bool, len(opts)), Recommended: recommendedOption(sents[i], len(opts))}
		stops := 0
		for j, o := range opts {
			if stopOption.MatchString(o) {
				a.Stop[j] = true
				stops++
			}
		}
		if stops == len(opts) {
			return nil
		}
		if stops > 0 {
			return a
		}
		break
	}
	// No way to stop: planChoices' options (it leaves out "Do you approve
	// …, or should I change something first?", which is no pick).
	opts, rec := planChoices(title)
	if len(opts) == 0 {
		return nil
	}
	return &approvalOpts{Options: opts, Stop: make([]bool, len(opts)), Recommended: rec, DontRun: true}
}

// planItemApproval: planApprovalOptions for a plan item's card.
func planItemApproval(s *deskSession, it *chat.Item) *approvalOpts {
	if s == nil || it == nil {
		return nil
	}
	return planApprovalOptions(planQuestion(s.conv, it))
}

// approvalChoice: the choice text an approval option adds to the approval
// message ("" for a plain Yes).
func approvalChoice(opt string) string {
	if strings.EqualFold(strings.TrimSpace(opt), "yes") {
		return ""
	}
	return opt
}

// planGoFull: the approval with the approval question's choice and the other
// questions' answers (planGoAnswers' numbering).
func planGoFull(choice string, qs []planQ, ans map[int]planAnswer) string {
	msg := planGoWith(choice)
	if len(qs) > 0 {
		msg += strings.TrimPrefix(planGoAnswers(qs, ans), planGoMessage+planGoScope)
	}
	return msg
}

// planStopMessage: what a stop on a card with other questions sends: the
// plan isn't run, and the answers go along (planGoAnswers' numbering).
func planStopMessage(choice string, qs []planQ, ans map[int]planAnswer) string {
	msg := planStopText
	if choice != "" {
		msg += " Answer to your question: " + choice + "."
	}
	return msg + strings.TrimPrefix(planGoAnswers(qs, ans), planGoMessage+planGoScope)
}

const planStopText = "Not running the plan: the user turned it down."

// answerPlanApproval answers the plan with option i of the approval question
// (dontRunPick: the card's own "Don't run"), and with qs the other
// questions' answers: a go-ahead approves as "Approve plan & run all" does;
// a stop is "Don't run", and with other questions their answers are sent
// after it (user 2026-11-22, f4684ce).
func (d *desk) answerPlanApproval(s *deskSession, it *chat.Item, a *approvalOpts, i int, qs []planQ, ans map[int]planAnswer) {
	if a == nil || (i < 0 || i >= len(a.Options)) && !(i == dontRunPick && a.DontRun) {
		return
	}
	stop, choice := true, ""
	if i >= 0 {
		stop, choice = a.Stop[i], approvalChoice(a.Options[i])
	}
	if stop && strings.EqualFold(choice, "no") {
		choice = ""
	}
	if s != nil && s.remote != nil {
		if s = d.cardOwner(s, it); s == nil {
			return
		}
		if stop && len(qs) > 0 {
			// rejectPlan's text goes to Claude after the refusal.
			d.remoteWrite(s, "answer", map[string]any{"requestId": it.RequestID, "action": "rejectPlan", "text": planStopMessage(choice, qs, ans)})
			return
		}
		var answers map[string]any
		if !stop && len(qs) > 0 {
			answers = planAnswersJSON(qs, ans)
		}
		d.remotePlan(s, it, !stop, choice, answers)
		return
	}
	if stop {
		pending := it.PermState == chat.PermPending
		d.answerPlanMsg(s, it, false, "")
		if pending && it.PermState == chat.PermDenied && len(qs) > 0 {
			d.sendTo(s, planStopMessage(choice, qs, ans))
		}
		return
	}
	d.answerPlanMsg(s, it, true, planGoFull(choice, qs, ans))
}

// approvalPick: the card's pick on the approval question (-1 = none yet).
func (v *chatView) approvalPick(plan *chat.Item) int {
	if p, ok := v.planApprove[plan]; ok {
		return p
	}
	return -1
}

// approvalButtons: the approval question's options as buttons. Without other
// questions a click answers the plan; with them it marks the pick (it can be
// changed) and "Send answers" sends everything.
func (v *chatView) approvalButtons(owner *deskSession, plan *chat.Item, a *approvalOpts, qs []planQ, ans map[int]planAnswer) fyne.CanvasObject {
	pick := v.approvalPick(plan)
	var btns []fyne.CanvasObject
	for i, o := range a.Options {
		i := i
		label := shortLabel(o, 60)
		if i == a.Recommended {
			label += " (recommended)"
		}
		icon, tint := fyne.Resource(nil), colIdle
		if a.Stop[i] {
			tint = colError
		}
		if len(qs) == 0 {
			icon = theme.ConfirmIcon()
			if a.Stop[i] {
				icon = theme.CancelIcon()
			}
		} else if pick == i {
			icon, tint = theme.ConfirmIcon(), colAccent
		}
		enabled := a.Stop[i] || v.d.canAnswer(owner)
		btns = append(btns, glowButton(label, icon, tint, enabled, v.guarded(func() {
			if len(qs) == 0 {
				v.d.answerPlanApproval(owner, plan, a, i, nil, nil)
				return
			}
			if v.planApprove == nil {
				v.planApprove = map[*chat.Item]int{}
			}
			v.planApprove[plan] = i
			v.redrawPlan(plan, planReply(owner.conv, plan))
		})))
	}
	if a.DontRun {
		// No option stops: the card's own way to turn the plan down, at
		// once (with the other questions' answers, if any).
		btns = append(btns, glowButton("Don't run", theme.CancelIcon(), colError, true, v.guarded(func() {
			v.d.answerPlanApproval(owner, plan, a, dontRunPick, qs, ans)
		})))
	}
	return v.buttonRow(btns...)
}

// approvalSend: "Send answers" for a card whose approval question has its own
// options, with a note while that question has no pick.
func (v *chatView) approvalSend(owner *deskSession, plan *chat.Item, a *approvalOpts, qs []planQ, ans map[int]planAnswer) []fyne.CanvasObject {
	pick := v.approvalPick(plan)
	enabled := pick >= 0 && (a.Stop[pick] || v.d.canAnswer(owner))
	send := glowButton("Send answers", theme.MailSendIcon(), colIdle, enabled, v.guarded(func() {
		v.d.answerPlanApproval(owner, plan, a, v.approvalPick(plan), qs, ans)
	}))
	out := []fyne.CanvasObject{v.buttonRow(send)}
	if pick < 0 {
		note := widget.NewLabel("Answer the approval question, then send.")
		note.SizeName = theme.SizeNameCaptionText
		note.Alignment = v.align()
		out = append(out, note)
	}
	return out
}

// approvalKind: the click guard's kind for a card with approval options.
func approvalKind(a *approvalOpts, qs []planQ) string {
	k := "approval:" + strings.Join(a.Options, "/")
	if a.DontRun {
		k += "|dontrun"
	}
	if len(qs) > 0 {
		k += "|send"
	}
	return k
}

// approvalCaption: the card's note on what the go-ahead does.
func approvalCaption(a *approvalOpts) string {
	goOpt := "Yes"
	for i, o := range a.Options {
		if !a.Stop[i] {
			goOpt = o
			break
		}
	}
	return "Answering \"" + shortLabel(goOpt, 40) + "\" runs this plan: every command and change, including its background jobs and subagents, is approved until the plan is done."
}

// isApprovalStop: opt is one of a's stop options.
func isApprovalStop(a *approvalOpts, opt string) bool {
	if a == nil {
		return false
	}
	for i, o := range a.Options {
		if a.Stop[i] && o == opt {
			return true
		}
	}
	return false
}
