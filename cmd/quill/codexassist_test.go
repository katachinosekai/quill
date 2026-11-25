package main

import (
	"testing"

	"github.com/katachinosekai/quill/internal/protocol"
)

func TestCodexAgentSelectorsKeepDraftsSeparate(t *testing.T) {
	h, _ := quillSetup(t)
	h.ui(func() {
		d := h.d
		claude := d.createSession(h.proj, "default", "default")
		d.input.SetText("Claude tab draft")
		found := false
		for _, item := range d.backendItems(claude, func() {}) {
			if item.Label == "ChatGPT" {
				found = true
			}
		}
		if !found {
			t.Fatal("Codex must be available in the Online menu")
		}

		d.assistantsBoot()
		d.openAssistBox(boxQuill)
		if d.quillAssist().s != nil || !d.abox.start.Visible() {
			t.Fatal("opening Quill must wait for Start")
		}
		d.abox.agent.SetSelected(codexAgent)
		quill := d.assistByRole(roleCodexQuill)
		if quill == nil || d.targeted(d.input) != roleCodexQuill {
			t.Fatal("the Quill selector should target its Codex agent")
		}
		d.input.SetText("Codex in Quill draft")
		if !d.sendTargeted(d.input, d.input.Text, false) || quill.s != nil || d.input.Text == "" {
			t.Fatal("a draft sent before pressing Start must stay in the box")
		}

		d.openAssistBox(boxKmem)
		w := d.kmemWin
		w.agent.SetSelected(codexAgent)
		mem := d.assistByRole(roleCodexKmem)
		if mem == nil || mem == quill || d.targeted(d.input) != roleCodexKmem {
			t.Fatal("the kmem selector should target a separate Codex agent")
		}
		d.input.SetText("Codex in kmem draft")
		w.agent.SetSelected(claudeAgent)
		if d.targeted(d.input) != roleKmem {
			t.Fatal("Claude must remain selectable inside the kmem window")
		}

		d.openQuillCodex()
		if d.input.Text != "Codex in Quill draft" {
			t.Fatalf("Quill agent draft: %q", d.input.Text)
		}
		d.openKmemCodex()
		if d.input.Text != "Codex in kmem draft" {
			t.Fatalf("kmem agent draft: %q", d.input.Text)
		}
		d.closeAssistBox()
		if d.current != claude || d.input.Text != "Claude tab draft" {
			t.Fatalf("Claude tab lost its draft: %q", d.input.Text)
		}
	})
}

func TestQuillBoxQueueRefreshesWhileAttached(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		a := h.d.codexAssistant(codexQuillRole)
		s := h.d.addSessionWithBackend(t.TempDir(), "default", "default", false, true)
		h.d.markHiddenAs(s, roleCodexQuill)
		a.s = s
		a.view.show(s)
		s.queue = []string{"pending"}
		h.d.syncQueue(s)
		if len(a.view.queueBox.Objects) != 2 {
			t.Fatal("the attached Quill box must show the queued message")
		}
		s.queue = nil
		h.d.syncQueue(s)
		if len(a.view.queueBox.Objects) != 0 {
			t.Fatal("the attached Quill box still shows a delivered message")
		}
	})
}

func TestCodexActivityChipsUseAttachedQuillBox(t *testing.T) {
	h, _ := quillSetup(t)
	h.ui(func() {
		d := h.d
		tab := d.createSession(h.proj, "default", "default")
		d.openQuillCodex()
		a := d.assistByRole(roleCodexQuill)
		s := d.addSessionWithBackend(h.proj, "default", "default", false, true)
		d.markHiddenAs(s, roleCodexQuill)
		a.s = s
		a.view.show(s)
		d.setCollapseChips(false)
		add := func(s *deskSession, line string) {
			t.Helper()
			ev, err := protocol.ParseLine([]byte(line))
			if err != nil {
				t.Fatal(err)
			}
			s.conv.Apply(ev)
		}
		add(tab, `{"type":"assistant","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"bash","name":"Bash","input":{"command":"echo Claude"}}]}}`)
		add(s, `{"type":"assistant","message":{"id":"m2","role":"assistant","content":[{"type":"tool_use","id":"cmd","name":"Command","input":{"command":"echo Codex"}}]}}`)
		tabChip, codexChip := tab.conv.Items[len(tab.conv.Items)-1], s.conv.Items[len(s.conv.Items)-1]
		if d.targeted(d.input) != roleCodexQuill || !a.view.isOpen(codexChip) {
			t.Fatal("expected the attached Codex box")
		}
		if activityChipLabel(tabChip.ToolName) != activityChipLabel(codexChip.ToolName) {
			t.Fatal("both engines should use the same command chip label")
		}
		d.setCollapseChips(true)
		if !a.view.isOpen(codexChip) {
			t.Fatal("the default changed an existing Codex chip")
		}
		add(s, `{"type":"assistant","message":{"id":"m3","role":"assistant","content":[{"type":"tool_use","id":"cmd2","name":"Command","input":{"command":"echo later"}}]}}`)
		if a.view.isOpen(s.conv.Items[len(s.conv.Items)-1]) {
			t.Fatal("a new activity chip should honor the collapsed default")
		}
		d.chipItems()[2].Action()
		if a.view.isOpen(codexChip) || !d.chat.isOpen(tabChip) {
			t.Fatal("Collapse activity chips must change only the targeted Codex box")
		}
		d.chipItems()[1].Action()
		if d.hideChips || !a.view.isOpen(codexChip) || !d.app.Preferences().Bool("collapseChips") {
			t.Fatal("Open activity chips or the saved default was lost")
		}
		d.chipItems()[0].Action()
		if !d.hideChips || !d.app.Preferences().Bool("hideChips") {
			t.Fatal("Show activity chips did not toggle and persist visibility")
		}
	})
}
