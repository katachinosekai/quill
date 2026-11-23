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
	{Name: "rename", Description: "Rename this session's tab and its kmem name (handled by Quill)", ArgumentHint: "[name]"},
	{Name: "remote-control", Description: "Remote Control through claude.ai for this session, like Remote under the message box (handled by Quill)", ArgumentHint: "[on|off]"},
	{Name: "rc", Description: "Same as /remote-control (handled by Quill)", ArgumentHint: "[on|off]"},
}

// terminalOnly are Claude Code commands that only change its terminal
// screen; Quill Desktop hides them and explains instead of sending them.
var terminalOnly = map[string]string{
	"color": "/color sets the prompt bar color in Claude Code's terminal; Quill Desktop's colors are in Settings.",
	"focus": "/focus switches Claude Code's terminal to a focus view; it doesn't change anything in Quill Desktop.",
}

func (d *desk) runLocalCommand(text string) bool {
	if !d.runLocalCommandFor(d.current, text) {
		return false
	}
	d.input.SetText("")
	return true
}

var deviceBlocked = map[string]bool{"upgrade": true, "login": true, "logout": true, "mcp": true, "chrome": true, "tasks": true,
	"permissions": true, "fork": true, "branch": true, "copy": true, "export": true, "diff": true, "memory": true, "hooks": true,
	"agents": true, "add-dir": true, "rewind": true}

func deviceCommandBlocked(text string) bool {
	if !strings.HasPrefix(text, "/") {
		return false
	}
	name, arg, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
	return deviceBlocked[name] || name == "rename" && strings.TrimSpace(arg) == ""
}

func (d *desk) runLocalCommandFor(s *deskSession, text string) bool {
	if !strings.HasPrefix(text, "/") {
		return false
	}
	name, arg, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
	arg = strings.TrimSpace(arg)
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
		d.clearSession(s)
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
	case "rename":
		if s == nil {
			return false
		}
		if s.remote != nil {
			note("This session runs on another PC; rename it in Quill Desktop there.")
		} else if arg != "" {
			d.renameSession(s.id, arg)
			note("Renamed this session to “" + arg + "”.")
		} else {
			title, _, _ := d.rowText(s.id)
			d.askSessionName(s.id, title)
		}
	case "remote-control", "rc":
		if s == nil {
			return false
		}
		if s.remote != nil {
			note("This session runs on another PC; turn Remote Control on or off in Quill Desktop there.")
			return true
		}
		switch strings.ToLower(arg) {
		case "on":
			if !s.rcOn {
				d.setRemoteControl(s, true)
			} else {
				note("Remote Control is already on: " + s.rcURL)
			}
		case "off":
			if s.rcOn {
				d.setRemoteControl(s, false)
			} else {
				note("Remote Control is already off.")
			}
		default:
			d.setRemoteControl(s, !s.rcOn)
		}
	case "color", "focus":
		note(terminalOnly[name])
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
			d.cmdHooks(s)
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
	d.updateChrome()
	return true
}
