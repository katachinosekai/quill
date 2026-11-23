package main

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// Choice overlay (user 2026-11-23): a reply's own choice buttons (one
// question with options, or several numbered ones with a Send answers) sit
// in the reply text, under each question, with no card below it, like the
// plan overlay (planoverlay.go). A reply that isn't the latest shows its text
// as written. nil, false: the overlay can't be drawn (the lines don't line
// up), so choiceCard's card is used.

// answerAfter: the text of the user's message that answered reply it ("" if
// the next thing is no user message).
func (v *chatView) answerAfter(it *chat.Item) string {
	after := false
	for _, x := range v.s.conv.Items {
		if x == it {
			after = true
			continue
		}
		if after && x.Kind == chat.User {
			return x.Text
		}
	}
	return ""
}

// renderChoiceReply: it with its buttons in place. plain: the reply has
// choices but none to show (repeated by a plan card), draw it as plain text.
// A reply that was answered or isn't the latest keeps its buttons greyed
// with the pick marked (user 2026-11-23).
func (v *chatView) renderChoiceReply(it *chat.Item) (o fyne.CanvasObject, plain bool) {
	s := v.s
	if s == nil || it.Streaming {
		return nil, false
	}
	text, sources := chat.SplitSources(it.Text)
	lines := strings.Split(text, "\n")
	line := lines[len(lines)-1]
	_, opts, single := parseChoices(line)
	rec := -1
	if single {
		rec = choiceRecommended(line, len(opts))
	}
	var multi []numChoiceQ
	if _, mq := numberedChoiceQs(text); mq != nil {
		for _, q := range mq {
			if len(q.opts) > 0 {
				multi = mq
				break
			}
		}
	}
	if !single && multi == nil {
		return nil, false
	}
	ok, active := v.choiceGate(it)
	if !ok {
		return nil, true
	}
	answer := ""
	if !active {
		answer = v.answerAfter(it)
	}
	kind := "choice"
	if !active {
		kind = "none"
	}
	if multi == nil {
		if active {
			kind += ":" + strings.Join(opts, "/")
		}
		var btns []fyne.CanvasObject
		for n, opt := range opts {
			n, opt := n, opt
			// No check before a pick: it marks only the picked option of an
			// answered reply.
			var icon fyne.Resource
			label, tint := recommendedButton(shortLabel(opt, 60), n == rec, active, colIdle)
			if !active {
				if answer == choiceReply(n, opt) {
					icon, tint = theme.ConfirmIcon(), colAccent
				}
			}
			btns = append(btns, glowButton(label, icon, tint, active && v.d.canAnswer(s), v.guarded(func() { v.d.pickChoice(s, it, choiceReply(n, opt)) })))
		}
		box := container.NewVBox()
		if len(lines) > 1 {
			box.Add(msgText(v.markdownBlocks(strings.Join(lines[:len(lines)-1], "\n"))))
		}
		box.Add(msgText(v.markdown(line)))
		box.Add(v.fadeIn(it.Seq, v.buttonRow(btns...)))
		if sources != "" {
			box.Add(msgText(v.markdownBlocks(sources)))
		}
		v.noteButtons(it.Seq, kind)
		return box, false
	}
	// Several numbered questions: a click marks the pick (it can be
	// changed); "Send answers" sends all of them in one reply.
	for _, q := range multi {
		if q.line < 0 || q.end > len(lines) || q.line >= q.end {
			return nil, false
		}
	}
	if v.choicePicks == nil {
		v.choicePicks = map[*chat.Item]map[int]int{}
	}
	if v.choiceTexts == nil {
		v.choiceTexts = map[*chat.Item]map[int]string{}
	}
	picks, texts := v.choicePicks[it], v.choiceTexts[it]
	if picks == nil {
		picks = map[int]int{}
		v.choicePicks[it] = picks
	}
	if texts == nil {
		texts = map[int]string{}
		v.choiceTexts[it] = texts
	}
	if !active {
		picks, texts = answeredPicks(multi, answer)
	}
	box := container.NewVBox()
	prose := func(from, to int) {
		if t := keepNumbers(lines[from:to]); strings.TrimSpace(t) != "" {
			box.Add(msgText(v.markdownBlocks(t)))
		}
	}
	at, missing := 0, 0
	for _, q := range multi {
		q := q
		if q.line < at {
			return nil, false
		}
		prose(at, q.line)
		// The question as written; "2." stays 2, not a list restarting at 1
		// (a no-break space after the number is no list marker).
		qt := numLeadMD.ReplaceAllString(strings.Join(lines[q.line:q.end], "\n"), "$1$2$3\u00a0")
		box.Add(msgText(v.markdown(strings.TrimRight(qt, "\n "))))
		if len(q.opts) == 0 {
			// A question without options: a text field for the answer.
			e := widget.NewMultiLineEntry()
			e.Wrapping = fyne.TextWrapWord
			e.SetMinRowsVisible(2)
			e.SetPlaceHolder("Your answer")
			e.Text = texts[q.idx]
			e.OnChanged = func(t string) { texts[q.idx] = t }
			if !active {
				e.Disable()
			}
			box.Add(v.fadeIn(it.Seq, e))
			kind += "|" + q.num + ":text"
		} else {
			if active {
				kind += "|" + q.num + ":" + strings.Join(q.opts, "/")
			}
			var btns []fyne.CanvasObject
			for i, o := range q.opts {
				i := i
				icon := fyne.Resource(nil)
				label, tint := recommendedButton(shortLabel(o, 60), i == q.rec, active, colIdle)
				if p, ok := picks[q.idx]; ok && p == i {
					icon, tint = theme.ConfirmIcon(), colAccent
				}
				btns = append(btns, glowButton(label, icon, tint, active && v.d.canAnswer(s), v.guarded(func() {
					picks[q.idx] = i
					if iv, ok := v.views[it.Seq]; ok {
						v.refreshItem(it, iv)
					}
				})))
			}
			if _, ok := picks[q.idx]; !ok {
				missing++
			}
			box.Add(v.fadeIn(it.Seq, v.buttonRow(btns...)))
		}
		at = q.end
	}
	prose(at, len(lines))
	if sources != "" {
		box.Add(msgText(v.markdownBlocks(sources)))
	}
	send := glowButton("Send answers", theme.MailSendIcon(), colIdle, active && v.d.canAnswer(s) && missing == 0, v.guarded(func() {
		v.d.pickChoice(s, it, multiChoiceReply(multi, picks, texts))
	}))
	box.Add(v.fadeIn(it.Seq, v.buttonRow(send)))
	if missing > 0 && active {
		note := widget.NewLabel("Pick an answer for each question, then send.")
		note.SizeName = theme.SizeNameCaptionText
		note.Alignment = v.align()
		box.Add(note)
	}
	v.noteButtons(it.Seq, kind)
	return box, false
}
