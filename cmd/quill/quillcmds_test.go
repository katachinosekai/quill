package main

import (
	"strings"
	"testing"

	"github.com/katachinosekai/quill/internal/protocol"
)

func TestClaudeCommandsAdaptedToQuill(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.open(s.id)
		h.d.cmdList = []protocol.Command{{Name: "color"}, {Name: "focus"}, {Name: "rename", Description: "Rename the current conversation"}, {Name: "compact"}}
		names := map[string]string{}
		for _, c := range h.d.commandsFor(s) {
			names[c.Name] = c.Description
		}
		if _, ok := names["color"]; ok {
			t.Error("/color (terminal only) is hidden")
		}
		if _, ok := names["focus"]; ok {
			t.Error("/focus (terminal only) is hidden")
		}
		if !strings.Contains(names["rename"], "Quill") || names["compact"] != "" || !strings.Contains(names["remote-control"], "Quill") || names["rc"] == "" {
			t.Errorf("Quill's /rename, /remote-control and /rc replace Claude Code's; others stay: %v", names)
		}
		if _, ok := names["compact"]; !ok {
			t.Error("/compact still goes to Claude Code")
		}

		if !h.d.runLocalCommandFor(s, "/rename Bug hunt") {
			t.Fatal("/rename is handled by Quill")
		}
		if title, _, _ := h.d.rowText(s.id); title != "Bug hunt" {
			t.Fatalf("/rename NAME renames the tab: %q", title)
		}
		if !h.d.runLocalCommandFor(s, "/focus") || !strings.Contains(s.conv.Items[len(s.conv.Items)-1].Text, "terminal") {
			t.Fatal("/focus is answered by Quill, not sent")
		}
		if !h.d.runLocalCommandFor(s, "/rc off") || !strings.Contains(s.conv.Items[len(s.conv.Items)-1].Text, "already off") {
			t.Fatal("/rc off on a session without Remote Control says so")
		}
		if !deviceCommandBlocked("/rename") || deviceCommandBlocked("/rename Phone name") || deviceCommandBlocked("/rc on") {
			t.Fatal("phones may rename with a name and use /rc; a bare /rename (a desktop dialog) is blocked")
		}
	})
}
