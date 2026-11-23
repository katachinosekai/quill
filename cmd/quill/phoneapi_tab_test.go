package main

import (
	"encoding/json"
	"testing"
)

func TestPhoneAPITabField(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		a := &phoneAPI{d: h.d, tracks: map[string]*itemTrack{}, lastSess: map[string]string{}, lastCtl: map[string]bool{}, wake: make(chan struct{})}
		s1 := h.d.createSession(h.proj, "default", "default")
		s2 := h.d.createSession(h.proj, "default", "default")
		s3 := h.d.createSession(h.proj, "default", "default")
		h.d.tabs = []string{s3.id, "remote:x", s1.id}
		if got := []int{a.tabIndex(s3.id), a.tabIndex(s1.id), a.tabIndex(s2.id)}; got[0] != 0 || got[1] != 1 || got[2] != -1 {
			t.Fatalf("tab indexes (remote skipped, closed -1): %v", got)
		}
		js, _ := json.Marshal(a.session(s2.id, "x"))
		var m map[string]any
		_ = json.Unmarshal(js, &m)
		if v, ok := m["tab"]; !ok || v.(float64) != -1 {
			t.Fatalf("closed session sends tab -1: %v", m["tab"])
		}
		a.scan()
		if a.lastSess[s3.id] == "" {
			t.Fatal("scan did not record the session")
		}
		before := a.lastSess[s3.id]
		a.mu.Lock()
		n := a.next
		a.mu.Unlock()
		h.d.tabs = []string{s1.id, s3.id} // tab move
		a.scan()
		if a.lastSess[s3.id] == before || a.lastSess[s1.id] == "" {
			t.Fatal("a tab move changes the session JSON")
		}
		a.mu.Lock()
		moved := len(a.ring) > 0 && a.next > n
		a.mu.Unlock()
		if !moved {
			t.Fatal("a tab move is reported as a change")
		}
	})
}
