package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
)

// The Quill operator (docs/design/quill-operator.md §3.1, §3.9, §5.1; phase 2
// sub E): the coordinator of topic `quill` (kmem name `quill`), run as a
// hidden topic session behind the Quill button. It is the second assistant
// role (assist.go): it starts with Quill Desktop, starts fresh from the topic
// briefing (no handoff), stops, rotates and quits through the end-of-session
// check, and holds the one live session of topic `quill`. Settings work is
// for every user; developer work only with a "Quill source folder" (Settings →
// General → Quill operator), which is then its working folder. The box UI
// (assistbox.go) wires the role's UI hooks (reason, attach, refresh, dot, ask,
// stopped), its bubble (quillOpBubble) and its host (setHiddenHost(roleQuill)).

const (
	quillOpName      = "quill"             // the topic and kmem name
	prefQuillSource  = "quillSourceFolder" // the Quill source folder ("" = unset)
	prefQuillOpModel = "quillOpModel"      // the operator's model; kmemAssistOff turns it off
	prefQuillOpSeen  = "quillOpBadgeSeen"  // the Quill badge's seen records
	quillModule      = "module github.com/katachinosekai/quill"
)

// quillOpBubble: the Quill button's bubble, set by the box UI (nil: no badge).
var quillOpBubble = func(d *desk) *bubble { return nil }

var quillRole *assistRole

func init() { quillRole = newQuillRole() } // not a var initializer: the hooks reach assistFor

func newQuillRole() *assistRole {
	return &assistRole{
		id:          "quill",
		name:        quillOpName,
		label:       "Quill operator",
		hidden:      roleQuill,
		hint:        quillOperatorHint,
		dir:         quillOpHomeDir,
		dirFor:      func(d *desk) string { return d.quillOpDir() },
		startNote:   quillOpStartNote,
		stopMessage: func() string { return "" }, // stop replaces the handoff save
		bootOK:      func(d *desk) bool { return d.kmemMemory() },
		available:   func(d *desk) bool { return d.kmemAvailable() },
		model:       func(d *desk) string { return d.quillOpModel() },
		unavailable: "kmem is not available, so the Quill operator can't run.",
		offReason:   "The Quill operator is off.",
		items:       func(d *desk) []badgeItem { return quillIssueItems(d.kmemNeedsNow.QuillIssues) },
		ready:       func(d *desk) bool { return d.kmemGot },
		seenPref:    prefQuillOpSeen,
		bubble:      func(d *desk) *bubble { return quillOpBubble(d) },
		prompt:      func(d *desk) string { return d.topicPrompt(quillTopic(), false) },
		stop:        quillOpStop,
		elsewhere:   quillOpElsewhere,
		blocked:     quillOpBlocked,
	}
}

// quillAssist: the desk's Quill operator.
func (d *desk) quillAssist() *assistant { return d.assistFor(quillRole) }

func quillTopic() topicInfo {
	if t, ok := cachedTopic(quillOpName); ok {
		return t
	}
	return topicInfo{Slug: quillOpName, Title: topicTitle(quillOpName), Scope: []string{quillOpName}}
}

// quillOperatorHint: the settings role, for every user (§3.1, Q3, Q5-Q7).
const quillOperatorHint = "You are the Quill operator, Quill Desktop's coordinator for Quill Desktop and Quill Android; use kmem --as quill. " +
	"You run hidden behind the Quill button; the user talks to you in its box. " +
	"Settings: change Quill Desktop settings only when the user asks in this chat, only with `quill settings` " +
	"(list/get are free; set and undo need the approved plan); never edit preference files. " +
	"Security settings are the user's: say where they are. " +
	"Other sessions report problems as `quill issue:` tasks (topic quill); list and plan them at start."

// quillDeveloperHint: the developer role, only with a Quill source folder.
const quillDeveloperHint = "Developer: improve Quill Desktop and Quill Android in the Quill source folder %s " +
	"(your working folder) through subagents in worktrees, with plans, reviews and tests, as before."

// quillNoSourceHint: no source folder: settings and explanations only.
const quillNoSourceHint = "No Quill source folder is set, so you don't change Quill's code: settings and explanations only " +
	"(the folder is set in Settings → General → Quill operator)."

// quillOpStartNote follows the topic prompt on every start.
const quillOpStartNote = "You run hidden behind the Quill button as the Quill operator; see the operator hint."

// quillOpMovedLine: the one line the moved tab session gets (§5.1 step 4).
const quillOpMovedLine = "You now run hidden behind the Quill button as the Quill operator; see the operator hint."

// quillOperatorHintFor: the operator hint for a source folder ("" = none).
func quillOperatorHintFor(src string) string {
	if src == "" {
		return quillOperatorHint + " " + quillNoSourceHint
	}
	return quillOperatorHint + " " + fmt.Sprintf(quillDeveloperHint, src)
}

// quillOperatorPrompt: the operator's session prompt: the topic hints of
// topic quill, the coordinator and build-gate hints only for the developer
// role, the operator hint and, as for every session, plan-first.
func (d *desk) quillOperatorPrompt(s *deskSession) string {
	src := d.quillSource()
	t := topicHints{topic: quillOpName, endCheck: d.kmemFeatureCached(endCheckFeature)}
	p := sessionPromptWith(true, src != "" && d.buildGateOn(), src != "", t) + " " + quillOperatorHintFor(src)
	s.approvalHint = d.planFirst
	if d.planFirst {
		p += " " + planApprovalHint
	}
	return p
}

// quillSource: the Quill source folder, "" when unset or gone.
func (d *desk) quillSource() string {
	p := strings.TrimSpace(d.app.Preferences().String(prefQuillSource))
	if p == "" {
		return ""
	}
	p = expandHome(p)
	if st, err := os.Stat(p); err != nil || !st.IsDir() {
		return ""
	}
	return filepath.Clean(p)
}

// quillOpHomeDir: the operator's folder without a source folder.
func quillOpHomeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "quill", "quill-operator")
}

// quillOpDir: the working folder: the source folder, else its own folder.
func (d *desk) quillOpDir() string {
	if src := d.quillSource(); src != "" {
		return src
	}
	return quillOpHomeDir()
}

// quillOpModel: the operator's model, or kmemAssistOff.
func (d *desk) quillOpModel() string {
	p := d.app.Preferences()
	if v := p.String(prefQuillOpModel); v != "" {
		return v
	}
	if m := p.StringWithFallback("newModel", "default"); !isLocal(m) {
		return m
	}
	return "default"
}

// quillOpOn: topic quill belongs to the operator (it can run here).
func (d *desk) quillOpOn() bool {
	return quillRole.bootOK(d) && quillRole.available(d) && d.quillOpModel() != assistOff
}

// quillIssue: a report from a session for the Quill operator (Q17).
func quillIssue(t kmemTask) bool {
	return t.Topic == quillOpName && strings.HasPrefix(strings.ToLower(strings.TrimSpace(t.Title)), "quill issue:")
}

// quillIssueItems: the open `quill issue:` tasks on the Quill badge.
func quillIssueItems(ts []kmemTask) []badgeItem {
	var items []badgeItem
	for _, t := range ts {
		if t.Status == "open" {
			items = append(items, badgeItem{Key: "issue:" + t.ID, Sig: t.Title})
		}
	}
	return items
}

// assistantsBoot starts every assistant with Quill Desktop (desk.go watch).
func (d *desk) assistantsBoot() {
	d.kmemAssistBoot()
	d.assistBoot(d.quillAssist())
}

// assistantsExited: a hidden session's process ended (handleExit).
func (d *desk) assistantsExited(s *deskSession) {
	for _, a := range d.assistants() {
		if a.s == s {
			d.assistDot(a)
			d.assistExited(a, s)
		}
	}
}

// assistantsQuit: every assistant stops for Quit (the kmem assistant saves
// its handoff, the operator stops: its check ran with the topic tabs); done
// once all are stopped.
func (d *desk) assistantsQuit(done func()) {
	flows := []func(func()){}
	if d.kmemQuitFlow != nil {
		flows = append(flows, d.kmemQuitFlow)
	}
	qa := d.quillAssist()
	flows = append(flows, func(f func()) {
		if qa.state == asSaving && qa.s != nil { // Stop's check still runs: Quit wins
			qa.s.endWait = nil
			qa.quit = true
			qa.done = append(qa.done, f)
			d.assistStopWith(qa, "Quill Desktop quit; the Quill operator stopped.")
			return
		}
		d.assistSaveStop(qa, true, f)
	})
	left := len(flows)
	for _, fl := range flows {
		fl(func() {
			if left--; left == 0 {
				done()
			}
		})
	}
}

// quillOpStop: Stop / Quit / Restart for the operator: the end-of-session
// check instead of a handoff save (§3.1). Quit stops at once: its check ran
// in parallel with the topic tabs (quitCheckSessions).
func quillOpStop(d *desk, a *assistant, s *deskSession) {
	if a.quit {
		d.assistStopWith(a, "Quill Desktop quit; the Quill operator stopped.")
		return
	}
	if !d.endCheckFor(s) || s.endWait != nil {
		d.assistStopWith(a, "The Quill operator stopped (this kmem has no end-of-session check).")
		return
	}
	ew := &endWait{why: endForStop, name: quillOpName}
	ew.done = func(ok bool, why string) {
		if a.s != s || a.state != asSaving {
			return
		}
		if ok {
			d.assistStopWith(a, "End-of-session check passed; the Quill operator is stopped (nothing deleted).")
			return
		}
		msg := "The end-of-session check did not pass"
		if why != "" {
			msg += " (" + why + ")"
		} else if bs := blockerList(ew.blockers, "; "); bs != "" {
			msg += ": " + bs
		}
		d.notify(s, msg+"; nothing was stopped. Stop it again once it can pass.")
		d.assistKeep(a)
	}
	d.notify(s, "Stopping: running the end-of-session check; the Quill operator is stopped once it passes.")
	d.startEndCheck(s, ew)
	d.afterUI(kmemEndTimeout, func() {
		if s.endWait != ew {
			return
		}
		s.endWait = nil
		ew.finish(false, fmt.Sprintf("not within %d minutes", int(kmemEndTimeout/time.Minute)))
	})
}

// quillOpElsewhere: topic quill is live in a tab (§5.1): the box offers to
// move that running session in, as it is.
func quillOpElsewhere(d *desk, a *assistant, o *deskSession) {
	title, _, _ := d.rowText(o.id)
	d.assistReasonWith(a, fmt.Sprintf("The Quill operator is open in the tab “%s”.", title),
		"Move it here", func() { d.quillMoveIn(a, o) })
}

// quillMoveIn turns the tab session o into the hidden operator without
// restarting it (Q1): same process, same session id, its items, queue,
// subagents and plan state come along. A tab that is not running closes and
// the operator starts fresh from the briefing.
func (d *desk) quillMoveIn(a *assistant, o *deskSession) {
	if a.state != asElsewhere {
		return
	}
	if d.assistOwner(a) != o || isHiddenSession(o) {
		d.assistProbe(a) // the world changed
		return
	}
	title, _, _ := d.rowText(o.id)
	if !o.running() || o.stopping {
		d.autoOffForEnd(o.id)
		d.closeTab(o.id)
		d.assistProbe(a)
		return
	}
	if len(o.conv.PendingPermissions()) > 0 { // a safe moment: no permission prompt open
		d.assistReasonWith(a, fmt.Sprintf("The tab “%s” waits for an answer to a permission prompt; answer it there, then move the Quill operator here.", title),
			"Move it here", func() { d.quillMoveIn(a, o) })
		return
	}
	d.quillAdopt(a, o, fmt.Sprintf("Moved from the tab “%s”; it keeps running here.", title))
	d.sendTo(o, quillOpMovedLine) // queued if it is mid-turn
}

// quillAdopt makes the running session o the hidden operator: its tab's
// auto-approve ends (§3.9), the tab closes without stopping the process.
func (d *desk) quillAdopt(a *assistant, o *deskSession, note string) {
	d.autoOffForEnd(o.id)
	wasCurrent := d.current == o
	if p := d.paneFor(o); p != nil && len(d.panes) > 1 {
		p.view.show(nil)
	}
	d.markHiddenAs(o, roleQuill) // out of the tabs, process untouched
	d.org.Kmem[o.id] = quillOpName
	d.saveOrg()
	if wasCurrent {
		if len(d.tabs) > 0 {
			d.open(d.tabs[len(d.tabs)-1])
		} else {
			d.current = nil
			d.chat.show(nil)
		}
	}
	a.run++ // a probe in flight starts nothing
	a.userStopped = false
	a.s, a.state = o, asLive
	d.assistAttach(a)
	d.notify(o, note)
	d.assistFlush(a, o)
	d.assistRefresh(a)
	d.assistDot(a)
	d.refreshSidebar()
}

// quillOpBlocked: topic quill is live outside Quill Desktop (K6): the box
// shows "running elsewhere" with Take over, as the topic card does.
func quillOpBlocked(d *desk, a *assistant) bool {
	e, ok := d.topicExternal(quillOpName)
	if !ok {
		return false
	}
	a.state = asElsewhere
	d.assistReasonWith(a, "The Quill operator is running elsewhere (kmem name "+e.Name+").", "Take over",
		func() { d.offerTakeOver(e, nil) })
	return true
}

// openQuillOperator brings up the operator's box and starts it if it is not
// running (topic quill from the card, the phone or --resume-all).
func (d *desk) openQuillOperator() {
	a := d.quillAssist()
	switch a.state {
	case asClosed, asPanels:
		a.userStopped = false
		d.assistProbe(a)
	}
	if h := d.hostFor(roleQuill); h.open != nil {
		h.open()
	}
}

// isQuillRepo: dir holds the Quill source (its go.mod names the module).
func isQuillRepo(dir string) bool {
	f, err := os.Open(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	return sc.Scan() && strings.TrimSpace(sc.Text()) == quillModule
}

// quillRepoAbove: the Quill source folder holding dir, "" if none.
func quillRepoAbove(dir string) string {
	for dir != "" {
		if isQuillRepo(dir) {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			return ""
		}
		dir = up
	}
	return ""
}

// suggestQuillSource: a Quill source folder to suggest (never set on its
// own): the folder of a session of topic quill, else of this binary.
func (d *desk) suggestQuillSource() string {
	var dirs []string
	for id, k := range d.org.Kmem {
		if k != quillOpName {
			continue
		}
		if s := d.sessions[id]; s != nil {
			dirs = append(dirs, s.cwd)
		}
		if info, ok := d.infos[id]; ok {
			dirs = append(dirs, info.Cwd)
		}
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	for _, dir := range dirs {
		if r := quillRepoAbove(dir); r != "" {
			return r
		}
	}
	return ""
}

// quillOpSettings: Settings → General → Quill operator.
func (d *desk) quillOpSettings() fyne.CanvasObject {
	prefs := d.app.Preferences()
	e := widget.NewEntry()
	e.SetPlaceHolder("Not set")
	e.SetText(prefs.String(prefQuillSource))
	e.OnChanged = func(v string) { prefs.SetString(prefQuillSource, strings.TrimSpace(v)) }
	browse := widget.NewButton("Choose...", func() {
		dlg := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
			if err == nil && u != nil {
				e.SetText(u.Path())
			}
		}, d.win)
		if p := d.quillSource(); p != "" {
			if l, err := storage.ListerForURI(storage.NewFileURI(p)); err == nil {
				dlg.SetLocation(l)
			}
		}
		dlg.Show()
	})
	clear := widget.NewButton("Clear", func() { e.SetText("") })
	box := container.NewVBox(boldLabel("Quill source folder"),
		container.NewBorder(nil, nil, nil, container.NewHBox(browse, clear), e))
	if sug := d.suggestQuillSource(); sug != "" {
		use := widget.NewButton("Use", func() { e.SetText(sug) })
		box.Add(container.NewBorder(nil, nil, nil, use, caption("Suggested: "+sug)))
	}
	box.Add(caption("The Quill operator changes Quill Desktop settings for everyone. With a Quill source folder it also works on Quill Desktop and Quill Android there (its working folder). Applies when the operator starts again."))
	return box
}
