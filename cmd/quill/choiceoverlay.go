package main

import (
	"image/color"
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

// renderChoiceReply: it with its buttons in place. plain: the reply has
// choices but none to show (no buttons: answered, repeated by a plan card),
// draw it as plain text.
func (v *chatView) renderChoiceReply(it *chat.Item) (o fyne.CanvasObject, plain bool) {
	s := v.s
	if s == nil || it.Streaming {
		return nil, false
	}
	text := strings.TrimRight(it.Text, " \n")
	lines := strings.Split(text, "\n")
	line := lines[len(lines)-1]
	title, opts, single := parseChoices(line)
	_ = title
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
	ok, latest := v.choiceGate(it)
	if !ok || !latest {
		return nil, true
	}
	kind := "choice"
	if multi == nil {
		kind += ":" + strings.Join(opts, "/")
		var btns []fyne.CanvasObject
		for n, opt := range opts {
			n, opt := n, opt
			btns = append(btns, glowButton(shortLabel(opt, 60), theme.ConfirmIcon(), colIdle, v.d.canAnswer(s), v.guarded(func() { v.d.pickChoice(s, it, choiceReply(n, opt)) })))
		}
		box := container.NewVBox()
		if len(lines) > 1 {
			box.Add(msgText(v.markdownBlocks(strings.Join(lines[:len(lines)-1], "\n"))))
		}
		box.Add(msgText(v.markdown(line)))
		box.Add(v.buttonRow(btns...))
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
		v.choicePicks = map[*chat.Item]map[string]int{}
	}
	picks := v.choicePicks[it]
	if picks == nil {
		picks = map[string]int{}
		v.choicePicks[it] = picks
	}
	box := container.NewVBox()
	prose := func(from, to int) {
		if t := strings.Join(lines[from:to], "\n"); strings.TrimSpace(t) != "" {
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
		// The question as written; "2." stays 2, not a list restarting at 1.
		qt := numLeadMD.ReplaceAllString(strings.Join(lines[q.line:q.end], "\n"), "$1$2$3 ")
		box.Add(msgText(v.markdown(strings.TrimRight(qt, "\n "))))
		if len(q.opts) > 0 {
			kind += "|" + q.num + ":" + strings.Join(q.opts, "/")
			var btns []fyne.CanvasObject
			for i, o := range q.opts {
				i := i
				icon, tint := fyne.Resource(nil), color.Color(colIdle)
				if p, ok := picks[q.num]; ok && p == i {
					icon, tint = theme.ConfirmIcon(), colAccent
				}
				btns = append(btns, glowButton(shortLabel(o, 60), icon, tint, v.d.canAnswer(s), v.guarded(func() {
					picks[q.num] = i
					if iv, ok := v.views[it.Seq]; ok {
						v.refreshItem(it, iv)
					}
				})))
			}
			if _, ok := picks[q.num]; !ok {
				missing++
			}
			box.Add(v.buttonRow(btns...))
		}
		at = q.end
	}
	prose(at, len(lines))
	send := glowButton("Send answers", theme.MailSendIcon(), colIdle, v.d.canAnswer(s) && missing == 0, v.guarded(func() {
		v.d.pickChoice(s, it, multiChoiceReply(multi, picks))
	}))
	box.Add(v.buttonRow(send))
	if missing > 0 {
		note := widget.NewLabel("Pick an answer for each question, then send.")
		note.SizeName = theme.SizeNameCaptionText
		box.Add(note)
	}
	v.noteButtons(it.Seq, kind)
	return box, false
}
