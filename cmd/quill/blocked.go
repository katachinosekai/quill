package main

import (
	"encoding/json"
	"regexp"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

var blockedReason = regexp.MustCompile(`Reason:\s*\[([^\]]+)\]`)

func autoBlocked(it *chat.Item) bool {
	return it.Kind == chat.Tool && it.ResultError && strings.Contains(it.Result, "auto mode classifier")
}

func blockedCommand(it *chat.Item) string {
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(it.Input, &in) == nil && in.Command != "" {
		return in.Command
	}
	return toolHeader(it)
}

func (v *chatView) blockedCard(it *chat.Item) fyne.CanvasObject {
	reason := "blocked"
	if m := blockedReason.FindStringSubmatch(it.Result); m != nil {
		reason = m[1]
	}
	title := widget.NewLabel("Blocked by Claude Code's auto-mode safety check: " + reason)
	title.TextStyle.Bold = true
	title.Wrapping = fyne.TextWrapWord
	title.Alignment = v.align()
	cmd := mono(oneLine(blockedCommand(it), 400))
	note := widget.NewLabel("Approving in chat doesn't lift this check. Approve and retry asks Claude to try again with normal approval prompts for this reply, so you confirm it in Quill.")
	note.Wrapping = fyne.TextWrapWord
	note.SizeName = theme.SizeNameCaptionText
	note.Alignment = v.align()
	s := v.s
	retry := glowButton("Approve and retry", theme.ConfirmIcon(), colIdle, s != nil, func() { v.d.retryBlocked(s, it) })
	copyCmd := glowButton("Copy command", theme.ContentCopyIcon(), colAccent, true, func() {
		v.d.app.Clipboard().SetContent(blockedCommand(it))
	})
	dismiss := glowButton("Dismiss", theme.CancelIcon(), colMuted, true, func() {
		if v.dismissed == nil {
			v.dismissed = map[*chat.Item]bool{}
		}
		v.dismissed[it] = true
		h := v.runHead(it)
		if iv, ok := v.views[h.Seq]; ok {
			v.refreshItem(h, iv)
		}
	})
	return questionCard([]fyne.CanvasObject{title, cmd, note, v.buttonRow(retry, copyCmd, dismiss)}, true)
}

func (d *desk) retryBlocked(s *deskSession, it *chat.Item) {
	if s = d.cardOwner(s, it); s == nil {
		return
	}
	if s.mode == "auto" {
		s.restoreMode = "auto"
		d.setMode(s, "default")
	}
	s.retryCmd = blockedCommand(it)
	msg := retryPrefix + oneLine(blockedCommand(it), 300) +
		". Please retry it now; Quill will ask me to confirm it."
	if d.current != s {
		if p := d.paneFor(s); p != nil {
			d.focusPane(p)
		} else {
			d.open(s.id)
		}
	}
	d.sendTo(s, msg)
}
