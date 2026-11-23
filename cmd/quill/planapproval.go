package main

import (
	"regexp"
	"strings"
	"unicode"

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
		if a.Recommended < 0 && i > 0 {
			// "Count it … (recommended), or keep it? Only while waiting, or
			// keep?": the question before the options sentence marks it.
			a.Recommended = recommendedOption(sents[i-1], len(opts))
		}
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
	if len(opts) == 0 || !asksToRun(title, opts) {
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
	return planGoWith(choice) + planAnswerList(qs, ans)
}

// planGoChanges: the approval with the user's "Changes (optional)" text
// appended ("Approved. Also: <text>"); the approval counts as before.
func planGoChanges(msg, changes string) string {
	if changes = strings.TrimSpace(changes); changes == "" {
		return msg
	}
	return msg + " Also: " + changes
}

// notCountedNotice: shown when a typed message looks like an approval the
// gate doesn't count (nearApproval).
const notCountedNotice = "Not counted as approval: press Approve on the plan card or type yes."

// nearApproval: a typed message that looks like an attempt to approve
// ("Yes appeoved"): it starts with an approval word or is within two edits of
// "approve(d)". Only a notice follows; it never counts as approval.
func nearApproval(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	words := strings.FieldsFunc(t, func(r rune) bool { return !unicode.IsLetter(r) && r != '\'' })
	if len(words) == 0 {
		return false
	}
	switch w := words[0]; w {
	case "yes", "yeah", "yep", "yup", "ok", "okay", "approve", "approved", "proceed", "lgtm":
		return true
	case "go":
		return len(words) > 1 && (words[1] == "ahead" || words[1] == "for")
	case "do":
		return len(words) > 1 && words[1] == "it"
	default:
		return len(w) >= 5 && w[0] == 'a' && (levenshtein(w, "approve") <= 2 || levenshtein(w, "approved") <= 2)
	}
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// planStopMessage: what a stop on a card with other questions sends: the
// plan isn't run, and the answers go along (planGoAnswers' numbering).
func planStopMessage(choice string, qs []planQ, ans map[int]planAnswer) string {
	msg := planStopText
	if choice != "" {
		msg += " Choice: " + choice + "."
	}
	return msg + planAnswerList(qs, ans)
}

const planStopText = "Not approved."

// answerPlanApproval answers the plan with option i of the approval question
// (dontRunPick: the card's own "Don't run"), and with qs the other
// questions' answers: a go-ahead approves as "Approve plan & run all" does;
// a stop is "Don't run", and with other questions their answers are sent
// after it (user 2026-11-22, f4684ce).
func (d *desk) answerPlanApproval(s *deskSession, it *chat.Item, a *approvalOpts, i int, qs []planQ, ans map[int]planAnswer, changes string) {
	if a == nil || (i < 0 || i >= len(a.Options)) && !(i == dontRunPick && a.DontRun) {
		return
	}
	if planSuperseded(d.ownerOf(s, it), it) {
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
		d.remotePlan(s, it, !stop, choice, answers, changes)
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
	d.answerPlanMsg(s, it, true, planGoChanges(planGoFull(choice, qs, ans), changes))
}

// approvalPick: the card's pick on the approval question: an index into its
// options, dontRunPick for the card's own "Don't run", or -1 for none. Until
// the user picks, the question's recommended option is the pick (user
// 2026-11-23: a radio group like the other questions).
func (v *chatView) approvalPick(plan *chat.Item, a *approvalOpts) int {
	if p, ok := v.planApprove[plan]; ok && (p >= 0 && p < len(a.Options) || p == dontRunPick && a.DontRun) {
		return p
	}
	if a != nil && a.Recommended >= 0 && a.Recommended < len(a.Options) {
		return a.Recommended
	}
	return -1
}

// approvalLabels: the approval radio group's labels: the options (the
// recommended one marked), then the card's own "Don't run" if no option stops.
func approvalLabels(a *approvalOpts) []string {
	var out []string
	for i, o := range a.Options {
		label := shortLabel(o, 60)
		if i == a.Recommended {
			label += " (recommended)"
		}
		out = append(out, label)
	}
	if a.DontRun {
		out = append(out, "Don't run")
	}
	return out
}

// approvalRadio: the approval question's options as a radio group, with the
// pick marked; it stays a pick (it can be changed) until "Send answers"
// sends it with the other questions' answers. active false: greyed.
func (v *chatView) approvalRadio(owner *deskSession, plan *chat.Item, a *approvalOpts, active bool) fyne.CanvasObject {
	labels := approvalLabels(a)
	r := widget.NewRadioGroup(labels, nil)
	if p := v.approvalPick(plan, a); p == dontRunPick {
		r.Selected = labels[len(labels)-1]
	} else if p >= 0 {
		r.Selected = labels[p]
	}
	r.OnChanged = func(sel string) {
		pick := -1
		for j, l := range labels {
			if l == sel {
				pick = j
				if a.DontRun && j == len(labels)-1 {
					pick = dontRunPick
				}
			}
		}
		if v.planApprove == nil {
			v.planApprove = map[*chat.Item]int{}
		}
		v.planApprove[plan] = pick
		v.redrawPlan(plan, planReply(owner.conv, plan))
	}
	if !active {
		r.Disable()
	}
	return r
}

// approvalSend: "Send answers" for a card whose approval question has its own
// options (with the other questions' answers, if any), with a note while that
// question has no pick. active false: greyed.
func (v *chatView) approvalSend(owner *deskSession, plan *chat.Item, a *approvalOpts, qs []planQ, ans map[int]planAnswer, active bool) []fyne.CanvasObject {
	pick := v.approvalPick(plan, a)
	stop := pick == dontRunPick || pick >= 0 && a.Stop[pick]
	enabled := active && pick != -1 && (stop || v.d.canAnswer(owner))
	send := glowButton("Send answers", theme.MailSendIcon(), colIdle, enabled, v.guarded(func() {
		v.d.answerPlanApproval(owner, plan, a, v.approvalPick(plan, a), qs, ans, "")
	}))
	out := []fyne.CanvasObject{v.buttonRow(send)}
	if pick == -1 && active {
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
	return k + "|send"
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

// asksToRun: the question (title, options opts) is about doing work. A pick
// between preferences ("Only while waiting, or keep it as is?") names none
// and is no approval question.
func asksToRun(_ string, opts []string) bool {
	for _, o := range opts {
		if chat.NamesWork(o) {
			return true
		}
	}
	return false
}
