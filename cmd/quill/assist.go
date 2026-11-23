package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// Role-generic assistant lifecycle and badge (docs/design/quill-operator.md §7,
// Phase 1 sub B). An assistant is an always-running hidden Claude session that
// holds one kmem name (and so one kmem handoff), started with Quill Desktop and
// stopped with a save. Everything that differs per assistant is an assistRole:
// its kmem name, label, working folder, model setting, start and stop texts,
// the badge's item sources and the UI hooks (reason line, chat, header refresh,
// run dot, save-timeout question). kmemassist.go defines the kmem role and its
// window UI; the Quill operator (phase 2) adds a second role by configuration.
// Every assistant has its own state machine, session, queue and badge; the
// one-live-session guard (kmemsingle.go) is per kmem name, so roles never
// share one.

// assistOff is the model setting value that turns an assistant off.
const assistOff = kmemAssistOff

// assistRole is the configuration of one assistant.
type assistRole struct {
	id     string // "kmem", "quill": the key of its state
	name   string // its kmem name (kmem --as NAME, handoff, peer name)
	label  string // "kmem assistant": in notes and hints
	hidden string // its hidden-session role (hidden.go: roleKmem, roleQuill); its host is set with setHiddenHost
	hint   string // the session prompt hint (memorymode.go)
	dir    func() string

	startedPref string // preference: when the last session started (the "ended without saving" note)
	startNote   string // follows the resume prompt on every start
	stopMessage func() string

	bootOK      func(d *desk) bool   // may it start with Quill Desktop (kmem memory on, ...)
	available   func(d *desk) bool   // can it run at all (kmem installed, ...)
	model       func(d *desk) string // the model setting; assistOff turns it off
	unavailable string               // reason line when !available
	offReason   string               // reason line when the model setting is Off

	// The badge: item sources besides the pending question or plan, the seen
	// records' preference, and whether the sources have answered once (records
	// of gone items are pruned only then).
	items    func(d *desk) []badgeItem
	ready    func(d *desk) bool
	badgeOn  func(d *desk) bool
	seenPref string
	bubble   func(d *desk) *bubble

	// UI hooks (UI thread); nil hooks do nothing.
	reason  func(d *desk, a *assistant, reason, button string, tap func())
	attach  func(d *desk, a *assistant) // the session is there: show its chat
	refresh func(d *desk, a *assistant) // header and badge
	dot     func(d *desk, running bool) // the button's run dot
	ask     func(d *desk, a *assistant) // the save timed out: ask the user
	stopped func(d *desk, a *assistant) // after Stop assistant: "Start assistant"
}

type assistant struct {
	role        *assistRole
	state       assistState
	w           *kmemWindow  // the open window, nil while closed (hidden); the kmem role's UI
	s           *deskSession // the assistant session (the last one, once stopped)
	gen         int          // bumped on every window open and close: stale ticks drop out
	run         int          // bumped on every probe and stop: stale lifecycle callbacks drop out
	tickGen     int
	reason      string
	button      string    // the label of the button after the reason line ("" if none)
	userStopped bool      // the user pressed Stop assistant: opening the window doesn't start it
	untilSaved  bool      // the user chose to keep it running until it saves
	rebootNext  bool      // Restart (new model setting): start again once stopped
	saveBy      time.Time // the save deadline while saving (the header's countdown)
	quit        bool      // stopping for Quit: no question on timeout
	done        []func()
	choice      dialog.Dialog
	lastSave    time.Time
	pending     []string // messages waiting for the assistant to run

	// The kmem role's chat side, built once and shown again on every reopen.
	chat     fyne.CanvasObject
	view     *chatView
	input    *sendEntry
	stateLbl *widget.Label
	modelLbl *widget.Label
	note     *widget.Label
	restart  *widget.Button
	reboot   *widget.Button // Restart on the changed model setting
	stopBtn  *widget.Button
}

// kmemAssist is the kmem role's assistant under its old name.
type kmemAssist = assistant

type assistKey struct {
	d    *desk
	role string
}

var (
	kmemAssistsMu sync.Mutex
	kmemAssists   = map[*desk]*kmemAssist{} // the kmem role's assistant per desk
	otherAssists  = map[assistKey]*assistant{}
)

// assistFor: the desk's assistant for role (created on first use).
func (d *desk) assistFor(role *assistRole) *assistant {
	kmemAssistsMu.Lock()
	defer kmemAssistsMu.Unlock()
	if role == kmemRole {
		a := kmemAssists[d]
		if a == nil {
			a = &assistant{role: role}
			kmemAssists[d] = a
		}
		return a
	}
	k := assistKey{d, role.id}
	a := otherAssists[k]
	if a == nil {
		a = &assistant{role: role}
		otherAssists[k] = a
	}
	return a
}

// assist is the kmem role's assistant.
func (d *desk) assist() *kmemAssist { return d.assistFor(kmemRole) }

// assistants: every assistant created on this desk.
func (d *desk) assistants() []*assistant {
	kmemAssistsMu.Lock()
	defer kmemAssistsMu.Unlock()
	var out []*assistant
	if a := kmemAssists[d]; a != nil {
		out = append(out, a)
	}
	for k, a := range otherAssists {
		if k.d == d {
			out = append(out, a)
		}
	}
	return out
}

// assistOf: the assistant whose session is s (nil if none).
func (d *desk) assistOf(s *deskSession) *assistant {
	if s == nil {
		return nil
	}
	for _, a := range d.assistants() {
		if a.s == s {
			return a
		}
	}
	return nil
}

// assistIs: s is a's session.
func (d *desk) assistIs(a *assistant, s *deskSession) bool {
	return s != nil && a.s == s
}

// The UI hooks, dispatched by role.

func (d *desk) assistReason(a *assistant, reason string) {
	a.reason, a.button = reason, ""
	if a.role.reason != nil {
		a.role.reason(d, a, reason, "", nil)
	}
}

func (d *desk) assistReasonWith(a *assistant, reason, button string, tap func()) {
	a.reason, a.button = reason, button
	if a.role.reason != nil {
		a.role.reason(d, a, reason, button, tap)
	}
}

func (d *desk) assistAttach(a *assistant) {
	if a.s != nil {
		a.reason, a.button = "", "" // phones read it too
	}
	if a.role.attach != nil {
		a.role.attach(d, a)
	}
}

func (d *desk) assistRefresh(a *assistant) {
	d.updateAssistBadge(a) // a question or plan may have come or been answered
	if a.role.refresh != nil {
		a.role.refresh(d, a)
	}
}

func (d *desk) assistAsk(a *assistant) {
	if a.role.ask != nil {
		a.role.ask(d, a)
	}
}

func (d *desk) assistStoppedReason(a *assistant) {
	if a.role.stopped != nil {
		a.role.stopped(d, a)
	}
}

// assistDeliver hands text (the Monday check-in, say) to the assistant: a
// message to the running one (queued if it is mid-turn, as user messages are);
// a new assistant starts only when none runs, and gets the message after its
// resume prompt. Returns what happened, for the log.
func (d *desk) assistDeliver(a *assistant, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "empty"
	}
	if s := a.s; s != nil && s.running() && !s.stopping && (a.state == asLive || a.state == asStarting) {
		d.sendTo(s, text)
		d.assistRefresh(a)
		return "sent to the running assistant"
	}
	if o := d.assistOwner(a); o != nil && o.running() && !o.stopping {
		d.sendTo(o, text) // live in a tab: it is the one session under this name
		return "sent to the live tab"
	}
	a.pending = append(a.pending, text)
	switch a.state {
	case asProbing, asSaving, asStopping, asStarting:
		return "queued until the assistant is up" // flushed by the start (or restarted after the stop)
	}
	a.userStopped = false
	d.assistProbe(a)
	return "starting the assistant"
}

// assistFlush sends the queued messages to the running assistant.
func (d *desk) assistFlush(a *assistant, s *deskSession) {
	p := a.pending
	a.pending = nil
	for _, t := range p {
		d.sendTo(s, t)
	}
}

// assistWaitStopped: a previous hidden session still stops (rotation,
// Restart on a new model): start nothing until its process is gone.
func (d *desk) assistWaitStopped(a *assistant, o *deskSession) {
	d.assistReason(a, "Waiting for the previous "+a.role.label+" to stop...")
	run := a.run
	var poll func(n int)
	poll = func(n int) {
		d.afterUI(100*time.Millisecond, func() {
			if a.run != run || a.state != asProbing {
				return
			}
			if o.running() && n < 100 {
				poll(n + 1)
				return
			}
			d.assistProbe(a)
		})
	}
	poll(0)
}

// The badge (user 2026-11-23, kmem task t491363; rules in kmemseen.go): the
// items that need the user, each with a seen record (signature and time, in
// the role's preference). An item is unread when it has no record or its
// signature changed since it was seen; the assistant's question or plan is
// sticky and badges until answered.

// assistWait: the assistant waits for the user: a plan to approve or a
// question (a permission or a reply to its question), with a signature that
// changes with each new one. UI thread.
func (d *desk) assistWait(a *assistant) (kind, sig string) {
	s := a.s
	if s == nil || s.conv == nil || !s.running() {
		return "", ""
	}
	c := s.conv
	for _, it := range c.PendingPermissions() {
		if it.Kind == chat.Plan {
			return "plan", fmt.Sprint(it.Seq, it.RequestID)
		}
	}
	if p := c.PendingPermissions(); len(p) > 0 {
		return "question", fmt.Sprint(p[0].Seq, p[0].RequestID)
	}
	if !c.Busy && (c.AsksApproval() || c.AwaitsReply()) {
		for i := len(c.Items) - 1; i >= 0; i-- {
			if it := c.Items[i]; it.Kind == chat.Assistant && strings.TrimSpace(it.Text) != "" {
				return "question", fmt.Sprint(it.Seq, it.UUID)
			}
		}
		return "question", "text"
	}
	return "", ""
}

// assistNeeds: everything that needs the user from this assistant: its
// pending question or plan first, then the role's item sources.
func (d *desk) assistNeeds(a *assistant) []badgeItem {
	var items []badgeItem
	if k, sig := d.assistWait(a); k != "" {
		items = append(items, badgeItem{Key: "assist:" + k, Sig: sig, Sticky: true})
	}
	if a.role.items != nil {
		items = append(items, a.role.items(d)...)
	}
	return items
}

func (d *desk) loadSeen(pref string) map[string]seenRec {
	m := map[string]seenRec{}
	if raw := d.app.Preferences().String(pref); raw != "" {
		if json.Unmarshal([]byte(raw), &m) != nil || m == nil {
			m = map[string]seenRec{}
		}
	}
	return m
}

func (d *desk) saveSeen(pref string, m map[string]seenRec) {
	b, _ := json.Marshal(m)
	d.app.Preferences().SetString(pref, string(b))
}

// updateAssistBadge recomputes the role's bubble from its needs, the
// assistant and the seen records (UI thread). Records of items that are gone
// are dropped, so an item that comes back is new again; only once the role's
// sources have answered.
func (d *desk) updateAssistBadge(a *assistant) {
	if a.role.bubble == nil {
		return
	}
	b := a.role.bubble(d)
	if b == nil {
		return
	}
	items := d.assistNeeds(a)
	seen := d.loadSeen(a.role.seenPref)
	if a.role.ready == nil || a.role.ready(d) {
		live := map[string]bool{}
		for _, it := range items {
			live[it.Key] = true
		}
		drop := false
		for k := range seen {
			if !live[k] {
				delete(seen, k)
				drop = true
			}
		}
		if drop {
			d.saveSeen(a.role.seenPref, seen)
		}
	}
	if a.role.badgeOn != nil && !a.role.badgeOn(d) {
		b.set(0)
		return
	}
	b.set(len(unreadBadgeItems(items, seen)))
}

// markAssistSeen records that the items were shown (UI thread). all (Mark all
// read) marks every shown item; a pending question still badges until
// answered (it is sticky).
func (d *desk) markAssistSeen(a *assistant, all bool) {
	items := d.assistNeeds(a)
	seen := d.loadSeen(a.role.seenPref)
	now := kmemNow()
	changed := false
	for _, it := range items {
		old, ok := seen[it.Key]
		if ok && old.Sig == it.Sig {
			continue // already recorded: keep its first seen time
		}
		seen[it.Key] = seenRec{Sig: it.Sig, At: now}
		changed = true
	}
	if changed {
		d.saveSeen(a.role.seenPref, seen)
	}
	d.updateAssistBadge(a)
}

// assistUnread: how many items would badge now.
func (d *desk) assistUnread(a *assistant) int {
	return len(unreadBadgeItems(d.assistNeeds(a), d.loadSeen(a.role.seenPref)))
}

type assistState int

const (
	asClosed    assistState = iota
	asProbing               // checking kmem, the model and who holds the handoff
	asPanels                // no assistant: the chat side shows one reason line
	asElsewhere             // kmem-lock-audit is live in a tab
	asStarting              // the session is being started
	asLive                  // running (or FAILED when its process is gone)
	asSaving                // Stop assistant or Quit: waiting for the handoff save
	asStopping              // stopping the process
)

func (st assistState) String() string {
	return [...]string{"closed", "probing", "panels only", "elsewhere", "starting", "live", "saving", "stopping"}[st]
}

// assistBoot starts the assistant with Quill Desktop (after a hot
// reload's restore, so a handed-over session is adopted, never doubled). It
// needs kmem memory on; the probe checks the model setting, sign-in and
// whether kmem-lock-audit is live in a tab (then the window offers to move it
// here and nothing starts).
func (d *desk) assistBoot(a *assistant) {
	if a.state != asClosed || a.userStopped || !a.role.bootOK(d) {
		return
	}
	d.assistProbe(a)
}

// assistProbe is PROBING: kmem, the model setting, sign-in, the local
// model's tool use and who holds the handoff decide between the chat and a
// reason line.
func (d *desk) assistProbe(a *assistant) {
	a.state = asProbing
	a.reason, a.button = "", ""
	a.run++
	run := a.run
	model := a.role.model(d)
	switch {
	case !a.role.available(d):
		a.state = asPanels
		d.assistReason(a, a.role.unavailable)
		return
	case model == assistOff:
		a.state = asPanels
		d.assistReason(a, a.role.offReason)
		return
	case !isLocal(model) && !kmemAssistSignedIn(d):
		a.state = asPanels
		d.assistReasonWith(a, "Claude Code is not signed in.", "Sign in", d.signIn)
		return
	}
	if o := d.assistOwner(a); o != nil {
		if !isHiddenSession(o) {
			d.assistElsewhere(a, o)
			return
		}
		if o.running() && !o.stopping {
			d.assistAdopt(a, o) // handed over by a hot reload
			return
		}
		if o.running() { // still stopping: never two at once
			d.assistWaitStopped(a, o)
			return
		}
	}
	d.assistReason(a, "Starting "+a.role.name+"...")
	go func() {
		reason := ""
		if isLocal(model) {
			if err := d.checkOllama(model); err != nil {
				reason = "The " + a.role.label + " can't start: " + err.Error() + "."
			} else if !localModelUsesTools(model) {
				reason = "The local model can't use tools; the assistant needs one that can."
			}
		}
		var saved time.Time
		prompt := ""
		if reason == "" {
			for _, h := range d.kmemHandoffs() {
				if h.Name == a.role.name {
					saved = h.Saved
				}
			}
			prompt = d.resumePrompt(a.role.name)
		}
		d.do(func() {
			if a.run != run || a.state != asProbing {
				return
			}
			if reason != "" {
				a.state = asPanels
				d.assistReason(a, reason)
				return
			}
			d.assistStart(a, model, prompt, saved)
		})
	}()
}

// assistOwner: a live session holding the operator's handoff, other than
// the window's own (nil if none).
func (d *desk) assistOwner(a *assistant) *deskSession {
	return d.handoffOwner(a.role.name, &deskSession{})
}

// assistStart is STARTING: a fresh hidden session resumes from the handoff.
func (d *desk) assistStart(a *assistant, model, prompt string, saved time.Time) {
	if o := d.assistOwner(a); o != nil {
		if isHiddenSession(o) && o.running() && !o.stopping {
			d.assistAdopt(a, o)
			return
		}
		if isHiddenSession(o) && o.running() { // still stopping: never two at once
			d.assistWaitStopped(a, o)
			return
		}
		if !isHiddenSession(o) {
			d.assistElsewhere(a, o)
			return
		}
	}
	dir := a.role.dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		a.state = asPanels
		d.assistReason(a, "The "+a.role.label+" can't start: "+err.Error()+".")
		return
	}
	prefs := d.app.Preferences()
	note := assistCrashNote(a.role.name, prefs.String(a.role.startedPref), saved)
	prefs.SetString(a.role.startedPref, time.Now().Format(time.RFC3339))
	s := d.addSession(dir, model, "default", false)
	d.markHiddenAs(s, a.role.hidden)
	s.title = a.role.name
	d.org.Kmem[s.id] = a.role.name
	d.saveOrg()
	a.s, a.state = s, asStarting
	a.lastSave = saved
	d.assistAttach(a)
	if note != "" {
		d.notify(s, note)
	}
	d.sendResume(s, a.role.name, prompt+"\n\n"+a.role.startNote)
	a.state = asLive
	d.assistFlush(a, s)
	d.assistRefresh(a)
	d.assistDot(a)
}

// assistAdopt takes over a running hidden session (a hot reload handed
// it over, with or without the window open).
func (d *desk) assistAdopt(a *assistant, s *deskSession) {
	a.s, a.state = s, asLive
	d.assistAttach(a)
	d.assistFlush(a, s)
	d.assistDot(a)
}

// assistCrashNote: the first notice when the last session ended without
// saving (its handoff is older than its start).
func assistCrashNote(name, started string, saved time.Time) string {
	t, err := time.Parse(time.RFC3339, started)
	if err != nil || !saved.IsZero() && !saved.Before(t) {
		return ""
	}
	if saved.IsZero() {
		return "The last session ended without saving, and " + name + " has no saved handoff yet."
	}
	return "The last session ended without saving; working from the save of " + localDate(saved.Local(), dateMD) + " " + saved.Local().Format("15:04") + "."
}

// assistElsewhere is ELSEWHERE: kmem-lock-audit is live in a tab. The
// window offers to move it in (save there, resume here; user 2026-11-22).
func (d *desk) assistElsewhere(a *assistant, o *deskSession) {
	a.state = asElsewhere
	title, _, _ := d.rowText(o.id)
	d.assistReasonWith(a, fmt.Sprintf("%s is live in the tab “%s”. Move it here: it saves its handoff there and resumes as the background %s.", a.role.name, title, a.role.label),
		"Move it here", func() { d.assistMoveIn(a, o) })
}

func (d *desk) assistMoveIn(a *assistant, o *deskSession) {
	if a.state != asElsewhere {
		return
	}
	title, _, _ := d.rowText(o.id)
	d.endAndSaveTo(o.id, d.endPlace())
	d.assistReason(a, fmt.Sprintf("Waiting for the tab “%s” to save its kmem handoff...", title))
	d.assistWaitOwner(a, a.run, o, title, time.Now().Add(3*kmemWinSaveWait))
}

func (d *desk) assistWaitOwner(a *assistant, run int, o *deskSession, title string, deadline time.Time) {
	d.afterUI(kmemWinSavePoll, func() {
		if a.run != run || a.state != asElsewhere {
			return
		}
		if d.assistOwner(a) == nil {
			d.assistProbe(a)
			return
		}
		if (o.kmemSave == nil && o.running() && !o.stopping) || time.Now().After(deadline) {
			d.assistReasonWith(a, fmt.Sprintf("The tab “%s” did not save its kmem handoff, so %s keeps running there.", title, a.role.name),
				"Try again", func() { d.assistMoveIn(a, o) })
			return
		}
		d.assistWaitOwner(a, run, o, title, deadline)
	})
}

// assistModelChanged: the model setting differs from the running
// assistant's model (the header's Restart).
func (d *desk) assistModelChanged(a *assistant) bool {
	s := a.s
	return s != nil && s.running() && !s.stopping && (a.state == asLive || a.state == asStarting) && a.role.model(d) != s.model
}

// assistReboot (Restart after a model setting change) runs the
// save-and-stop flow, then starts the assistant again on the new setting.
func (d *desk) assistReboot(a *assistant) {
	if !d.assistModelChanged(a) {
		return
	}
	a.rebootNext = true
	d.assistSaveStop(a, false, nil)
}

// assistExited: the assistant's process ended (handleExit). While it was
// saving, the stop flow ends at once (the window shows "Start assistant");
// otherwise the header shows it stopped.
func (d *desk) assistExited(a *assistant, s *deskSession) {
	if a.s != s {
		return
	}
	if a.state == asSaving {
		d.assistStop(a, false)
		return
	}
	d.assistRefresh(a)
}

// assistRestartNow: Restart without the window (Quill Android's
// /phone/kmem/assistant/restart and start); false when the state offers no
// restart.
func (d *desk) assistRestartNow(a *assistant) bool {
	if a.s != nil && a.s.running() || a.state == asSaving || a.state == asStopping || a.state == asProbing {
		return false
	}
	if a.s != nil && (a.state == asLive || a.state == asStarting) {
		d.markEnded(a.s.id) // FAILED: nothing to save
	}
	a.userStopped = false
	d.assistProbe(a)
	return true
}

// assistSaveStop runs the save-and-stop flow (Stop assistant, Quit, Restart on a
// new model): bounded by kmemWinSaveWait; quit asks no question on timeout.
// done is called once the assistant is stopped.
func (d *desk) assistSaveStop(a *assistant, quit bool, done func()) {
	if done != nil {
		a.done = append(a.done, done)
	}
	a.quit = a.quit || quit
	s := a.s
	switch {
	case a.state == asSaving || a.state == asStopping:
		if quit && a.state == asSaving && (a.choice != nil || a.untilSaved) {
			d.assistStop(a, false)
		}
		return
	case s == nil || !s.running() || s.stopping || a.state != asLive && a.state != asStarting:
		if s != nil && !s.running() && (a.state == asLive || a.state == asStarting) {
			d.markEnded(s.id) // FAILED: nothing to save
		}
		a.run++ // a probe in flight starts nothing
		a.state, a.quit = asClosed, false
		d.assistDot(a)
		d.assistFinished(a)
		if a.w != nil && a.userStopped {
			d.assistStoppedReason(a)
		}
		return
	}
	a.state = asSaving
	a.untilSaved = false
	a.saveBy = time.Now().Add(kmemWinSaveWait)
	asked := time.Now().Add(-2 * time.Second)
	// prompt set: kmemAfterTurn leaves this save to the close flow below;
	// kmemSaveAllowed still lets `kmem handoff save` through plan-first.
	ks := &kmemSave{name: a.role.name, asked: asked, end: true, prompt: a.role.label + " stop"}
	s.kmemSave = ks
	d.chatFor(s, s.conv.CancelPlans())
	d.flashHint(d.folder, a.role.label+": saving handoff...")
	msg := a.role.stopMessage()
	d.assistRefresh(a)
	if p := s.proc; s.inTurn || s.conv.Busy || p.Busy() {
		// Mid-turn: stop at once, then ask for the save (user 2026-11-22).
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = p.Interrupt(ctx)
			d.do(func() {
				if s.kmemSave == ks {
					d.sendTo(s, msg)
				}
			})
		}()
	} else {
		d.sendTo(s, msg)
	}
	go d.assistWaitSave(a, s, ks, a.saveBy, kmemWinSavePoll)
}

// assistSavedSince: the same save-time check as kmemAfterTurn (kmem.go): saved
// at or after asked, or a recent save that names this session.
func (d *desk) assistSavedSince(name, id string, asked time.Time) (bool, time.Time) {
	var last time.Time
	saved, recent := false, false
	for _, h := range d.kmemHandoffs() {
		if h.Name != name {
			continue
		}
		last = h.Saved
		if !h.Saved.Before(asked.Truncate(time.Second)) {
			saved = true
		} else if asked.Sub(h.Saved) <= kmemRecentSave {
			recent = true
		}
	}
	if !saved && recent {
		saved = strings.Contains(d.kmemHandoffText(name), "session cs:"+id+")")
	}
	return saved, last
}

func (d *desk) assistWaitSave(a *assistant, s *deskSession, ks *kmemSave, deadline time.Time, poll time.Duration) {
	for {
		saved, last := d.assistSavedSince(ks.name, s.id, ks.asked)
		stop, ran := false, false
		// doWait, not do: fyne.Do returns before f runs, so ran was still
		// false and the loop ended after one poll (the window stayed at
		// "saving its handoff" after the process died; t9249e3).
		d.doWait(func() {
			ran = true
			if s.kmemSave != ks || a.s != s || a.state != asSaving {
				stop = true // reopened, or already stopped
				return
			}
			if !last.IsZero() {
				a.lastSave = last
			}
			switch {
			case saved:
				d.assistStop(a, true)
				stop = true
			case !s.running():
				d.assistStop(a, false)
				stop = true
			case !a.untilSaved && a.choice == nil && time.Now().After(deadline):
				if a.quit {
					d.assistStop(a, false)
					stop = true
					return
				}
				d.assistAsk(a)
			}
		})
		if stop || !ran {
			return
		}
		time.Sleep(poll)
	}
}

func (d *desk) assistLastSave(a *assistant) string {
	if a.lastSave.IsZero() {
		return "There is no earlier save."
	}
	return "The last save is from " + localDate(a.lastSave.Local(), dateMD) + " " + a.lastSave.Local().Format("15:04") + "."
}

// assistChoose: 0 stop without saving, 1 keep running until saved, 2 keep
// it running (cancel the stop).
func (d *desk) assistChoose(a *assistant, n int) {
	if a.choice != nil {
		a.choice.Hide()
		a.choice = nil
	}
	if a.state != asSaving {
		return
	}
	switch n {
	case 0:
		d.assistStop(a, false)
	case 1:
		a.untilSaved = true
		d.flashHint(d.folder, a.role.label+": still running until it saves its handoff.")
	case 2:
		d.assistKeep(a)
	}
}

// assistKeep cancels the stop: the session goes on in the background.
func (d *desk) assistKeep(a *assistant) {
	if a.choice != nil {
		a.choice.Hide()
		a.choice = nil
	}
	a.userStopped, a.rebootNext = false, false
	s := a.s
	if s == nil || !s.running() {
		a.state = asClosed
		d.assistProbe(a)
		return
	}
	s.kmemSave = nil
	a.state, a.untilSaved, a.quit = asLive, false, false
	d.notify(s, "Quill Desktop no longer stops "+a.role.name+"; it keeps running in the background.")
	d.assistAttach(a)
	d.assistDot(a)
}

// assistStop is STOPPING: stop the process and mark the session ended
// (the hidden session has no tab and no place; docs §4 #23).
func (d *desk) assistStop(a *assistant, saved bool) {
	s := a.s
	if a.choice != nil {
		a.choice.Hide()
		a.choice = nil
	}
	s.kmemSave = nil
	a.state = asStopping
	a.run++
	d.assistRefresh(a)
	note := "Saved kmem handoff “" + a.role.name + "”."
	if !saved {
		note = fmt.Sprintf(a.role.name+" did not save its handoff within %s; it was stopped. %s", waitText(), d.assistLastSave(a))
		if !s.running() {
			note = a.role.name + " stopped before it saved its handoff. " + d.assistLastSave(a)
		}
	}
	d.notify(s, note)
	d.flashHint(d.folder, note)
	d.markEnded(s.id)
	if !s.running() {
		d.assistStopped(a, s)
		return
	}
	s.stopping = true
	p := s.proc
	go func() {
		_ = p.Stop(5 * time.Second)
		d.do(func() { d.assistStopped(a, s) })
	}()
}

func (d *desk) assistStopped(a *assistant, s *deskSession) {
	if a.s != s || a.state != asStopping {
		return
	}
	quit, reboot := a.quit, a.rebootNext && !a.quit
	a.state, a.untilSaved, a.quit, a.rebootNext = asClosed, false, false, false
	d.assistDot(a)
	d.assistFinished(a)
	if reboot {
		a.userStopped = false
		d.assistProbe(a) // on the new model setting
		return
	}
	if len(a.pending) > 0 && !quit { // a check-in arrived while it stopped
		a.userStopped = false
		d.assistProbe(a)
		return
	}
	if a.w != nil && !quit {
		d.assistStoppedReason(a)
	}
}

// assistRunning: the assistant's process runs (the button's dot).
func (d *desk) assistRunning(a *assistant) bool {
	return a.s != nil && a.s.running() && !a.s.stopping && a.state != asClosed && a.state != asStopping
}

// assistDot shows the role's button dot while the assistant runs.
func (d *desk) assistDot(a *assistant) {
	if a.role.dot != nil {
		a.role.dot(d, d.assistRunning(a))
	}
}

func (d *desk) assistFinished(a *assistant) {
	done := a.done
	a.done = nil
	for _, f := range done {
		f()
	}
}
