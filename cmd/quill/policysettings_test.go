package main

import (
	"strings"
	"sync"
	"testing"
)

func TestAutoHandoffSettingsRow(t *testing.T) {
	h := newHarness(t)
	var mu sync.Mutex
	var sets []string
	saved := `{"policy": {"soft": 40, "hard": 70, "mode": "ask", "ttl": null}, "source": "fact f0000a1", "api": 1}`
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		a := strings.Join(args, " ")
		switch {
		case a == "version --json":
			return `{"version": "1.4.0", "api": 1}`, nil
		case a == "handoff policy --json":
			return saved, nil
		case strings.HasPrefix(a, "handoff policy set "):
			sets = append(sets, a)
			return "ok", nil
		}
		return "", nil
	}
	h.d.kmemOff = false
	h.d.kmemVer.reset()
	var r *policyRow
	h.ui(func() {
		r = h.d.policyUI
		if r == nil {
			t.Fatal("Settings → Sessions has the Auto-handoff row")
		}
		h.d.loadPolicyRow()
	})
	h.eventually("policy loaded", func() bool {
		ok := false
		ok = r.soft.Selected == "40%" && r.hard.Selected == "70%" && r.mode.Selected == "Ask only"
		return ok
	})
	h.ui(func() {
		if !strings.Contains(r.status.Text, "saved in kmem") {
			t.Fatalf("the row says where the policy comes from: %q", r.status.Text)
		}
		r.soft.SetSelected("80%")
		r.hard.SetSelected("60%")
		h.d.savePolicy(r)
		if !strings.Contains(r.status.Text, "can't be higher") {
			t.Fatalf("soft above hard is refused: %q", r.status.Text)
		}
		r.soft.SetSelected("55%")
		r.hard.SetSelected("85%")
		r.mode.SetSelected("Automatic")
		h.d.savePolicy(r)
	})
	h.eventually("saved", func() bool {
		ok := false
		ok = strings.HasPrefix(r.status.Text, "Saved")
		return ok
	})
	mu.Lock()
	got := strings.Join(sets, "|")
	mu.Unlock()
	if got != "handoff policy set soft=55 hard=85 mode=auto" {
		t.Fatalf("one kmem policy set with the chosen values: %q", got)
	}
	h.ui(func() {
		if p := h.d.handoffPolicy(); p.Soft != 55 || p.Hard != 85 || p.Mode != "auto" {
			t.Fatalf("Quill uses the new policy at once: %+v", p)
		}
	})
}
