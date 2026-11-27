package main

import (
	"os"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func kmemBoxHarness(t *testing.T) *harness {
	h := newHarness(t)
	h.d.kmemOff = false
	h.d.kmemVer.reset()
	oldOpen, oldClose := kmemWindowOpenHook, kmemWindowCloseHook
	kmemWindowOpenHook, kmemWindowCloseHook = nil, nil
	t.Cleanup(func() { kmemWindowOpenHook, kmemWindowCloseHook = oldOpen, oldClose })
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "version --json":
			return `{"version": "1.8.1", "api": 1}`, nil
		case "brief --json":
			return `{"brief": "rules: read, unchanged\n", "api": 1}`, nil
		case "task list --json":
			return `{"tasks": [], "api": 1}`, nil
		case "handoff list --json":
			return `{"handoffs": [], "api": 1}`, nil
		}
		return "", nil
	}
	return h
}

func TestKmemBoxContentHasRailAndChat(t *testing.T) {
	h := kmemBoxHarness(t)
	h.ui(func() {
		obj := h.d.kmemBoxContent()
		w := h.d.kmemWin
		if w == nil || w.win != nil || obj != fyne.CanvasObject(w.panel.tabs) {
			t.Fatal("the content is the sidebar and selected page, built without a window")
		}
		names := ""
		for _, it := range w.panel.tabs.Items {
			names += it.Text + ","
		}
		if names != "Assistant,Overview,Handoffs,Tasks,Memory," || w.panel.tabs.SelectedIndex() != kmemTabAssistant {
			t.Fatalf("unexpected sidebar pages or default: %s, %d", names, w.panel.tabs.SelectedIndex())
		}
		if w.agent == nil || w.agent.Selected != claudeAgent || len(w.chatSlot.Objects) != 1 ||
			w.chatSlot.Objects[0] != w.right || len(w.right.Objects) != 1 {
			t.Fatal("the chat side opens with the Claude Code chat and an agent selector")
		}
		if h.d.kmemBoxContent() != obj {
			t.Fatal("asking again returns the same content")
		}
		os.MkdirAll("/tmp/quill-op-d-shots", 0o755)
		tw := test.NewWindow(obj)
		tw.Resize(fyne.NewSize(1000, 600))
		shot(t, tw, "/tmp/quill-op-d-shots", "kmem-box-content.png")
		tw.Close()
	})
}

func TestKmemPopOutKeepsState(t *testing.T) {
	h := kmemBoxHarness(t)
	var content fyne.CanvasObject
	h.ui(func() {
		content = h.d.kmemBoxContent()
		h.d.kmemWin.panel.tabs.SelectIndex(kmemTabTasks)
		h.d.kmemWin.setChat(wrapLabel("(chat)"))
		h.d.popOutKmem()
		w := h.d.kmemWin
		if w.win == nil || w.win.Content() != content {
			t.Fatal("Pop out shows the same content in its own window")
		}
		if w.panel.tabs.SelectedIndex() != kmemTabTasks {
			t.Fatal("the selected tab is kept")
		}
		if !strings.Contains(labelsIn([]fyne.CanvasObject{w.right}), "(chat)") {
			t.Fatal("the chat is kept")
		}
		h.d.popOutKmem()
		if h.d.kmemWin != w {
			t.Fatal("pop out again raises")
		}
		w.close()
	})
}

func TestShowKmemFromButtonAndCommand(t *testing.T) {
	h := kmemBoxHarness(t)
	h.ui(func() {
		d := h.d
		test.Tap(d.kmemBtn)
		w := d.kmemWin
		if d.assistBoxRole() != boxKmem || w == nil || w.win != nil || d.box().body.Objects[0] != fyne.CanvasObject(w.panel.tabs) {
			t.Fatal("the kmem button calls showKmem, which opens the kmem box with kmemBoxContent")
		}
		if !d.box().popBtn.Visible() || d.box().title.Text != "kmem assistant" {
			t.Fatal("the kmem box has Pop out")
		}
		test.Tap(d.kmemBtn)
		if d.assistBoxRole() != "" {
			t.Fatal("the button again closes the box")
		}
		d.showKmem()
		test.Tap(d.box().popBtn)
		if d.assistBoxRole() != "" || d.kmemWin == nil || d.kmemWin.win == nil || len(d.box().body.Objects) != 0 {
			t.Fatal("Pop out closes the box and shows the content in its window")
		}
		pop := d.kmemWin
		d.showKmem()
		if d.assistBoxRole() != "" || d.kmemWin != pop {
			t.Fatal("while popped out, showKmem raises the window")
		}
		pop.close()
		d.showKmem()
		if d.assistBoxRole() != boxKmem || d.kmemWin == nil || d.kmemWin == pop || d.box().body.Objects[0] != fyne.CanvasObject(d.kmemWin.panel.tabs) {
			t.Fatal("after the window closes, the box shows a fresh content")
		}
		d.closeAssistBox()
	})
}

func TestKmemSidebarPreservesDraftAndApproval(t *testing.T) {
	for _, codex := range []bool{false, true} {
		name := claudeAgent
		if codex {
			name = codexAgent
		}
		t.Run(name, func(t *testing.T) {
			h := kmemBoxHarness(t)
			h.ui(func() {
				d := h.d
				d.win.Resize(fyne.NewSize(1800, 1000))
				d.app.Preferences().SetBool("kmemAgentCodex", codex)
				d.showKmem()
				w := d.kmemWin
				s := d.addSessionWithBackend(h.proj, "default", "default", false, codex)
				if codex {
					d.markHiddenAs(s, roleCodexKmem)
					a := d.codexAssistant(codexKmemRole)
					a.s = s
					a.view.show(s)
				} else {
					d.markHiddenAs(s, roleKmem)
					a := d.assist()
					a.s, a.w, a.state = s, w, asLive
					kmemWinAttach(d, a)
				}
				w.composer().SetText("draft for kmem")
				s.conv.AddPlan()
				w.panel.tabs.SelectIndex(kmemTabTasks)
				if d.hostShown(s) || len(s.conv.PendingPermissions()) != 1 {
					t.Fatal("the utility page should hide the chat and preserve the approval")
				}
				w.panel.tabs.SelectIndex(kmemTabAssistant)
				if !d.hostShown(s) || w.composer().Text != "draft for kmem" || len(s.conv.PendingPermissions()) != 1 {
					t.Fatal("switching pages lost the chat, draft, or approval")
				}
				w.panel.tabs.SelectIndex(kmemTabTasks)
				test.Tap(d.box().popBtn)
				if w.win == nil || w.panel.tabs.SelectedIndex() != kmemTabTasks || w.composer().Text != "draft for kmem" {
					t.Fatal("pop out lost the page or draft")
				}
				d.hostFor(hiddenRole(s)).open()
				if w.panel.tabs.SelectedIndex() != kmemTabAssistant || !d.hostShown(s) {
					t.Fatal("approval notification did not reveal the assistant in the pop-out")
				}
				w.panel.tabs.SelectIndex(kmemTabTasks)
				w.panel.prefill()("Review the task")
				if w.panel.tabs.SelectedIndex() != kmemTabAssistant || w.composer().Text != "Review the task" {
					t.Fatal("a utility action did not fill the visible assistant composer")
				}
				w.panel.tabs.SelectIndex(kmemTabTasks)
				w.composer().TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
				if !w.closed || len(s.conv.PendingPermissions()) != 1 {
					t.Fatal("closing a utility page interrupted the pending approval")
				}
				d.showKmem()
				if w := d.kmemWin; !w.assistantSelected() || w.composer().Text != "Review the task" {
					t.Fatal("reopening did not restore the draft on Assistant")
				}
			})
		})
	}
}
