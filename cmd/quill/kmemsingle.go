package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// One live Quill Desktop session per kmem name (bug 2026-11-23: a scheduled
// check-in started a second kmem-lock-audit session that overwrote the
// running assistant's handoff). Every path that would start a session under
// a kmem name asks liveKmemOwner first and focuses the live one instead.

// liveKmemOwner: the live session (tab, running, or the kmem window's) that
// holds the kmem name, other than self; nil if none. `kmem sessions --live`
// is not consulted: this kmem has no such command.
func (d *desk) liveKmemOwner(name string, self *deskSession) *deskSession {
	if name == "" {
		return nil
	}
	return d.handoffOwner(name, self)
}

// refuseSecondSession logs why no second session starts under name and
// brings the live one forward (the kmem window for the hidden assistant).
func (d *desk) refuseSecondSession(name, via string, owner *deskSession) {
	msg := fmt.Sprintf("Quill Desktop: not starting a second session under the kmem name %q (%s): one is already live; using it.", name, via)
	fmt.Fprintln(os.Stderr, msg)
	d.flashHint(d.folder, msg)
	if isHiddenSession(owner) {
		d.openKmemWindow()
		return
	}
	d.open(owner.id)
}

// kmemAssistDeliver hands text (the Monday check-in, say) to the kmem
// assistant: a message to the running one (queued if it is mid-turn, as user
// messages are); a new assistant starts only when none runs, and gets the
// message after its resume prompt. Returns what happened, for the log.
func (d *desk) kmemAssistDeliver(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "empty"
	}
	a := d.assist()
	if s := a.s; s != nil && s.running() && !s.stopping && (a.state == asLive || a.state == asStarting) {
		d.sendTo(s, text)
		d.kmemAssistRefresh(a)
		return "sent to the running assistant"
	}
	if o := d.kmemAssistOwner(); o != nil && o.running() && !o.stopping {
		d.sendTo(o, text) // live in a tab: it is the one kmem-lock-audit
		return "sent to the live tab"
	}
	a.pending = append(a.pending, text)
	switch a.state {
	case asProbing, asSaving, asStopping, asStarting:
		return "queued until the assistant is up" // flushed by the start (or restarted after the stop)
	}
	a.userStopped = false
	d.kmemAssistProbe(a)
	return "starting the assistant"
}

// kmemAssistFlush sends the queued messages to the running assistant.
func (d *desk) kmemAssistFlush(a *kmemAssist, s *deskSession) {
	p := a.pending
	a.pending = nil
	for _, t := range p {
		d.sendTo(s, t)
	}
}

// kmemAssistWaitStopped: a previous hidden session still stops (rotation,
// Restart on a new model): start nothing until its process is gone.
func (d *desk) kmemAssistWaitStopped(a *kmemAssist, o *deskSession) {
	d.kmemAssistReason(a, "Waiting for the previous kmem assistant to stop...")
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
			d.kmemAssistProbe(a)
		})
	}
	poll(0)
}

// stopThen stops old's process (it has saved its handoff), then runs f on
// the UI thread: rotation stops first and only then starts the new session.
func (d *desk) stopThen(old *deskSession, f func()) {
	if !old.running() {
		f()
		return
	}
	old.stopping = true
	p := old.proc
	go func() {
		_ = p.Stop(5 * time.Second)
		d.do(f)
	}()
}
