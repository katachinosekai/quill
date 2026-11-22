package main

import "testing"

func TestKmemNameIn(t *testing.T) {
	for in, want := range map[string]string{
		`use kmem name "Quill 2" (kmem --as "Quill 2" ...)`: "Quill 2",
		"run `kmem --as demo brief` first":                 "demo",
		`kmem --as=demo rules`:                             "demo",
		`please fix the build`:                             "",
		`this session's kmem name is "x"`:                  "",
	} {
		if got := kmemNameIn(in); got != want {
			t.Errorf("kmemNameIn(%q) = %q, want %q", in, got, want)
		}
	}
}

// A fresh session's first message (not Quill's resume prompt) lets the
// read-only kmem startup commands for the name it gives skip plan-first,
// for that turn only.
func TestKmemFirstTurnGate(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.planFirst = true
		s := h.d.createSession(h.proj, "default", "default")
		startup := []string{
			`kmem --as "demo" brief; echo "==== HANDOFF"; kmem --as "demo" handoff get demo; kmem --as "demo" rules; kmem --as "demo" task list`,
			`kmem --as demo find "plan-first"`,
			`kmem --as demo context "fix the gate"`,
		}
		gated := []string{
			`kmem --as demo add "x"`,
			`kmem --as demo task claim t0000a1`,
			`kmem --as other brief`,
			`rm -rf /home/user/Projects/demo`,
		}
		h.d.sendTo(s, `You are the demo session; use kmem name "demo" (kmem --as "demo" ...). Start by running kmem brief.`)
		for _, c := range startup {
			if !h.d.kmemResumeAllowed(s, bashReq(c)) {
				t.Fatalf("first turn lets %q through", c)
			}
		}
		for _, c := range gated {
			if h.d.kmemResumeAllowed(s, bashReq(c)) {
				t.Fatalf("first turn: %q must stay gated", c)
			}
		}
		s.inTurn = false
		h.d.sendTo(s, "now fix the build, kmem --as demo is fine")
		for _, c := range startup {
			if h.d.kmemResumeAllowed(s, bashReq(c)) {
				t.Fatalf("second message: %q must stay gated", c)
			}
		}
	})
}

// A first message naming no kmem name uses the session's own kmem name.
func TestKmemFirstTurnGateSessionName(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.planFirst = true
		s := h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[s.id] = "demo"
		h.d.sendTo(s, "hello")
		if !h.d.kmemResumeAllowed(s, bashReq(`kmem --as demo brief`)) {
			t.Fatal("session's own kmem name passes on the first turn")
		}
		if h.d.kmemResumeAllowed(s, bashReq(`kmem --as other brief`)) {
			t.Fatal("other names stay gated")
		}
	})
}
