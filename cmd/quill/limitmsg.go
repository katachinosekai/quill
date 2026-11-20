package main

import (
	"strings"

	"github.com/katachinosekai/quill/internal/chat"
)

func isLimitText(text string) bool {
	t := strings.TrimSpace(text)
	return strings.HasPrefix(t, "You've hit your") && strings.Contains(t, "limit")
}

func (d *desk) checkLimitMessage(s *deskSession, items []*chat.Item) {
	for _, it := range items {
		if it.Kind == chat.Assistant && !it.Streaming && isLimitText(it.Text) {
			d.limitMsg = strings.TrimSpace(it.Text)
			s.limitSeen = d.limitMsg
			for _, o := range d.sessions {
				d.copyLimit(o)
			}
			return
		}
	}
}

func (d *desk) copyLimit(s *deskSession) {
	if d.limitMsg == "" || s == nil || s.limitSeen == d.limitMsg || !s.loaded {
		return
	}
	s.limitSeen = d.limitMsg
	for _, it := range s.conv.Items {
		if (it.Kind == chat.Assistant || it.Kind == chat.Notice) && strings.TrimSpace(it.Text) == d.limitMsg {
			return
		}
	}
	d.chatFor(s, []*chat.Item{s.conv.AddNotice(d.limitMsg)})
}

func (d *desk) clearLimitIfAllowed() {
	if d.rate == nil || d.limitMsg == "" {
		return
	}
	for _, name := range []string{"five_hour", "seven_day"} {
		if w, ok := d.rate.Window(name); ok && (w.Status == "rejected" || w.Utilization >= 1) {
			return
		}
	}
	if d.rate.Status != "rejected" {
		d.limitMsg = ""
	}
}
