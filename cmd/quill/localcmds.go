package main

import (
	"net/url"
	"strings"

	"fyne.io/fyne/v2/dialog"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/session"
)

const upgradeURL = "https://claude.ai/upgrade"

var localCommands = []protocol.Command{
	{Name: "upgrade", Description: "Open the plan upgrade page in your browser (handled by Quill)"},
	{Name: "login", Description: "Sign in to your Claude account (handled by Quill)"},
	{Name: "logout", Description: "Sign out of your Claude account (handled by Quill)"},
	{Name: "clear", Description: "Start a fresh session in the same folder (handled by Quill)"},
	{Name: "model", Description: "Switch the model for this session (handled by Quill)", ArgumentHint: "<default|fable|opus|sonnet|haiku|ollama:MODEL>"},
	{Name: "exit", Description: "End this session's Claude process (handled by Quill)"},
	{Name: "mcp", Description: "MCP servers: status, reconnect, enable/disable, sign in (handled by Quill)"},
	{Name: "chrome", Description: "Claude in Chrome for this session (handled by Quill)", ArgumentHint: "[on|off]"},
	{Name: "tasks", Description: "Background tasks of this session, with Stop and Log (handled by Quill)"},
	{Name: "permissions", Description: "Your allow, ask and deny rules, with Remove (handled by Quill)"},
	{Name: "fork", Description: "Copy this session into a new one to try another direction (handled by Quill)"},
	{Name: "branch", Description: "Same as /fork (handled by Quill)"},
	{Name: "copy", Description: "Copy Claude's last reply (handled by Quill)"},
	{Name: "export", Description: "Save the conversation as Markdown (handled by Quill)", ArgumentHint: "[file]"},
	{Name: "diff", Description: "Show the git changes in this session's folder (handled by Quill)"},
	{Name: "memory", Description: "Open CLAUDE.md and memory files (handled by Quill)"},
	{Name: "hooks", Description: "Show the configured hooks (handled by Quill)"},
	{Name: "agents", Description: "List the configured subagents (handled by Quill)"},
	{Name: "add-dir", Description: "Give this session another working folder (handled by Quill)", ArgumentHint: "[folder]"},
	{Name: "rewind", Description: "Go back to an earlier message, optionally undoing file changes (handled by Quill)"},
	{Name: "usage", Description: "Plan usage now, even mid-turn (handled by Quill)"},
	{Name: "cost", Description: "This session's cost and turns (handled by Quill)"},
	{Name: "context", Description: "This session's context use (handled by Quill)"},
	{Name: "status", Description: "This session's model, mode, folder and state (handled by Quill)"},
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
		if arg == "" || !contains(modelChoices, arg) && session.LocalModel(arg) == "" {
			dialog.ShowInformation("Model", "Use /model with one of: "+strings.Join(modelChoices, ", "), d.win)
			return true
		}
		d.setModel(s, arg)
		note("Model set to " + arg + ".")
	case "mcp", "chrome", "tasks", "permissions", "fork", "branch", "copy", "export", "diff", "memory", "hooks", "agents", "add-dir", "rewind", "usage", "cost", "context", "status":
		if s == nil {
			return false
		}
		switch name {
		case "mcp":
			d.cmdMCP(s)
		case "chrome":
			d.cmdChrome(s, arg)
		case "tasks":
			d.cmdTasks(s)
		case "permissions":
			d.cmdPermissions(s)
		case "fork", "branch":
			d.cmdFork(s)
		case "copy":
			d.cmdCopy(s)
		case "export":
			d.cmdExport(s, arg)
		case "diff":
			d.cmdDiff(s)
		case "memory":
			d.cmdMemory(s)
		case "hooks":
			d.textDialog("Hooks", hooksText(s.cwd))
		case "agents":
			d.cmdAgents(s)
		case "add-dir":
			d.cmdAddDir(s, arg)
		case "rewind":
			d.cmdRewind(s)
		case "usage":
			d.cmdUsage(s)
		case "cost":
			d.cmdCost(s)
		case "context":
			d.cmdContext(s)
		case "status":
			d.cmdStatus(s)
		}
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
