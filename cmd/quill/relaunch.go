package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"fyne.io/fyne/v2/dialog"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/session"
	"github.com/katachinosekai/quill/internal/transcript"
)

const (
	handoffItems     = 40
	handoffItemMax   = 1500
	handoffTotalMax  = 16000
	recentItems      = 6
	recentItemMax    = 800
	recentTotalMax   = 4000
	compactItemMax   = 4000
	compactToolMax   = 200
	compactInputMax  = 120000
	compactTimeout   = 3 * time.Minute
	compactSystemMsg = "You compact a Claude Code session so a fresh session can take over its work. The conversation is inside <conversation>. Write a concise hand-off brief in Markdown with these sections: Goal; Done so far; Current state; Next steps; Open questions and pending user requests; Constraints and preferences the user stated; Key files, commands and ids. Keep facts exact (paths, names, versions, commit ids). Leave out chit-chat and finished details that no longer matter. Never invent anything. Stay under 600 words. Output only the brief."
)

func clip(text string, n int) string {
	text = strings.TrimSpace(text)
	if r := []rune(text); len(r) > n {
		return string(r[:n]) + " [...]"
	}
	return text
}

func recentParts(conv *chat.Conversation, n, itemMax, totalMax int) []string {
	var parts []string
	total := 0
	items := conv.Items
	for i := len(items) - 1; i >= 0 && len(parts) < n; i-- {
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
		text := clip(it.Text, itemMax)
		if text == "" {
			continue
		}
		if total+len(text) > totalMax {
			break
		}
		total += len(text)
		parts = append(parts, who+": "+text)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return parts
}

func compactSource(conv *chat.Conversation) string {
	var parts []string
	total := 0
	items := conv.Items
	for i := len(items) - 1; i >= 0; i-- {
		it := items[i]
		var line string
		switch it.Kind {
		case chat.User:
			line = "User: " + clip(it.Text, compactItemMax)
		case chat.Assistant:
			line = "Claude: " + clip(it.Text, compactItemMax)
		case chat.Tool:
			what := it.Summary
			if what == "" {
				what = string(it.Input)
			}
			line = "[" + it.ToolName + "] " + clip(what, compactToolMax)
			if it.ResultError {
				line += " (failed)"
			}
		case chat.Error:
			line = "Error: " + clip(it.Text, compactToolMax)
		default:
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if total+len(line) > compactInputMax {
			break
		}
		total += len(line)
		parts = append(parts, line)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "\n")
}

func (d *desk) claudeCompact(ctx context.Context, model, text string) (string, error) {
	args := []string{"-p", "--tools", "", "--max-turns", "1", "--no-session-persistence", "--output-format", "text", "--system-prompt", compactSystemMsg}
	if m := session.LocalModel(model); m != "" {
		args = append(args, "--model", m)
	} else if model != "" && model != "default" {
		args = append(args, "--model", model)
	}
	cmd := exec.CommandContext(ctx, d.cfg.ClaudeBin, args...)
	cmd.Env = append(append(os.Environ(), session.LocalEnv(model)...), d.localEnv(model)...)
	cmd.Dir = privateDir()
	cmd.Stdin = strings.NewReader("<conversation>\n" + text + "\n</conversation>")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v %s", err, clip(errb.String(), 300))
	}
	res := strings.TrimSpace(out.String())
	if res == "" {
		return "", errors.New("the summary came back empty")
	}
	return clip(res, 12000), nil
}

func handoffPrompt(oldID, path, title string, conv *chat.Conversation, brief string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are taking over from a previous Claude Code session named %q (session %s), which was closed so the work could continue in a fresh session.", title, oldID)
	if path != "" {
		fmt.Fprintf(&b, " Its full transcript is at %s if you need more detail.", path)
	}
	b.WriteString(" Take over its name and its job: other sessions may message you by this name, so answer them as that session would.\n\n")
	if strings.TrimSpace(brief) != "" {
		b.WriteString("Compacted brief of its work:\n\n")
		b.WriteString(strings.TrimSpace(brief))
		if parts := recentParts(conv, recentItems, recentItemMax, recentTotalMax); len(parts) > 0 {
			b.WriteString("\n\nIts last messages (trimmed):\n\n")
			b.WriteString(strings.Join(parts, "\n\n"))
		}
	} else {
		b.WriteString("Its most recent conversation:\n\n")
		b.WriteString(strings.Join(recentParts(conv, handoffItems, handoffItemMax, handoffTotalMax), "\n\n"))
	}
	b.WriteString("\n\nStart by summarizing in two or three lines where the work stands, then continue with the next step. Ask me before anything risky or irreversible.")
	return b.String()
}

func (d *desk) askRelaunch(id string) {
	title, _, _ := d.rowText(id)
	if d.endCheckFor(d.session(id)) {
		dialog.ShowConfirm("Restart this topic?", restartTopicText(title, d.topicTitleOf(id)),
			func(ok bool) {
				if ok {
					d.rotateTopic(id)
				}
			}, d.win)
		return
	}
	if name := d.org.Kmem[id]; name != "" && d.kmemAvailable() {
		// An old-name session (not merged into a topic yet) keeps the
		// handoff save (decision f5d45ef).
		dialog.ShowConfirm("Restart from kmem?",
			fmt.Sprintf("“%s” is asked to save its kmem handoff “%s”. Once it is saved, a fresh session with the same name, folder, model and mode resumes from it, and this session is deleted (its knowledge is in kmem). If the handoff isn't saved, nothing is deleted.", title, name),
			func(ok bool) {
				if ok {
					d.relaunchViaKmem(id, name)
				}
			}, d.win)
		return
	}
	closed := "will be closed and moved to Archive"
	if d.placesRemoved() {
		closed = "will be closed"
	}
	dialog.ShowConfirm("Restart as a new session?",
		fmt.Sprintf("“%s” %s. A fresh session with the same name, folder, model and mode starts. Claude compacts the old conversation into a short brief, and the new session takes over from that brief and the last few messages. The old session's history is kept.", title, closed),
		func(ok bool) {
			if ok {
				d.relaunchSession(id)
			}
		}, d.win)
}

// restartTopicText: the restart dialog of a topic session (rotation through
// the end-of-session check; no handoff).
func restartTopicText(title, topic string) string {
	return fmt.Sprintf("“%s” runs its end-of-session check first (claimed tasks checkpointed or released, status fact current). Once it passes, a fresh %s session starts from the topic's kmem briefing and this session is deleted (its state is in kmem). If the check does not pass, nothing is deleted.", title, topic)
}

// topicTitleOf: the title of the topic a session coordinates.
func (d *desk) topicTitleOf(id string) string {
	slug := d.topicOf(id)
	if t, ok := cachedTopic(slug); ok && t.Title != "" {
		return t.Title
	}
	return topicTitle(slug)
}

func (d *desk) clearSession(s *deskSession) {
	if name := d.org.Kmem[s.id]; name != "" && d.kmemAvailable() {
		d.relaunchViaKmem(s.id, name)
		return
	}
	if s.running() && (s.conv.Busy || s.proc.Busy()) {
		s.clearAfterTurn = true
		d.notify(s, "Clearing once Claude finishes this turn.")
		return
	}
	d.clearNow(s)
}

func (d *desk) clearNow(s *deskSession) {
	s.clearAfterTurn = false
	title, _, _ := d.rowText(s.id)
	if s.running() {
		p := s.proc
		s.stopping = true
		go func() { _ = p.Stop(5 * time.Second) }()
	}
	queued := s.queue
	s.queue = nil
	d.syncQueue(s)
	ns := d.replaceWith(s, title)
	d.putPlace(s.id, placeInactive)
	d.saveOrg()
	d.refreshSidebar()
	if len(queued) > 0 {
		d.sendTo(ns, strings.Join(queued, "\n\n"))
	}
}

func isClearSuggestion(text string) bool {
	f := strings.Fields(strings.TrimSpace(text))
	return len(f) > 0 && (f[0] == "/clear" || f[0] == "/compact")
}

func (d *desk) shownSuggestion(s *deskSession) string {
	sug := s.conv.Suggestion
	if isClearSuggestion(sug) && contextPct(s) < d.handoffPolicy().Soft {
		return ""
	}
	return sug
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
	if old.running() {
		p := old.proc
		old.stopping = true
		go func() { _ = p.Stop(5 * time.Second) }()
	}
	ns := d.replaceWith(old, title)
	ns.restarting = true
	d.putPlace(id, placeArchive)
	d.saveOrg()
	d.compactInto(ns, id, path, title, conv)
}

func (d *desk) compactInto(ns *deskSession, oldID, path, title string, conv *chat.Conversation) {
	ns.compacting, ns.compactFrom = true, oldID
	d.notify(ns, fmt.Sprintf("Compacting “%s” for the hand-off. Messages you send now are queued until it's done.", title))
	compact := d.compactor
	if compact == nil {
		compact = d.claudeCompact
	}
	src, model := compactSource(conv), ns.model
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), compactTimeout)
		brief, err := compact(ctx, model, src)
		cancel()
		d.do(func() {
			ns.compacting, ns.compactFrom = false, ""
			if err != nil {
				brief = ""
				d.notify(ns, "Compacting failed ("+clip(err.Error(), 200)+"); handing over the trimmed conversation instead.")
			}
			d.sendTo(ns, handoffPrompt(oldID, path, title, conv, brief))
		})
	}()
}

func (d *desk) replaceWith(old *deskSession, title string) *deskSession {
	id := old.id
	if isHiddenSession(old) {
		return d.replaceHidden(old, title)
	}
	place := d.placement(id)
	ns := d.addSession(old.cwd, old.model, old.mode, false)
	d.org.Titles[ns.id] = title
	if strings.HasPrefix(place, folderPrefix) || place == placeAuto {
		d.putPlace(ns.id, place)
	}
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
		if len(tabs) > maxTabs {
			tabs = tabs[len(tabs)-maxTabs:]
		}
	}
	d.tabs = tabs
	d.saveTabs()
	d.saveOrg()
	switch p := d.paneFor(old); {
	case p != nil && d.split():
		d.showInPane(p, ns.id)
		if d.focused == p {
			d.open(ns.id)
		}
	case d.current == old:
		d.open(ns.id)
	default:
		d.syncTabs()
		d.refreshSidebar()
	}
	return ns
}

// replaceHidden: context rotation of the kmem window's assistant. The new
// session stays hidden (no tab, no placement) and takes the old one's place
// in the window (docs/design/kmem-window.md §4 #22).
func (d *desk) replaceHidden(old *deskSession, title string) *deskSession {
	ns := d.addSession(old.cwd, old.model, old.mode, false)
	d.markHiddenAs(ns, hiddenRole(old)) // keeps its role (hidden.go)
	if title != "" {
		d.org.Titles[ns.id] = title
	}
	d.saveOrg()
	if w := d.kmemWin; w != nil && w.s == old {
		w.s = ns
		if w.view != nil {
			w.view.show(ns)
		}
	}
	return ns
}
