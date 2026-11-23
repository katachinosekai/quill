package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/protocol"
)

// End-of-session check (docs/design/topic-tabs.md §3.8-§3.9, decision
// fabd19d): a topic session no longer saves a handoff when it ends, rotates or
// Quill Desktop quits. Quill runs `kmem --as TOPIC end-check --json` itself;
// blockers (claims without a checkpoint, the shared lock turn, jobs,
// uncommitted files, the status fact) are shown and the session is asked to
// resolve them, then the check runs again after each finished turn. Quill
// never resolves a blocker itself.
//
// Only with kmem's "end-check" feature (`kmem version --json`, kmem 1.10.0)
// and only for topic sessions; everything else keeps the handoff save flows
// in kmem.go and identity.go. A kmem error falls back to those flows (End,
// rotation, Checkpoint now) or lists the topic in Quit's second confirm.

const (
	endCheckFeature = "end-check"
	quitCheckCap    = 60 * time.Second // per topic session, like kmemQuitFlow
)

type endWhy int

const (
	endForEnd        endWhy = iota // End session, Switch topic, closing the tab
	endForRotate                   // rotation (auto, cold cache, Rotate now, /clear, restart)
	endForQuit                     // Quill Desktop quits
	endForCheckpoint               // /export "Checkpoint now": report only, the session goes on
)

// endBlocker is one entry of end-check's "blockers" (K4).
type endBlocker struct {
	Kind  string   `json:"kind"` // claim, lock, job, files, status
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Name  string   `json:"name"`
	PID   int      `json:"pid"`
	What  string   `json:"what"`
	Repo  string   `json:"repo"`
	Paths []string `json:"paths"`
	Need  string   `json:"need"`
}

type endCheckResult struct {
	OK       bool         `json:"ok"`
	Name     string       `json:"name"`
	Session  string       `json:"session"`
	Blockers []endBlocker `json:"blockers"`
	Notes    []string     `json:"notes"`
	API      int          `json:"api"`
}

// endWait replaces kmemSave for topic sessions while the check runs.
type endWait struct {
	why      endWhy
	name     string // the topic (kmem name)
	place    string // End: where the ended session is listed until places go (sub F)
	spare    int    // turns that may finish before the session's reply
	asked    bool   // the session was sent the blockers
	checking bool
	blockers []endBlocker
	done     func(ok bool, err string) // Quit: the outcome, called once
}

// endCheckFor: s ends through the end-of-session check (a local, visible,
// running topic session and a kmem that has the check). UI thread.
func (d *desk) endCheckFor(s *deskSession) bool {
	return s != nil && s.remote == nil && !isHiddenSession(s) && s.running() &&
		d.topicOf(s.id) != "" && d.kmemAvailable() && d.kmemFeatureCached(endCheckFeature)
}

// endCheck runs K4 for kmem name `name` and Claude session sid (pid: its
// claude process, 0 if unknown). err != "" is a kmem error (exit 2, no or
// bad JSON). Not on the UI thread.
func (d *desk) endCheck(name, sid string, pid int) (endCheckResult, string) {
	args := []string{"--as", name, "end-check", "--session", sid}
	if pid > 0 {
		args = append(args, "--pid", strconv.Itoa(pid))
	}
	out, err := d.runKmem("kmem", append(args, "--json")...)
	r, perr := parseEndCheck(out)
	switch {
	case perr != "":
		if err != nil {
			return r, err.Error() + ": " + firstLine(out)
		}
		return r, perr
	case r.OK && err != nil:
		return r, err.Error()
	}
	return r, ""
}

func parseEndCheck(out string) (endCheckResult, string) {
	var r endCheckResult
	raw := strings.TrimSpace(out)
	if json.Unmarshal([]byte(raw), &r) != nil {
		// kmem's notices go to stderr, which runKmem merges: read from the
		// first line that starts the JSON object.
		i := strings.Index(raw, "\n{")
		if i < 0 || json.NewDecoder(strings.NewReader(raw[i+1:])).Decode(&r) != nil {
			return endCheckResult{}, "no JSON from kmem end-check: " + firstLine(out)
		}
	}
	if r.API != kmemAPI {
		return endCheckResult{}, fmt.Sprintf("kmem end-check api %d", r.API)
	}
	if !r.OK && len(r.Blockers) == 0 {
		return endCheckResult{}, "kmem end-check failed without blockers"
	}
	return r, ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 160 {
		s = string(r[:159]) + "…"
	}
	return s
}

// blockerText: one blocker for people and for the session.
func blockerText(b endBlocker) string {
	var what string
	switch b.Kind {
	case "claim":
		what = "claim " + b.ID
		if b.Title != "" {
			what += " “" + b.Title + "”"
		}
	case "lock":
		what = "the shared lock turn " + b.Name
	case "job":
		what = fmt.Sprintf("background job pid %d", b.PID)
		if b.What != "" {
			what += " (" + b.What + ")"
		}
	case "files":
		what = "uncommitted files in " + b.Repo
		if len(b.Paths) > 0 {
			ps := b.Paths
			more := ""
			if len(ps) > 5 {
				ps, more = ps[:5], fmt.Sprintf(" and %d more", len(b.Paths)-5)
			}
			what += " (" + strings.Join(ps, ", ") + more + ")"
		}
	case "status":
		what = "the status fact"
		if b.ID != "" {
			what += " (current " + b.ID + ")"
		}
	default:
		what = b.Kind
	}
	if b.Need != "" {
		what += ": " + b.Need
	}
	return what
}

func blockerList(bs []endBlocker, sep string) string {
	var parts []string
	for _, b := range bs {
		parts = append(parts, blockerText(b))
	}
	return strings.Join(parts, sep)
}

// endCheckMessage: the one message the session gets when the check lists
// blockers.
func endCheckMessage(why endWhy, name string, bs []endBlocker) string {
	var head string
	switch why {
	case endForRotate:
		head = "Quill is rotating this topic to a fresh session that starts from kmem, and this session will then be deleted."
	case endForQuit:
		head = "Quill Desktop is quitting and will stop this session."
	default:
		head = "Quill is ending this session; it is stopped once its end-of-session check passes."
	}
	return fmt.Sprintf("%s `kmem --as %s end-check` lists:\n- %s\n"+
		"Resolve each one: checkpoint or release the claims, note or end the jobs and uncommitted files in `%s/status` (or commit them), "+
		"update `%s/status` (one fact, `--replaces`), and release your shared lock turn after its logs are read. "+
		"Don't start other work. Reply with one line when done; Quill runs the check again.",
		head, name, blockerList(bs, "\n- "), name, name)
}

func checkpointMessage(name string) string {
	return fmt.Sprintf("Checkpoint now: run `kmem task checkpoint` for every task you have claimed and update `%s/status` (one fact, `--replaces`). "+
		"Then reply with one line and wait; Quill runs the end-of-session check as a report and this session goes on afterwards.", name)
}

// endTopic: End for a topic session: the check, then stop, mark ended, close
// the tab.
func (d *desk) endTopic(id, place string) {
	d.autoOffForEnd(id) // at once, not at process exit (autoapprove.go)
	s := d.sessions[id]
	if s == nil || s.endWait != nil {
		return
	}
	ew := &endWait{why: endForEnd, name: d.org.Kmem[id], place: place}
	d.notify(s, "Ending: running the end-of-session check; this session is stopped once it passes.")
	d.startEndCheck(s, ew)
	d.afterUI(kmemEndTimeout, func() {
		if s.endWait != ew {
			return
		}
		s.endWait = nil
		d.notify(s, fmt.Sprintf("The end-of-session check didn't pass within %d minutes, so this session was not ended; nothing was stopped. Close it again once it can pass.", int(kmemEndTimeout/time.Minute)))
		d.refreshSidebar()
	})
	d.refreshSidebar()
}

// rotateTopic: rotation for a topic session: the check, then a fresh session
// with the topic prompt (§3.2); the old session is deleted.
func (d *desk) rotateTopic(id string) {
	s := d.sessions[id]
	if s == nil || s.endWait != nil {
		return
	}
	ew := &endWait{why: endForRotate, name: d.org.Kmem[id]}
	d.notify(s, "Rotating: running the end-of-session check; a fresh session starts once it passes.")
	d.startEndCheck(s, ew)
}

// checkpointNow (/export "Checkpoint now"): ask for checkpoints and the
// status fact, then report the check's result; the session goes on.
func (d *desk) checkpointNow(s *deskSession) {
	if s.endWait != nil {
		return
	}
	ew := &endWait{why: endForCheckpoint, name: d.org.Kmem[s.id], asked: true}
	if s.inTurn {
		ew.spare = 1
	}
	s.endWait = ew
	d.notify(s, "Asked this session to checkpoint its claims and update its status fact; the end-of-session check runs after its reply.")
	d.sendTo(s, checkpointMessage(ew.name))
	if !s.running() {
		s.endWait = nil
	}
}

func (d *desk) startEndCheck(s *deskSession, ew *endWait) {
	s.endWait = ew
	d.runEndCheckFor(s, ew)
}

func (d *desk) runEndCheckFor(s *deskSession, ew *endWait) {
	if ew.checking {
		return
	}
	ew.checking = true
	pid := 0
	if s.proc != nil {
		pid = s.proc.PID()
	}
	name, sid := ew.name, s.id
	go func() {
		r, err := d.endCheck(name, sid, pid)
		d.do(func() {
			ew.checking = false
			if s.endWait != ew {
				return
			}
			d.endCheckDone(s, ew, r, err)
		})
	}()
}

// endAfterTurn: re-run the check after each finished turn while the session
// is resolving blockers (replaces kmemAfterTurn's handoff time test).
func (d *desk) endAfterTurn(s *deskSession) {
	if ew := s.endWait; ew != nil && ew.asked {
		d.runEndCheckFor(s, ew)
	}
}

func (d *desk) endCheckDone(s *deskSession, ew *endWait, r endCheckResult, err string) {
	switch {
	case err != "":
		s.endWait = nil
		d.endFallback(s, ew, err)
	case r.OK:
		s.endWait = nil
		ew.blockers = nil
		d.endPassed(s, ew, r)
	case !ew.asked:
		ew.blockers = r.Blockers
		ew.asked = true
		if s.inTurn {
			ew.spare = 1 // the current turn ends before the session reads the message
		}
		d.notify(s, fmt.Sprintf("End-of-session check: %d blocker(s): %s. Asked this session to resolve them.", len(r.Blockers), blockerList(r.Blockers, "; ")))
		d.sendTo(s, endCheckMessage(ew.why, ew.name, r.Blockers))
		if !s.running() {
			s.endWait = nil
			d.endNotPassed(s, ew, "this session's Claude process could not start")
		}
		d.refreshSidebar()
	default:
		ew.blockers = r.Blockers
		if hasPendingPlan(s) || s.conv.AsksApproval() {
			d.notify(s, "Waiting for you: answer this session's plan or question so it can resolve its end-of-session blockers.")
			return
		}
		if ew.spare > 0 {
			ew.spare--
			return
		}
		s.endWait = nil
		d.endNotPassed(s, ew, "")
	}
}

func (d *desk) endPassed(s *deskSession, ew *endWait, r endCheckResult) {
	switch ew.why {
	case endForEnd:
		d.finishTopicEnd(s, ew.place)
	case endForRotate:
		d.finishTopicRotate(s, ew.name)
	case endForQuit:
		ew.finish(true, "")
	case endForCheckpoint:
		msg := "End-of-session check passed: nothing is left to record."
		if len(r.Notes) > 0 {
			msg += " " + strings.Join(r.Notes, " ")
		}
		d.notify(s, msg)
	}
	d.refreshSidebar()
}

func (d *desk) endNotPassed(s *deskSession, ew *endWait, why string) {
	list := blockerList(ew.blockers, "; ")
	switch ew.why {
	case endForQuit:
		ew.finish(false, why)
		return
	case endForCheckpoint:
		d.notify(s, "End-of-session check still lists: "+list+".")
	default:
		what := "end"
		if ew.why == endForRotate {
			what = "rotation"
		}
		if why != "" {
			d.notify(s, fmt.Sprintf("The %s was cancelled (%s); nothing was stopped.", what, why))
		} else {
			d.notify(s, fmt.Sprintf("The end-of-session check still lists %s, so the %s was cancelled; nothing was stopped. Try again once it can pass.", list, what))
		}
	}
	d.refreshSidebar()
}

// endFallback: kmem could not run the check; End, rotation and Checkpoint now
// use the handoff save flows, Quit lists the topic for the second confirm.
func (d *desk) endFallback(s *deskSession, ew *endWait, err string) {
	if ew.why == endForQuit {
		ew.finish(false, "kmem error: "+err)
		return
	}
	d.notify(s, "kmem could not run the end-of-session check ("+err+"); using the kmem handoff save instead.")
	switch ew.why {
	case endForEnd:
		d.endWithHandoff(s.id, ew.place)
	case endForRotate:
		d.relaunchViaHandoff(s.id, ew.name)
	case endForCheckpoint:
		d.saveHandoffOnly(s)
	}
}

func (ew *endWait) finish(ok bool, err string) {
	if f := ew.done; f != nil {
		ew.done = nil
		f(ok, err)
	}
}

func (d *desk) finishTopicEnd(s *deskSession, place string) {
	d.notify(s, "End-of-session check passed; this session is stopped (nothing deleted).")
	if s.running() {
		p := s.proc
		s.stopping = true
		go func() { _ = p.Stop(5 * time.Second) }()
	}
	d.putPlace(s.id, place)
	d.markEnded(s.id)
	d.saveOrg()
	d.closeTab(s.id)
	d.refreshSidebar()
}

func (d *desk) finishTopicRotate(old *deskSession, slug string) {
	t, ok := cachedTopic(slug)
	if !ok {
		t = topicInfo{Slug: slug, Title: topicTitle(slug), Scope: []string{slug}}
	}
	go func() {
		prompt := d.topicPrompt(t, false)
		d.do(func() {
			if d.sessions[old.id] != old || old.endWait != nil {
				return
			}
			title, _, _ := d.rowText(old.id)
			ns := d.replaceWith(old, title)
			ns.restarting = true
			delete(d.org.Kmem, old.id)
			d.org.Kmem[ns.id] = slug
			d.saveOrg()
			d.notify(ns, "Fresh topic session: the previous one passed its end-of-session check and was deleted.")
			d.sendResume(ns, slug, prompt)
			d.deleteSession(old.id)
		})
	}()
}

// endCheckAllowed: plan-first lets through, while the check waits, the
// read-only set plus kmem (checkpoints, releases, the status fact) and
// `pzlock release` of a listed lock (decision 24); commits still need a plan.
func (d *desk) endCheckAllowed(s *deskSession, r *protocol.ControlRequest) bool {
	ew := s.endWait
	if ew == nil {
		return false
	}
	extra := append(d.readOnlyExtraFor(s), kmemSaveExtra...)
	for _, b := range ew.blockers {
		if b.Kind == "lock" && b.Name != "" {
			extra = append(extra, []string{"pzlock", "release", b.Name})
		}
	}
	return readOnlyRequest(r, extra)
}

// quitFail: a topic that did not pass its check when quitting.
type quitFail struct {
	title    string
	blockers []endBlocker
	err      string
}

// quitCheckSessions: the visible running topic sessions that run the check
// when Quill Desktop quits.
func (d *desk) quitCheckSessions() []*deskSession {
	var out []*deskSession
	for _, s := range d.runningVisible() {
		if d.endCheckFor(s) && s.endWait == nil {
			out = append(out, s)
		}
	}
	return out
}

// quitEndChecks runs the check for every session in list at once (60 s cap
// each) and calls done on the UI thread with the ones that did not pass.
func (d *desk) quitEndChecks(list []*deskSession, done func([]quitFail)) {
	left := len(list)
	var failed []quitFail
	for _, s := range list {
		title, _, _ := d.rowText(s.id)
		ew := &endWait{why: endForQuit, name: d.org.Kmem[s.id]}
		ew.done = func(ok bool, err string) {
			if !ok {
				failed = append(failed, quitFail{title: title, blockers: ew.blockers, err: err})
			}
			if left--; left == 0 {
				done(failed)
			}
		}
		d.startEndCheck(s, ew)
		d.afterUI(quitCheckCap, func() {
			if s.endWait != ew {
				return
			}
			s.endWait = nil
			why := ""
			if !ew.asked {
				why = "kmem did not answer"
			}
			ew.finish(false, why)
			d.refreshSidebar()
		})
	}
}

func quitFailMessage(fs []quitFail) string {
	var b strings.Builder
	b.WriteString("These topics did not pass their end-of-session check:\n")
	for _, f := range fs {
		b.WriteString("\n• " + f.title)
		switch {
		case len(f.blockers) > 0:
			b.WriteString(": " + blockerList(f.blockers, "; "))
		case f.err != "":
			b.WriteString(": " + f.err)
		}
	}
	b.WriteString("\n\nStop and quit anyway? Cancel keeps every session running.")
	return b.String()
}

func (d *desk) confirmQuitAnyway(fs []quitFail, quit func()) {
	l := widget.NewLabel(quitFailMessage(fs))
	l.Wrapping = fyne.TextWrapWord
	dlg := dialog.NewCustomConfirm("Quit anyway?", "Stop and quit", "Cancel", l, func(ok bool) {
		if ok {
			quit()
		}
	}, d.win)
	dlg.Resize(fyne.NewSize(520, 320))
	dlg.Show()
}
