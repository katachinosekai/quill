package main

import (
	"fmt"
	"os"
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
// assistant (assistDeliver, assist.go).
func (d *desk) kmemAssistDeliver(text string) string { return d.assistDeliver(d.assist(), text) }

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
