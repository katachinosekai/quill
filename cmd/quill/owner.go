package main

import (
	"fyne.io/fyne/v2/dialog"

	"github.com/katachinosekai/quill/internal/chat"
)

func convHas(c *chat.Conversation, it *chat.Item) bool {
	if c == nil || it == nil {
		return false
	}
	for i := len(c.Items) - 1; i >= 0; i-- {
		if c.Items[i] == it {
			return true
		}
	}
	return false
}

func (d *desk) ownerOf(hint *deskSession, it *chat.Item) *deskSession {
	if hint != nil && convHas(hint.conv, it) {
		return hint
	}
	for _, s := range d.sessions {
		if convHas(s.conv, it) {
			return s
		}
	}
	return nil
}

func (d *desk) cardOwner(hint *deskSession, it *chat.Item) *deskSession {
	s := d.ownerOf(hint, it)
	if s == nil {
		dialog.ShowInformation("Not sent", "The session this card belongs to is no longer open in Quill (it was closed or replaced, for example during an update), so nothing was sent. Open that session and answer there.", d.win)
	}
	return s
}

func replied(s *deskSession, it *chat.Item) bool {
	after := false
	for _, x := range s.conv.Items {
		if x == it {
			after = true
			continue
		}
		if after && (x.Kind == chat.Assistant || x.Kind == chat.Tool || x.Kind == chat.Thinking) {
			return true
		}
	}
	return false
}
