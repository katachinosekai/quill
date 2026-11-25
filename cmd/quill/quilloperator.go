package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
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
	quillOpName       = "quill"               // the topic and kmem name
	prefQuillSource   = "quillSourceFolder"   // the Quill source folder ("" = unset)
	prefQuillOpModel  = "quillOpModel"        // the operator's model; kmemAssistOff turns it off
	prefQuillMoveSeen = "quillMoveNoticeSeen" // the one-time move-in notice was shown
	prefQuillOpSeen   = "quillOpBadgeSeen"    // the Quill badge's seen records
	prefQuillClone    = "quillCloneOffered"   // the one-time offer to clone the source was shown
	quillRepoURL      = "https://github.com/katachinosekai/quill"
	quillModule       = "module github.com/katachinosekai/quill"
)

// quillOpBubble: the Quill button's bubble, set by the box UI (nil: no badge).
var quillOpBubble = func(d *desk) *bubble { return d.assistBubble(roleQuill) }

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
		bootOK:      func(d *desk) bool { return true },
		available:   func(d *desk) bool { return true },
		model:       func(d *desk) string { return d.quillOpModel() },
		offReason:   "The Quill operator is off.",
		items:       func(d *desk) []badgeItem { return quillIssueItems(d.kmemNeedsNow.QuillIssues) },
		ready:       func(d *desk) bool { return d.kmemGot },
		mirrored:    func(d *desk) int { return d.waitingMirrors() },
		seenPref:    prefQuillOpSeen,
		bubble:      func(d *desk) *bubble { return quillOpBubble(d) },
		reason:      quillBoxReason,
		attach:      quillBoxAttach,
		refresh:     quillBoxRefresh,
		dot:         func(d *desk, on bool) { d.setAssistRunDot(roleQuill, on) },
		prompt: func(d *desk) string {
			if d.kmemMemory() {
				return d.topicPrompt(quillTopic(), false)
			}
			return "Start as the Quill operator using the current Quill configuration. Wait for the user's request."
		},
		stop:      quillOpStop,
		elsewhere: quillOpElsewhere,
		blocked:   quillOpBlocked,
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
const quillOperatorHint = "You are the Quill operator, Quill Desktop's coordinator for Quill Desktop and Quill Android. " +
	"You run hidden behind the Quill button; the user talks to you in its box. " +
	"Settings: change Quill Desktop settings only when the user asks in this chat, only with `quill settings`: " +
	"`list` and `get ID` are free; `set ID VALUE --why \"<the user's request>\"` and `undo [N]` need the approved plan; " +
	"never edit preference files. " +
	"Security settings are the user's: say where they are."

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
	linked := d.kmemMemory()
	var t topicHints
	if linked {
		t = topicHints{topic: quillOpName, endCheck: d.kmemFeatureCached(endCheckFeature)}
	}
	p := sessionPromptWith(linked, src != "" && d.buildGateOn(), src != "" && d.coordinatorOn(s.model), t) + " " + quillOperatorHintFor(src)
	if linked {
		p += " Use kmem --as quill. Other sessions report problems as `quill issue:` tasks in topic quill; list and plan them at start."
	}
	s.approvalHint = d.planFirst
	if d.planFirst {
		p += " " + planApprovalHint
	}
	return p
}

// quillSource: the Quill source folder: the one set in Settings → General →
// Quill operator, else the default (quillSourceDefault); "" when there is
// none. A set folder that is gone counts as unset.
func (d *desk) quillSource() string {
	if p := d.quillSourceSet(); p != "" {
		return p
	}
	return quillSourceDefault()
}

// quillSourceSet: the folder set in Settings, "" when unset or gone.
func (d *desk) quillSourceSet() string {
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

// quillExe: the running binary (a test hook).
var quillExe = os.Executable

// quillCloneDir: where the Quill source is cloned when no checkout runs
// Quill Desktop (te2211d).
func quillCloneDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "quill", "source")
}

// quillSourceDefault: the source folder when none is set (te2211d): the git
// checkout of Quill holding the running binary (a build from source), else
// quillCloneDir when it holds the Quill source; "" when neither does.
func quillSourceDefault() string {
	if exe, err := quillExe(); err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		if r := quillRepoAbove(filepath.Dir(exe)); r != "" && isGitCheckout(r) {
			return r
		}
	}
	if c := quillCloneDir(); isQuillRepo(c) {
		return c
	}
	return ""
}

// isGitCheckout: dir is a git checkout (.git is a folder, or a file in a worktree).
func isGitCheckout(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// quillCloneOffer: the one-time offer when the operator runs without the
// Quill source (only an offer: nothing is cloned by itself).
func quillCloneOffer() string {
	dir := quillCloneDir()
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, dir); err == nil {
			dir = filepath.Join("~", rel)
		}
	}
	return "No Quill source folder, so the Quill operator only changes settings. For developer work on Quill Desktop and Quill Android, clone the source: `git clone " +
		quillRepoURL + " " + dir + "` (found there by itself), or set a folder in Settings → General → Quill operator. Ask the operator to clone it if you like; it won't on its own."
}

// offerQuillClone shows quillCloneOffer once ever, on the operator's chat,
// when it runs without a source folder.
func (d *desk) offerQuillClone(s *deskSession) {
	prefs := d.app.Preferences()
	if s == nil || d.quillSource() != "" || prefs.Bool(prefQuillClone) {
		return
	}
	prefs.SetBool(prefQuillClone, true)
	d.notify(s, quillCloneOffer())
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

// assistantsBoot reattaches processes from a live reload.
func (d *desk) assistantsBoot() {
	for _, a := range []*assistant{d.assist(), d.quillAssist()} {
		if s := d.assistOwner(a); s != nil && hiddenRole(s) == a.role.hidden && s.running() && !s.stopping {
			d.assistAdopt(a, s)
		}
	}
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
	button := "Move it here" // only a running tab moves in as it is
	if !o.running() || o.stopping {
		button = "Close the tab, start here"
	}
	d.assistReasonWith(a, fmt.Sprintf("The Quill operator is open in the tab “%s”.", title),
		button, func() { d.quillMoveIn(a, o) })
	d.offerQuillMoveNotice(a, o)
}

const quillMoveNoticeText = "The Quill operator now lives behind the Quill button. Move this session there?"

// quillMoveNotice is the one-time notice on the running quill tab (§5.1).
type quillMoveNotice struct {
	box   *fyne.Container
	owner *deskSession // the tab it is offered on; nil: none pending
	shown bool         // displayed once: remembered in prefs
}

func (d *desk) buildQuillMoveBanner() fyne.CanvasObject {
	n := &quillMoveNotice{}
	d.quillMove = n
	lbl := widget.NewLabel(quillMoveNoticeText)
	lbl.Wrapping = fyne.TextWrapWord
	move := widget.NewButton("Move it here", func() { d.quillMoveNoticeMove() })
	move.Importance = widget.HighImportance
	later := widget.NewButton("Later", func() { d.quillMoveNoticeLater() })
	bgr := canvas.NewRectangle(colCard)
	bgr.CornerRadius = 8
	n.box = container.NewStack(bgr, container.NewPadded(container.NewBorder(nil, nil, nil, container.NewHBox(move, later), lbl)))
	n.box.Hide()
	return n.box
}

// offerQuillMoveNotice arms the notice for the running tab o, once ever. It
// shows when that tab is the current one. Never moves anything by itself.
func (d *desk) offerQuillMoveNotice(a *assistant, o *deskSession) {
	n := d.quillMove
	if n == nil || !o.running() || o.stopping || d.app.Preferences().Bool(prefQuillMoveSeen) {
		return
	}
	n.owner = o
	d.syncQuillMoveBanner()
}

// syncQuillMoveBanner shows the notice only on its tab; the first display is
// remembered in prefs so it never comes back.
func (d *desk) syncQuillMoveBanner() {
	n := d.quillMove
	if n == nil || n.box == nil {
		return
	}
	o := n.owner
	if o != nil && (!o.running() || o.stopping || isHiddenSession(o) || !contains(d.tabs, o.id)) {
		n.owner, o = nil, nil
	}
	show := o != nil && d.current == o
	if show && !n.shown {
		n.shown = true
		d.app.Preferences().SetBool(prefQuillMoveSeen, true)
	}
	if show != n.box.Visible() {
		if show {
			n.box.Show()
		} else {
			n.box.Hide()
		}
		if d.headerBox != nil {
			d.headerBox.Refresh()
		}
	}
}

// quillMoveNoticeMove: [Move it here]: the box's move-in (same process).
func (d *desk) quillMoveNoticeMove() {
	n := d.quillMove
	o := n.owner
	n.owner = nil
	if o != nil {
		if a := d.quillAssist(); a.state == asElsewhere {
			d.quillMoveIn(a, o)
			if a.s == o {
				d.openQuillOperator() // the moved session shows where it went
			}
		}
	}
	d.syncQuillMoveBanner()
}

// quillMoveNoticeLater: [Later] dismisses the notice; the box keeps offering.
func (d *desk) quillMoveNoticeLater() {
	d.quillMove.owner = nil
	d.syncQuillMoveBanner()
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
	d.sendOrQueue(o, quillOpMovedLine) // queued if it is mid-turn
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
			// The box the user moved it into stays open (t653485: the tab
			// switch closed the overlay, so the moved session vanished).
			d.keepBox = true
			d.open(d.tabs[len(d.tabs)-1])
			d.keepBox = false
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
	if !d.kmemMemory() {
		return false
	}
	e, ok := d.topicExternal(quillOpName)
	if !ok {
		return false
	}
	a.state = asElsewhere
	d.assistReasonWith(a, "The Quill operator is running elsewhere (kmem name "+e.Name+").", "Take over",
		func() { d.offerTakeOver(e, nil) })
	return true
}

// openQuillOperator brings up the operator's box.
func (d *desk) openQuillOperator() {
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

// quillSourceText: the effective source folder, for Settings.
func (d *desk) quillSourceText() string {
	if p := d.quillSourceSet(); p != "" {
		return "In use: " + p
	}
	if strings.TrimSpace(d.app.Preferences().String(prefQuillSource)) != "" {
		if def := quillSourceDefault(); def != "" {
			return "That folder doesn't exist; in use: the default, " + def
		}
		return "That folder doesn't exist, and there is no default: settings only."
	}
	if def := quillSourceDefault(); def != "" {
		return "In use (default): " + def
	}
	return "None: settings only. The default is the checkout running Quill Desktop, else " + quillCloneDir() + " once the source is cloned there (" + quillRepoURL + ")."
}

// quillOpSettings: Settings → General → Quill operator.
func (d *desk) quillOpSettings() fyne.CanvasObject {
	prefs := d.app.Preferences()
	e := widget.NewEntry()
	def := quillSourceDefault()
	if def != "" {
		e.SetPlaceHolder("Default: " + def)
	} else {
		e.SetPlaceHolder("Not set")
	}
	e.SetText(prefs.String(prefQuillSource))
	eff := caption("")
	eff.Wrapping = fyne.TextWrapWord
	showEff := func() { eff.SetText(d.quillSourceText()) }
	showEff()
	e.OnChanged = func(v string) {
		prefs.SetString(prefQuillSource, strings.TrimSpace(v))
		showEff()
	}
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
		container.NewBorder(nil, nil, nil, container.NewHBox(browse, clear), e), eff)
	if sug := d.suggestQuillSource(); sug != "" && sug != def && sug != d.quillSourceSet() {
		use := widget.NewButton("Use", func() { e.SetText(sug) })
		box.Add(container.NewBorder(nil, nil, nil, use, caption("Suggested: "+sug)))
	}
	box.Add(caption("The Quill operator changes Quill Desktop settings for everyone. With a Quill source folder it also works on Quill Desktop and Quill Android there (its working folder). Applies when the operator starts again."))
	return box
}
