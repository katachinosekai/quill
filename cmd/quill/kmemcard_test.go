package main

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

func TestNewTabKmemCard(t *testing.T) {
	h := newHarness(t)
	h.d.kmemOff = false
	saved := time.Now().Add(-time.Hour).Format(time.RFC3339)
	var mu sync.Mutex
	var calls []string
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		mu.Lock()
		calls = append(calls, prog+" "+strings.Join(args, " "))
		mu.Unlock()
		switch {
		case prog == "kmem" && len(args) > 1 && args[0] == "handoff" && args[1] == "list":
			return "quill  3 items, saved " + saved + "\nnetworking  2 items, saved " + saved + "\n", nil
		case prog == "kmem" && len(args) > 1 && args[0] == "handoff" && args[1] == "rename":
			return "renamed", nil
		}
		return "", errors.New("unexpected")
	}
	var s *deskSession
	h.ui(func() {
		other := h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[other.id] = "networking"
		h.d.newSessionDialog()
		s = h.d.current
		if !s.pickPending {
			t.Fatal("a new tab asks resume or new")
		}
		h.d.send("hello first")
		if len(s.queue) != 1 || len(s.conv.Items) != 0 {
			t.Fatal("a message typed before picking waits")
		}
	})
	var labels []string
	h.eventually("handoff rows", func() bool {
		labels = nil
		deepWalk(h.d.chat.box, func(o fyne.CanvasObject) {
			if b, ok := o.(*widget.Button); ok {
				labels = append(labels, b.Text)
			}
		})
		return strings.Contains(strings.Join(labels, "|"), "quill · saved")
	})
	joined := strings.Join(labels, "|")
	if !strings.Contains(joined, "networking · saved") || !strings.Contains(joined, "open in tab") || !strings.Contains(joined, "Start new") || strings.Contains(joined, "No kmem") {
		t.Fatalf("card buttons: %v", labels)
	}
	h.ui(func() {
		h.d.pickNew(s, "My Work")
		if s.pickPending || h.d.org.Titles[s.id] != "My Work" || h.d.org.Kmem[s.id] != "my-work" {
			t.Fatalf("new: title %q kmem %q", h.d.org.Titles[s.id], h.d.org.Kmem[s.id])
		}
	})
	h.eventually("queued message sent after picking", func() bool {
		for _, it := range s.conv.Items {
			if strings.Contains(it.Text, "hello first") {
				return true
			}
		}
		return false
	})
	var r *deskSession
	h.ui(func() {
		h.d.newSessionDialog()
		r = h.d.current
		h.d.pickResume(r, "quill")
		if h.d.org.Titles[r.id] != "quill" || h.d.org.Kmem[r.id] != "quill" {
			t.Fatal("resume adopts the handoff's name")
		}
		h.d.org.Kmem[r.id] = "quill-desktop"
		h.d.syncTitleToKmem(r.id)
		if h.d.org.Titles[r.id] != "quill-desktop" {
			t.Fatal("a new kmem name renames the tab")
		}
	})
	h.ui(func() { h.d.renameKmem(s.id, "my-work", "my-project") })
	h.eventually("rename link", func() bool { return h.d.org.Kmem[s.id] == "my-project" })
	mu.Lock()
	defer mu.Unlock()
	for _, c := range calls {
		if strings.Contains(c, "handoff rename") {
			t.Fatal("an unsaved handoff only changes the link; no kmem rename")
		}
	}
	mu.Unlock()
	// A saved handoff is renamed in kmem too; from then on Quill asks for saves
	// under the new name (kmem 1.6.0 refuses `handoff save OLD` after a rename).
	h.ui(func() { h.d.renameKmem(r.id, "quill", "quill-2") })
	h.eventually("saved handoff renamed", func() bool { return h.d.org.Kmem[r.id] == "quill-2" })
	mu.Lock()
	renamed := false
	for _, c := range calls {
		renamed = renamed || c == "kmem handoff rename quill quill-2"
	}
	if !renamed {
		t.Fatalf("a saved handoff is renamed in kmem: %v", calls)
	}
	for _, m := range []string{kmemSaveMessage(h.d.org.Kmem[r.id]), kmemEndMessage(h.d.org.Kmem[r.id])} {
		if !strings.Contains(m, "kmem handoff save quill-2") || strings.Contains(m, "save quill ") {
			t.Fatalf("saves use the current name: %q", m)
		}
	}
}
