package main

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
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
	cmd.Alignment = v.align()
	note := widget.NewLabel("Approving in chat doesn't lift this check. Approve and retry asks Claude to run exactly this command again and allows it once. Always allow also adds permission rules, so matching commands skip the check from now on.")
	note.Wrapping = fyne.TextWrapWord
	note.SizeName = theme.SizeNameCaptionText
	note.Alignment = v.align()
	s := v.s
	retry := glowButton("Approve and retry", theme.ConfirmIcon(), colIdle, s != nil, func() { v.d.retryBlocked(s, it, nil, "") })
	always := glowButton("Always allow...", theme.ConfirmIcon(), colAccent, s != nil, func() { v.d.alwaysAllowDialog(s, it) })
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
	return questionCard([]fyne.CanvasObject{title, cmd, note, v.buttonRow(retry, always, copyCmd, dismiss)}, true)
}

func (d *desk) retryBlocked(s *deskSession, it *chat.Item, rules []protocol.Rule, dest string) {
	if s = d.cardOwner(s, it); s == nil {
		return
	}
	if s.mode == "auto" {
		s.restoreMode = "auto"
		d.setMode(s, "default")
	}
	s.retryCmd = blockedCommand(it)
	s.retryRules, s.retryDest = rules, dest
	msg := retryPrefix + oneLine(blockedCommand(it), 300) +
		". Please retry exactly the same command now; Quill will allow it."
	if d.current != s {
		if p := d.paneFor(s); p != nil {
			d.focusPane(p)
		} else {
			d.open(s.id)
		}
	}
	d.sendTo(s, msg)
}

const (
	scopeSession = "Only this session"
	scopeAll     = "All sessions (saved in your Claude Code settings)"
)

var (
	shPrefixWords = map[string]bool{"do": true, "then": true, "else": true, "elif": true, "if": true, "while": true, "until": true, "!": true, "time": true, "{": true}
	shSkipWords   = map[string]bool{"for": true, "case": true, "select": true, "done": true, "fi": true, "esac": true, "}": true, "function": true}
)

func ruleWords(seg []shWord) string {
	for len(seg) > 0 && (assignName.MatchString(seg[0].text) || shPrefixWords[seg[0].text]) {
		seg = seg[1:]
	}
	if len(seg) == 0 || seg[0].unknown || shSkipWords[seg[0].text] {
		return ""
	}
	r := seg[0].text
	if len(seg) > 1 && !seg[1].unknown && subcommandWord.MatchString(seg[1].text) {
		r += " " + seg[1].text
	}
	return r
}

var subcommandWord = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

func suggestedRules(it *chat.Item) []string {
	if it.ToolName != "Bash" {
		return []string{it.ToolName}
	}
	cmd := blockedCommand(it)
	var out []string
	seen := map[string]bool{}
	add := func(w string) {
		if w != "" && !seen[w] {
			seen[w] = true
			out = append(out, protocol.Rule{Tool: "Bash", Content: w + " *"}.String())
		}
	}
	if segs, ok := shSegments(cmd); ok {
		for _, seg := range segs {
			if !readOnlySegment(seg, nil) {
				add(ruleWords(seg))
			}
		}
	}
	if len(out) == 0 {
		if f := strings.Fields(cmd); len(f) > 0 {
			add(f[0])
		}
	}
	return out
}

func parseRules(text string) ([]protocol.Rule, error) {
	var out []protocol.Rule
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		r, ok := protocol.ParseRule(line)
		if !ok {
			return nil, errors.New("not a permission rule: " + strings.TrimSpace(line) + " (write it like Bash(gio trash *) or Edit)")
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, errors.New("add at least one rule")
	}
	return out, nil
}

func (d *desk) alwaysAllowDialog(s *deskSession, it *chat.Item) {
	e := widget.NewMultiLineEntry()
	e.SetText(strings.Join(suggestedRules(it), "\n"))
	e.SetMinRowsVisible(4)
	scope := widget.NewRadioGroup([]string{scopeSession, scopeAll}, nil)
	scope.SetSelected(scopeSession)
	explain := widget.NewLabel("Quill asks Claude to retry the command, allows it, and adds these rules (one per line). " +
		"Commands that match a rule skip the auto-mode check. Keep them narrow: Bash(gio trash *) allows every gio trash command.")
	explain.Wrapping = fyne.TextWrapWord
	explain.SizeName = theme.SizeNameCaptionText
	body := container.NewBorder(nil, container.NewVBox(scope, explain), nil, nil, e)
	dlg := dialog.NewCustomConfirm("Always allow", "Allow and retry", "Cancel", body, func(ok bool) {
		if !ok {
			return
		}
		rules, err := parseRules(e.Text)
		if err != nil {
			dialog.ShowError(err, d.win)
			return
		}
		dest := "session"
		if scope.Selected == scopeAll {
			dest = "userSettings"
		}
		d.retryBlocked(s, it, rules, dest)
	}, d.win)
	dlg.Resize(fyne.NewSize(560, 380))
	dlg.Show()
}
