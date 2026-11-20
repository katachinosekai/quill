package main

import (
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
	if len(s.queue) == 0 || !s.running() {
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
	for i, q := range s.queue {
		i := i
		l := widget.NewLabel(oneLine(q, 160))
		l.Truncation = fyne.TextTruncateEllipsis
		edit := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() { d.editQueued(s, i) })
		edit.Importance = widget.LowImportance
		del := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
			if i < len(s.queue) {
				s.queue = append(s.queue[:i], s.queue[i+1:]...)
				d.syncQueue(s)
			}
		})
		del.Importance = widget.LowImportance
		rows = append(rows, bg(colCard, 8, container.NewBorder(nil, nil, nil, container.NewHBox(edit, del), l)))
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

func (d *desk) editQueued(s *deskSession, i int) {
	if i < 0 || i >= len(s.queue) {
		return
	}
	text := s.queue[i]
	s.queue = append(s.queue[:i], s.queue[i+1:]...)
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
