package main

import (
	"runtime/debug"
	"sync"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
)

// idleUnloadAfter: a session with no tab, nothing running and no phone read
// for this long drops its conversation (about 2 MB or more each). Its title,
// last time and usage stay in d.infos for the sidebar, search and the phone's
// session list; opening it or a phone read loads the transcript again.
var idleUnloadAfter = 10 * time.Minute

// unloadable: s may drop its items now. Only a session that is saved on disk
// (the reload reads that file) and has nothing live, pending or on screen.
func (d *desk) unloadable(s *deskSession) bool {
	if s == nil || !s.loaded || s.hidden || s.remote != nil || d.hiddenID(s.id, "") {
		return false
	}
	if s.running() || s.proc != nil || s.terminal != nil || s.loading || s.takeover || s.failed != "" {
		return false
	}
	if s.stopping || s.restarting || s.compacting || s.pickPending || s.planWait || s.kmemNew ||
		s.kmemSave != nil || s.endWait != nil || s.qedit != nil || len(s.queue) > 0 ||
		s.resumeNext != "" || s.pendingEnd != 0 || s.clearAfterTurn || s.inTurn {
		return false
	}
	if c := s.conv; c == nil || c.Busy || c.Background() > 0 || len(c.PendingPermissions()) > 0 {
		return false
	}
	if info, ok := d.infos[s.id]; !ok || info.Path == "" {
		return false
	}
	if d.current == s || d.paneFor(s) != nil {
		return false
	}
	for _, t := range d.tabs {
		if t == s.id {
			return false
		}
	}
	return true
}

// unloadIdle drops the conversation of every session that has been
// unloadable for idleUnloadAfter. A session that is on a tab, shown or busy
// has its idle clock restarted, so the time counts from the tab closing.
// Returns how many were unloaded.
func (d *desk) unloadIdle(now time.Time) int {
	n := 0
	for _, s := range d.sessions {
		if !d.unloadable(s) || s.touched.IsZero() {
			s.touched = now
			continue
		}
		if now.Sub(s.touched) < idleUnloadAfter {
			continue
		}
		d.unload(s)
		n++
	}
	if n > 0 {
		memReturn.request()
	}
	return n
}

// unload: s goes back to the state of a session never opened (what
// d.session makes from a scan): empty conversation, not loaded.
func (d *desk) unload(s *deskSession) {
	s.conv, s.loaded, s.loadedMod, s.limitSeen = chat.New(), false, time.Time{}, ""
	d.chat.forget(s)
	for _, p := range d.panes {
		if p.view != nil && p.view != d.chat {
			p.view.forget(s)
		}
	}
	// The phone's tracker goes: a read loads the transcript again and makes a
	// new tracker whose epoch and rev are past every old one.
	if p := d.phone(); p != nil && p.api != nil {
		delete(p.api.tracks, s.id)
	}
}

// osMemReturner hands freed heap back to the OS (Go keeps it: 572 MB reserved
// against 50 MB in use, 2026-11-23) after big closes and unloads. FreeOSMemory
// forces a collection, so calls are at least gap apart: one that comes too
// soon is made once at the end of the gap, never more. It runs off the UI
// thread.
type osMemReturner struct {
	mu      sync.Mutex
	gap     time.Duration
	free    func()
	last    time.Time
	waiting bool // a call is scheduled and has not started
	running bool // a call is running
	dirty   bool // asked again while it ran: more to free
}

var memReturn = &osMemReturner{gap: 30 * time.Second, free: debug.FreeOSMemory}

func (m *osMemReturner) request() {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.waiting:
	case m.running:
		m.dirty = true
	default:
		m.schedule()
	}
}

// schedule (mu held): the next call, gap after the last one.
func (m *osMemReturner) schedule() {
	m.waiting = true
	time.AfterFunc(max(time.Duration(0), m.gap-time.Since(m.last)), func() {
		m.mu.Lock()
		m.waiting, m.running = false, true
		m.mu.Unlock()
		m.free()
		m.mu.Lock()
		defer m.mu.Unlock()
		m.last, m.running = time.Now(), false
		if m.dirty {
			m.dirty = false
			m.schedule()
		}
	})
}
