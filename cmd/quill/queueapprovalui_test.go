package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

func TestOperatorQueueEditorUsesWholeMessageAndSavesInPlace(t *testing.T) {
	for _, mode := range []string{"overlay", "docked"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			h.ui(func() {
				d := h.d
				d.createSession(h.proj, "default", "default")
				d.app.Preferences().SetBool(prefAssistDock+boxQuill, mode == "docked")
				d.openQuillCodex()
				c := d.codexQuillChat
				s := d.addSessionWithBackend(h.proj, "default", "default", false, true)
				d.markHiddenAs(s, roleCodexQuill)
				c.a.s, c.a.state = s, asLive
				c.view.show(s)
				whole := "First line\n" + strings.Repeat("0123456789", 22) + "\nLast line"
				s.queue = []string{"before", whole, "after"}
				in := d.input
				if mode == "docked" {
					in = c.input
				}
				in.SetText("Unsent draft")
				d.syncQueue(s)
				var rows []*queueRow
				for _, object := range c.view.queueBox.Objects {
					if row, ok := object.(*queueRow); ok {
						rows = append(rows, row)
					}
				}
				if len(rows) != 3 {
					t.Fatal("expected one queue row per message")
				}
				var edit *widget.Button
				deepWalk(rows[1].content, func(o fyne.CanvasObject) {
					if button, ok := o.(*widget.Button); ok && button.Icon != nil &&
						button.Icon.Name() == theme.DocumentCreateIcon().Name() {
						edit = button
					}
				})
				if edit == nil {
					t.Fatal("missing edit button")
				}
				test.Tap(edit)
				if s.qedit == nil || s.qedit.in != in || in.Text != whole || h.w.Canvas().Focused() != in {
					t.Fatal("Edit must load the entire queued message into the active operator composer")
				}
				edited := whole + "\nRevision"
				in.SetText(edited)
				in.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
				if s.qedit != nil || !slices.Equal(s.queue, []string{"before", edited, "after"}) || in.Text != "Unsent draft" || len(s.conv.Items) != 0 {
					t.Fatal("Enter should save the queue position and restore the draft without sending")
				}
				d.editQueued(s, 1)
				in.SetText("Discard this edit")
				d.closeAssistBox()
				if s.qedit != nil || !slices.Equal(s.queue, []string{"before", edited, "after"}) {
					t.Fatal("closing the operator box should put the original queued text back")
				}
			})
		})
	}
}

func TestCodexCommandApprovalDetailsStartCollapsed(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		d := h.d
		s := d.createSession(h.proj, "default", "default")
		d.chat.show(s)
		command := "first-line\n" + strings.Repeat("text ", 40) + "\nunique-last-line"
		raw, err := json.Marshal(map[string]any{
			"command": command, "cwd": "/project", "threadId": "internal-thread",
			"availableDecisions":    []string{"accept", "decline"},
			"additionalPermissions": map[string]any{"fileSystem": map[string]any{"write": []string{"/project/output"}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		it := &chat.Item{Kind: chat.Permission, ToolName: "Codex command", Input: raw, PermState: chat.PermPending,
			Request: &protocol.ControlRequest{DecisionReason: "Write the output file"}}
		it.Seq = 1
		s.conv.Items = append(s.conv.Items, it)
		d.chat.show(s)
		view := d.chat.views[it.Seq]
		var disclosure *widget.Button
		var buttons []string
		deepWalk(view.holder, func(o fyne.CanvasObject) {
			if button, ok := o.(*widget.Button); ok {
				buttons = append(buttons, button.Text)
				if button.Text == "Details" {
					disclosure = button
				}
			}
		})
		if disclosure == nil || !slices.Contains(buttons, "Allow") || !slices.Contains(buttons, "Deny") {
			t.Fatal("details, Allow and Deny must be available")
		}
		closed := view.height
		test.Tap(disclosure)
		if view.height <= closed {
			t.Fatal("opening Details must increase the height of the chat row")
		}
		var scroll *container.Scroll
		deepWalk(view.holder, func(o fyne.CanvasObject) {
			if pane, ok := o.(*container.Scroll); ok && pane.Visible() {
				scroll = pane
			}
		})
		if scroll == nil {
			t.Fatal("long command details should be scrollable")
		}
		var labels []string
		deepWalk(scroll.Content, func(o fyne.CanvasObject) {
			if label, ok := o.(*widget.Label); ok {
				labels = append(labels, label.Text)
			}
		})
		details := strings.Join(labels, "\n")
		if !strings.Contains(details, command) || !strings.Contains(details, "/project/output") ||
			strings.Contains(details, "internal-thread") || strings.Contains(details, "availableDecisions") {
			t.Fatalf("unexpected command details: %q", details)
		}
		test.Tap(disclosure)
		if view.height != closed || scroll.Visible() {
			t.Fatal("closing Details must restore the compact height")
		}
	})
}
