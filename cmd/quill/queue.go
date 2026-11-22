package main

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

func (d *desk) enqueue(s *deskSession, text string) {
	s.queue = append(s.queue, text)
	d.clearInputFor(s, text)
	d.syncQueue(s)
}

func (d *desk) clearInputFor(s *deskSession, text string) {
	if p := d.paneFor(s); p != nil && d.split() {
		if strings.TrimSpace(p.input.Text) == strings.TrimSpace(text) {
			p.input.SetText("")
		}
		return
	}
	if d.current == s && strings.TrimSpace(d.input.Text) == strings.TrimSpace(text) {
		d.input.SetText("")
	}
}

func (d *desk) flushQueue(s *deskSession) {
	if len(s.queue) == 0 || !s.running() || d.usagePaused {
		return
	}
	text := strings.Join(s.queue, "\n\n")
	s.queue = nil
	d.syncQueue(s)
	d.sendTo(s, text)
}

func (d *desk) viewFor(s *deskSession) *chatView {
	if p := d.paneFor(s); p != nil && d.split() {
		return p.view
	}
	if d.current == s {
		return d.chat
	}
	return nil
}

func (d *desk) syncQueue(s *deskSession) {
	views := []*chatView{d.single}
	for _, p := range d.panes {
		views = append(views, p.view)
	}
	for _, v := range views {
		if v != nil && v.s == s {
			d.drawQueue(v, s)
		}
	}
	if v := d.viewFor(s); v != nil && v.s != s {
		d.drawQueue(v, s)
	}
}

func (d *desk) drawQueue(v *chatView, s *deskSession) {
	if v == nil || v.queueBox == nil {
		return
	}
	var rows []fyne.CanvasObject
	if s == nil {
		v.queueBox.Objects = nil
		v.queueBox.Refresh()
		return
	}
	head := func(t string) fyne.CanvasObject {
		m := mutedText(t, fontMeta)
		if d.center {
			m.Alignment = fyne.TextAlignCenter
		}
		return m
	}
	if len(s.queue) > 0 {
		rows = append(rows, head("Queued, sent when Claude finishes · ↑ in an empty box edits the last one"))
	}
	if s.qedit != nil {
		rows = append(rows, head(fmt.Sprintf("Editing queued message %d · Enter saves it in place · ↓ or Esc puts it back unchanged", s.qedit.idx+1)))
	}
	// With centering on, queued messages follow the chat's centering rules
	// (user 2026-11-22): one centered column that scales with the chat,
	// text centered and wrapped, an overlong message wrapped balanced.
	var lines []wrapText
	for _, q := range s.queue {
		lines = append(lines, measureWords(oneLine(q, 160), theme.TextSize(), fyne.TextStyle{}))
	}
	col := func(avail float32) float32 {
		return groupWidth(avail, lines, queueRowExtra())
	}
	for i, q := range s.queue {
		i := i
		l := widget.NewLabel(oneLine(q, 160))
		l.Truncation = fyne.TextTruncateEllipsis
		if d.center {
			l.Truncation = fyne.TextTruncateOff
			l.Wrapping = fyne.TextWrapWord
			l.Alignment = fyne.TextAlignCenter
		}
		now := widget.NewButtonWithIcon("", theme.MailSendIcon(), func() { d.sendQueuedNow(s, i) })
		now.Importance = widget.LowImportance
		edit := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() { d.editQueued(s, i) })
		edit.Importance = widget.LowImportance
		del := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
			if i < len(s.queue) {
				s.queue = append(s.queue[:i], s.queue[i+1:]...)
				s.editRemoved(i)
				d.syncQueue(s)
			}
		})
		del.Importance = widget.LowImportance
		handle := container.NewCenter(sized(16, 16, widget.NewIcon(theme.MenuIcon())))
		var content fyne.CanvasObject = bg(colCard, 8, container.NewBorder(nil, nil, handle, container.NewHBox(now, edit, del), l))
		if d.center {
			c := newColumn(true, col, content)
			c.Layout.(*colLayout).onWidth = d.queueReflow
			content = c
		}
		var row *queueRow
		q := q
		row = newQueueRow(content, func(dy float32) {
			step := row.Size().Height + theme.Padding()
			if step <= 0 {
				return
			}
			from := i
			if from >= len(s.queue) || s.queue[from] != q {
				from = slices.Index(s.queue, q)
			}
			if from < 0 {
				return
			}
			d.moveQueued(s, from, from+int(math.Round(float64(dy/step))))
		})
		rows = append(rows, row)
	}
	v.queueBox.Objects = rows
	v.queueBox.Refresh()
	if d.mainPane != nil {
		relayout(d.mainPane)
	}
}

// queueReflow lays the window out once more after a queued row's column
// changed width, so wrapped rows get their new height (once per frame).
func (d *desk) queueReflow() {
	if d.queueReflowing || d.mainPane == nil {
		return
	}
	d.queueReflowing = true
	d.afterUI(0, func() {
		d.queueReflowing = false
		relayoutTree(d.mainPane)
		canvas.Refresh(d.mainPane)
	})
}

// queueRowExtra: what a queued row adds around its text (drag handle,
// three icon buttons, label and card padding).
func queueRowExtra() float32 {
	btn := theme.IconInlineSize() + 2*theme.InnerPadding()
	return 16 + 3*btn + 4*theme.Padding() + 2*theme.InnerPadding() + 2*8
}

func (d *desk) inputFor(s *deskSession) *sendEntry {
	if p := d.paneFor(s); p != nil && d.split() {
		return p.input
	}
	return d.input
}

type queueEdit struct {
	idx   int
	text  string
	draft string
}

func (d *desk) boxDraft(s *deskSession, in *sendEntry) string {
	if s != nil && s.qedit != nil {
		return s.qedit.draft
	}
	if in == nil {
		return ""
	}
	return in.Text
}

func (d *desk) idleForQueue(s *deskSession) bool {
	return s.running() && !s.conv.Busy && !s.proc.Busy() && !s.compacting
}

func (s *deskSession) editRemoved(i int) {
	if s.qedit != nil && i < s.qedit.idx {
		s.qedit.idx--
	}
}

func (s *deskSession) editInserted(i int) {
	if s.qedit != nil && i <= s.qedit.idx {
		s.qedit.idx++
	}
}

type queueRow struct {
	widget.BaseWidget
	content fyne.CanvasObject
	dy      float32
	onDrop  func(dy float32)
}

func newQueueRow(content fyne.CanvasObject, onDrop func(dy float32)) *queueRow {
	r := &queueRow{content: content, onDrop: onDrop}
	r.ExtendBaseWidget(r)
	return r
}

func (r *queueRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.content)
}

func (r *queueRow) Dragged(e *fyne.DragEvent) {
	r.dy += e.Dragged.DY
	r.content.Move(fyne.NewPos(0, r.dy))
	r.content.Refresh()
}

func (r *queueRow) DragEnd() {
	dy := r.dy
	r.dy = 0
	r.content.Move(fyne.NewPos(0, 0))
	if r.onDrop != nil {
		r.onDrop(dy)
	}
}

func (d *desk) moveQueued(s *deskSession, from, to int) {
	if from < 0 || from >= len(s.queue) {
		return
	}
	to = max(0, min(to, len(s.queue)-1))
	if to != from {
		item := s.queue[from]
		rest := append(append([]string{}, s.queue[:from]...), s.queue[from+1:]...)
		s.editRemoved(from)
		s.queue = append(append(append([]string{}, rest[:to]...), item), rest[to:]...)
		s.editInserted(to)
	}
	d.syncQueue(s)
}

func (d *desk) sendQueuedNow(s *deskSession, i int) {
	if i < 0 || i >= len(s.queue) {
		return
	}
	if s.compacting || s.takeover || s.loading || s.terminal != nil && !s.running() {
		d.notify(s, "This session can't take a message right now; it stays queued.")
		return
	}
	text := s.queue[i]
	s.queue = append(s.queue[:i], s.queue[i+1:]...)
	s.editRemoved(i)
	d.syncQueue(s)
	d.sendTo(s, text)
}

func (d *desk) cancelQueueEdit(s *deskSession) bool {
	if s == nil || s.qedit == nil {
		return false
	}
	q := s.qedit
	s.qedit = nil
	idx := min(q.idx, len(s.queue))
	s.queue = append(append(append([]string{}, s.queue[:idx]...), q.text), s.queue[idx:]...)
	d.inputFor(s).SetText(q.draft)
	d.syncQueue(s)
	if d.idleForQueue(s) {
		d.flushQueue(s)
	}
	return true
}

func (d *desk) editQueued(s *deskSession, i int) {
	if i < 0 || i >= len(s.queue) {
		return
	}
	if s.qedit != nil {
		d.cancelQueueEdit(s)
	}
	text := s.queue[i]
	draft := d.inputFor(s).Text
	s.queue = append(s.queue[:i], s.queue[i+1:]...)
	s.qedit = &queueEdit{idx: i, text: text, draft: draft}
	in := d.inputFor(s)
	in.SetText(text)
	in.CursorRow = strings.Count(text, "\n")
	in.CursorColumn = len([]rune(text[strings.LastIndex(text, "\n")+1:]))
	in.Refresh()
	d.win.Canvas().Focus(in)
	d.syncQueue(s)
}

type history struct {
	idx   int
	draft string
	sid   string // the session whose box this is (tabs share one box)
}

// upAgain is how soon a second Up must follow to leave a new draft for the
// queue or earlier messages (user 2026-11-21).
const upAgain = 600 * time.Millisecond

func userHistory(s *deskSession) []string {
	var out []string
	seen := map[string]bool{}
	items := s.conv.Items
	for i := len(items) - 1; i >= 0 && len(out) < 200; i-- {
		it := items[i]
		t := strings.TrimSpace(it.Text)
		if it.Kind != chat.User || t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, it.Text)
	}
	return out
}

// historyKey: Up/Down in a message box (user 2026-11-21).
//   - While writing, Up and Down move through the lines (wrapped lines too);
//     only on the top line does Up leave the text. With something typed, that
//     takes two presses within upAgain (the first shows a hint); an empty box
//     needs one. Up pulls back the last queued message if any, else recalls
//     earlier messages.
//   - While browsing earlier messages, Up and Down change the message
//     directly; Down past the newest brings the draft back.
//   - The draft is kept from the first Up until the box is sent or cleared,
//     whatever is edited or moved meanwhile.
func (d *desk) historyKey(e *sendEntry, s *deskSession, k *fyne.KeyEvent) bool {
	if e.hist != nil && (e.Text == "" || s == nil || e.hist.sid != s.id) {
		e.hist = nil // sent, cleared or another tab: the draft is done
	}
	if s != nil && s.qedit != nil && k.Name == fyne.KeyEscape {
		return d.cancelQueueEdit(s)
	}
	if s == nil || (k.Name != fyne.KeyUp && k.Name != fyne.KeyDown) || e.shift {
		return false
	}
	if e.hist != nil {
		list := userHistory(s)
		if k.Name == fyne.KeyUp {
			if e.hist.idx+1 < len(list) {
				e.hist.idx++
				e.setHistoryText(list[e.hist.idx])
			}
			return true
		}
		e.hist.idx--
		if e.hist.idx < 0 || e.hist.idx >= len(list) {
			draft := e.hist.draft
			e.hist = nil
			e.setHistoryText(draft)
			return true
		}
		e.setHistoryText(list[e.hist.idx])
		return true
	}
	if k.Name == fyne.KeyDown {
		row := e.CursorRow
		e.Entry.TypedKey(k)
		if row == e.CursorRow && s.qedit != nil {
			return d.cancelQueueEdit(s) // Down on the last line undoes a queued-message edit
		}
		return true
	}
	if e.CursorRow > 0 {
		return false // move up a line
	}
	if strings.TrimSpace(e.Text) != "" {
		now := time.Now()
		if e.upArmed.IsZero() || now.Sub(e.upArmed) > upAgain {
			e.upArmed = now
			e.Entry.TypedKey(k) // to the start of the line, like a normal Up
			if e.onFix != nil && (len(s.queue) > 0 || len(userHistory(s)) > 0) {
				e.onFix("Press ↑ again for earlier messages")
			}
			return true
		}
	}
	e.upArmed = time.Time{}
	if len(s.queue) > 0 && s.qedit == nil {
		d.editQueued(s, len(s.queue)-1)
		return true
	}
	list := userHistory(s)
	if len(list) == 0 {
		return false
	}
	e.hist = &history{idx: 0, draft: e.Text, sid: s.id}
	e.setHistoryText(list[0])
	return true
}

func (e *sendEntry) setHistoryText(text string) {
	e.browsing = true
	e.SetText(text)
	e.browsing = false
	e.SetCursorTextOffset(len([]rune(text)))
}

func (d *desk) syncVisibleQueues() {
	var shown []*deskSession
	if d.split() {
		for _, p := range d.panes {
			if p.view.s != nil {
				shown = append(shown, p.view.s)
			}
		}
	} else if d.current != nil {
		shown = append(shown, d.current)
	}
	for _, s := range shown {
		d.syncQueue(s)
		if len(s.queue) > 0 && s.qedit == nil && d.idleForQueue(s) {
			d.flushQueue(s)
		}
	}
}
