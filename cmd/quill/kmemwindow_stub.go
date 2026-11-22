package main

import (
	"os"
	"path/filepath"
	"strings"
)

// Phase 0 of the kmem operations window (docs/design/kmem-window.md §11):
// shared stubs on master so the window (A), hidden-session (B) and
// assistant (C) work can branch without fighting over desk.go. A replaces
// openKmemWindow and fills kmemWindow; B routes events through viewFor and
// filters hidden sessions; C starts the assistant in kmemWindowDir.

// kmemWindow is the open kmem operations window (nil while closed).
type kmemWindow struct {
	s    *deskSession // the assistant session shown on the right, or nil
	view *chatView    // its chat view, or nil
}

// session: the window's assistant session (nil-safe).
func (w *kmemWindow) session() *deskSession {
	if w == nil {
		return nil
	}
	return w.s
}

// viewFor (queue.go) also returns the kmem window's view for its session.

// kmemWindowDir: the working folder of the kmem window's assistant, so its
// transcripts sit in their own Claude projects folder.
func kmemWindowDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "quill", "kmem-window")
}

// isKmemWindowDir: cwd is the kmem window assistant's folder.
func isKmemWindowDir(cwd string) bool {
	if cwd == "" {
		return false
	}
	return filepath.Clean(strings.TrimSpace(cwd)) == filepath.Clean(kmemWindowDir())
}

// isHiddenSession: s is the kmem window's assistant (flag or folder).
func isHiddenSession(s *deskSession) bool {
	return s != nil && (s.hidden || isKmemWindowDir(s.cwd))
}

// openKmemWindow opens the kmem operations window; until A lands it opens
// today's kmem panel.
func (d *desk) openKmemWindow() {
	d.showKmemPanel()
}
