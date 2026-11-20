package main

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2/dialog"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/transcript"
)

const (
	handoffItems    = 40
	handoffItemMax  = 1500
	handoffTotalMax = 16000
)

func handoffPrompt(oldID, path, title string, conv *chat.Conversation) string {
	var parts []string
	total := 0
	items := conv.Items
	for i := len(items) - 1; i >= 0 && len(parts) < handoffItems; i-- {
		it := items[i]
		var who string
		switch it.Kind {
		case chat.User:
			who = "User"
		case chat.Assistant:
			who = "Claude"
		default:
			continue
		}
		text := strings.TrimSpace(it.Text)
		if text == "" {
			continue
		}
		if r := []rune(text); len(r) > handoffItemMax {
			text = string(r[:handoffItemMax]) + " [...]"
		}
		if total+len(text) > handoffTotalMax {
			break
		}
		total += len(text)
		parts = append(parts, who+": "+text)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are taking over from a previous Claude Code session named %q (session %s), which was closed so the work could continue in a fresh session.", title, oldID)
	if path != "" {
		fmt.Fprintf(&b, " Its full transcript is at %s if you need more detail.", path)
	}
	b.WriteString(" Take over its name and its job: other sessions may message you by this name, so answer them as that session would.\n\nIts most recent conversation:\n\n")
	b.WriteString(strings.Join(parts, "\n\n"))
	b.WriteString("\n\nStart by summarizing in two or three lines where the work stands, then continue with the next step. Ask me before anything risky or irreversible.")
	return b.String()
}

func (d *desk) askRelaunch(id string) {
	title, _, _ := d.rowText(id)
	dialog.ShowConfirm("Restart as a new session?",
		fmt.Sprintf("“%s” will be closed and moved to Archive. A fresh session with the same name, folder, model and mode starts and takes over its job from its recent conversation.", title),
		func(ok bool) {
			if ok {
				d.relaunchSession(id)
			}
		}, d.win)
}

func (d *desk) relaunchSession(id string) {
	old := d.session(id)
	if old == nil {
		return
	}
	title, _, _ := d.rowText(id)
	path := ""
	if info, ok := d.infos[id]; ok {
		path = info.Path
	}
	conv := old.conv
	if !old.loaded && path != "" {
		if c, err := transcript.Load(path); err == nil && c != nil {
			conv = c
		}
	}
	prompt := handoffPrompt(id, path, title, conv)
	place := d.placement(id)
	if old.running() {
		p := old.proc
		old.stopping = true
		go func() { _ = p.Stop(5 * time.Second) }()
	}
	ns := d.createSession(old.cwd, old.model, old.mode)
	d.org.Titles[ns.id] = title
	if strings.HasPrefix(place, folderPrefix) || place == placeAuto {
		d.org.Place[ns.id] = place
	}
	d.org.Place[id] = placeArchive
	var tabs []string
	replaced := false
	for _, t := range d.tabs {
		switch t {
		case ns.id:
			continue
		case id:
			tabs = append(tabs, ns.id)
			replaced = true
		default:
			tabs = append(tabs, t)
		}
	}
	if !replaced {
		tabs = append(tabs, ns.id)
	}
	d.tabs = tabs
	d.saveTabs()
	d.saveOrg()
	d.open(ns.id)
	d.sendTo(ns, prompt)
}
