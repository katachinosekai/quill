package main

import (
	"net/url"
	"strings"

	"fyne.io/fyne/v2/dialog"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

const upgradeURL = "https://claude.ai/upgrade"

var localCommands = []protocol.Command{
	{Name: "upgrade", Description: "Open the plan upgrade page in your browser (handled by Quill)"},
	{Name: "login", Description: "Sign in to your Claude account (handled by Quill)"},
	{Name: "logout", Description: "Sign out of your Claude account (handled by Quill)"},
	{Name: "clear", Description: "Start a fresh session in the same folder (handled by Quill)"},
	{Name: "model", Description: "Switch the model for this session (handled by Quill)", ArgumentHint: "<default|fable|opus|sonnet|haiku>"},
	{Name: "exit", Description: "End this session's Claude process (handled by Quill)"},
}

func (d *desk) runLocalCommand(text string) bool {
	if !strings.HasPrefix(text, "/") {
		return false
	}
	name, arg, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
	arg = strings.TrimSpace(arg)
	s := d.current
	note := func(msg string) {
		if s != nil {
			d.chatFor(s, []*chat.Item{s.conv.AddNotice(msg)})
		}
	}
	switch name {
	case "upgrade":
		if u, err := url.Parse(upgradeURL); err == nil {
			_ = d.app.OpenURL(u)
		}
		note("Opened the upgrade page in your browser.")
	case "login":
		d.signIn()
	case "logout":
		d.accountMenu()
	case "clear":
		if s == nil {
			return false
		}
		d.createSession(s.cwd, s.model, s.mode)
	case "model":
		if s == nil {
			return false
		}
		if arg == "" || !contains(modelChoices, arg) {
			dialog.ShowInformation("Model", "Use /model with one of: "+strings.Join(modelChoices, ", "), d.win)
			return true
		}
		d.setModel(s, arg)
		note("Model set to " + arg + ".")
	case "exit", "quit":
		if s == nil {
			return false
		}
		d.endSession(s.id)
		note("Session ended. Send a message to resume it.")
	default:
		return false
	}
	d.input.SetText("")
	d.updateChrome()
	return true
}
