package main

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
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
	v := d.viewFor(s)
	if v == nil || v.queueBox == nil {
		return
	}
	var rows []fyne.CanvasObject
	if len(s.queue) > 0 {
		rows = append(rows, mutedText("Queued, sent when Claude finishes · ↑ in an empty box edits the last one", fontMeta))
	}
	if s.qedit != nil {
		rows = append(rows, mutedText(fmt.Sprintf("Editing queued message %d · Enter saves it in place · ↓ or Esc puts it back unchanged", s.qedit.idx+1), fontMeta))
	}
	for i, q := range s.queue {
		i := i
		l := widget.NewLabel(oneLine(q, 160))
		l.Truncation = fyne.TextTruncateEllipsis
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
		content := bg(colCard, 8, container.NewBorder(nil, nil, handle, container.NewHBox(now, edit, del), l))
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
}

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

func (d *desk) historyKey(e *sendEntry, s *deskSession, k *fyne.KeyEvent) bool {
	if s != nil && s.qedit != nil && (k.Name == fyne.KeyEscape || k.Name == fyne.KeyDown && e.CursorRow == strings.Count(e.Text, "\n")) {
		return d.cancelQueueEdit(s)
	}
	if s == nil || (k.Name != fyne.KeyUp && k.Name != fyne.KeyDown) {
		return false
	}
	if k.Name == fyne.KeyUp {
		if e.CursorRow != 0 {
			return false
		}
		if e.hist == nil && strings.TrimSpace(e.Text) == "" && len(s.queue) > 0 {
			d.editQueued(s, len(s.queue)-1)
			return true
		}
		list := userHistory(s)
		if e.hist == nil {
			if len(list) == 0 {
				return false
			}
			e.hist = &history{idx: -1, draft: e.Text}
		}
		if e.hist.idx+1 >= len(list) {
			return true
		}
		e.hist.idx++
		e.setHistoryText(list[e.hist.idx])
		return true
	}
	if e.hist == nil || e.CursorRow != strings.Count(e.Text, "\n") {
		return false
	}
	list := userHistory(s)
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

func (e *sendEntry) setHistoryText(text string) {
	e.browsing = true
	e.SetText(text)
	e.browsing = false
	e.CursorRow = strings.Count(text, "\n")
	e.CursorColumn = len([]rune(text[strings.LastIndex(text, "\n")+1:]))
	e.Refresh()
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
