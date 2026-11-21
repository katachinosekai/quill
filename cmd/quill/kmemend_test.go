package main

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEndAndSaveToKmem(t *testing.T) {
	h := newHarness(t)
	h.d.kmemOff = false
	var mu sync.Mutex
	list := ""
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if prog == "kmem" && len(args) > 1 && args[0] == "handoff" && args[1] == "list" {
			return list, nil
		}
		return "", errors.New("unexpected")
	}
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[s.id] = "endme"
		h.d.send("start")
	})
	h.eventually("running and idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		h.d.endAndSave(s.id)
		if s.kmemSave == nil || !s.kmemSave.end {
			t.Fatal("End and save asks the session to save first")
		}
		last := s.conv.Items[len(s.conv.Items)-1]
		if !strings.Contains(last.Text, "kmem handoff save endme") || !strings.Contains(last.Text, "stopped and archived") {
			t.Fatalf("save request: %q", last.Text)
		}
		s.kmemSave.spare = 0
		h.d.kmemAfterTurn(s)
	})
	h.eventually("an unsaved handoff cancels the end", func() bool { return s.kmemSave == nil })
	h.ui(func() {
		if !s.running() || s.stopping || h.d.org.Place[s.id] == placeArchive {
			t.Fatal("nothing is stopped or archived without a save")
		}
		h.d.endAndSave(s.id)
		mu.Lock()
		list = "endme  3 items, saved " + time.Now().Add(time.Minute).Format(time.RFC3339) + "\n"
		mu.Unlock()
		h.d.kmemAfterTurn(s)
	})
	h.eventually("saved: stopped and archived", func() bool { return s.stopping && h.d.org.Place[s.id] == placeArchive })
	h.ui(func() {
		if slices.Contains(h.d.tabs, s.id) || h.d.sessions[s.id] == nil {
			t.Fatal("the tab closes and the session is kept (not deleted)")
		}
	})
}
