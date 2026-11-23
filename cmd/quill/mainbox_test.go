package main

import (
	"testing"

	"fyne.io/fyne/v2"
)

// td1e074: over the chat the box has no message box of its own; the main one
// messages the assistant, each target keeps its draft, and closing gives the
// main box back to the tab.
func TestMainBoxMessagesTheOverlayAssistant(t *testing.T) {
	h, _ := quillSetup(t)
	a, op := bootQuill(h)
	var tab *deskSession
	h.ui(func() {
		d := h.d
		tab = d.createSession(h.proj, "default", "default")
		d.input.SetText("tab draft")
		d.openAssistBox(boxQuill)
		if d.input.Text != "" || d.input.PlaceHolder != "Message the Quill operator" || d.quillChat.inRow.Visible() {
			t.Fatalf("the main box messages the operator: %q %q", d.input.Text, d.input.PlaceHolder)
		}
		d.input.SetText("op draft")
		d.closeAssistBox()
		if d.input.Text != "tab draft" || d.input.PlaceHolder != "Message Claude" || d.targetLbl.Visible() {
			t.Fatalf("closing gives the box back with the tab's draft: %q", d.input.Text)
		}
		d.openAssistBox(boxQuill)
		if d.input.Text != "op draft" {
			t.Fatalf("the operator's draft comes back: %q", d.input.Text)
		}
		d.input.SetText("hello operator")
		d.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
		if lastUser(op) != "hello operator" || d.input.Text != "" || len(tab.conv.Items) != 0 {
			t.Fatalf("Enter sends to the operator, not the tab: %q", lastUser(op))
		}
		if a.s != op {
			t.Fatal("the same operator session")
		}
		// Escape in the main box closes the overlay, never the tab's turn.
		d.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
		if d.assistBoxRole() != "" || d.input.Text != "tab draft" {
			t.Fatal("Escape closes the box; the tab's draft is back")
		}
	})
	h.eventually("the operator's reply in the box", func() bool {
		return !op.conv.Busy && viewShows(h.d.quillChat.view, "echo: hello operator")
	})
	h.ui(func() {
		d := h.d
		// Switching the box's role hands the main box from one to the other.
		d.openAssistBox(boxQuill)
		d.input.SetText("for quill")
		d.setAssistDock(boxQuill, true)
		if !d.assistBoxDocked() || d.input.Text != "tab draft" || !d.quillChat.inRow.Visible() {
			t.Fatal("docked: unchanged, the box has its own message box")
		}
		d.setAssistDock(boxQuill, false)
		if d.input.Text != "for quill" {
			t.Fatal("undocked again: the operator's draft")
		}
		d.closeAssistBox()
	})
}

func TestTargetLabels(t *testing.T) {
	if targetLabel(boxQuill) != "Message the Quill operator" || targetLabel(boxKmem) != "Message the kmem assistant" {
		t.Fatal(targetLabel(boxQuill), targetLabel(boxKmem))
	}
}
