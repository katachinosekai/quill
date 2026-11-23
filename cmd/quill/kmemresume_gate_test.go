package main

import "testing"

func TestKmemResumeGateOnlyForResumeTurn(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.planFirst = true
		s := h.d.createSession(h.proj, "default", "default")
		startup := []string{
			`kmem --as "demo" brief; echo ----; kmem --as "demo" handoff get demo`,
			`kmem --as=demo rules`,
			`kmem --as demo get demo/notes`,
			`kmem --as demo task list`,
		}
		gated := []string{
			`kmem --as demo add "x"`,
			`kmem --as demo task claim t0000a1`,
			`kmem --as other add "x"`, // reads with --as are read-only anywhere (user 2026-11-23)
			`rm -rf /home/user/Projects/demo`,
		}
		// A new tab (no resume prompt sent) gets no exception.
		for _, c := range startup {
			if h.d.kmemResumeAllowed(s, bashReq(c)) {
				t.Fatalf("new tab: %q must stay gated", c)
			}
		}
		// Quill's own resume prompt opens the exception for that turn.
		h.d.sendResume(s, "demo", "You are the session for kmem handoff \"demo\"; ...")
		for _, c := range startup {
			if !h.d.kmemResumeAllowed(s, bashReq(c)) {
				t.Fatalf("resume turn lets %q through", c)
			}
		}
		for _, c := range gated {
			if h.d.kmemResumeAllowed(s, bashReq(c)) {
				t.Fatalf("resume turn: %q must stay gated", c)
			}
		}
		// The user's next message ends it.
		s.inTurn = false
		h.d.sendTo(s, "now fix the build")
		for _, c := range startup {
			if h.d.kmemResumeAllowed(s, bashReq(c)) {
				t.Fatalf("after the user's message: %q must stay gated", c)
			}
		}
	})
}
