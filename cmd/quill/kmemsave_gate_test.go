package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/katachinosekai/quill/internal/protocol"
)

func bashReq(cmd string) *protocol.ControlRequest {
	in, _ := json.Marshal(map[string]string{"command": cmd})
	return &protocol.ControlRequest{ToolName: "Bash", Input: in}
}

func TestKmemSaveGateLetsSaveChecksThrough(t *testing.T) {
	h := newHarness(t)
	home, _ := os.UserHomeDir()
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		saveCheck := "kmem handoff save --help 2>&1 | head -15; kmem task list 2>&1 | grep -i \"launcher\" | head; git -C /home/user/Projects/demo status --short | head; pzlock status 2>&1 | head -3"
		if h.d.kmemSaveAllowed(s, bashReq("kmem handoff save demo --state x")) {
			t.Fatal("without a pending save the plan-first gate stays as it is")
		}
		s.kmemSave = &kmemSave{name: "demo", end: true}
		for _, c := range []string{"kmem handoff save demo --state x", "kmem task release t0000a1", saveCheck, home + "/.local/bin/pzlock status"} {
			if !h.d.kmemSaveAllowed(s, bashReq(c)) {
				t.Fatalf("a pending save lets %q through", c)
			}
		}
		for _, c := range []string{"rm -rf /home/user/Projects/demo", "pzlock release demo", "git -C /home/user/Projects/demo commit -m x"} {
			if h.d.kmemSaveAllowed(s, bashReq(c)) {
				t.Fatalf("%q must stay gated", c)
			}
		}
	})
}
