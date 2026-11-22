package main

import (
	"fmt"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

// Hidden chips leave no blank space (user 2026-11-22): a long run of tool
// calls (e.g. a subagent's) and auto-allowed permissions between two replies
// take no height while chips are hidden, and come back when shown.
func TestHiddenChipsTakeNoSpace(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.setShowChips(false)
		plain := h.d.createSession(h.proj, "default", "default")
		plain.conv.Items = []*chat.Item{
			{Seq: 1, Kind: chat.Assistant, Text: "Both subagents are running."},
			{Seq: 2, Kind: chat.Assistant, Text: "The full test suite passes."},
		}
		h.d.chat.show(plain)
		want := h.d.chat.box.MinSize().Height

		busy := h.d.createSession(h.proj, "default", "default")
		items := []*chat.Item{{Seq: 1, Kind: chat.Assistant, Text: "Both subagents are running."}}
		seq := 2
		for i := 0; i < 30; i++ {
			items = append(items, &chat.Item{Seq: seq, Kind: chat.Tool, ToolName: "Bash", HasResult: true, Input: []byte(fmt.Sprintf(`{"command":"grep %d"}`, i))})
			seq++
			if i%6 == 0 {
				items = append(items, &chat.Item{Seq: seq, Kind: chat.Permission, ToolName: "Bash", PermState: chat.PermAutoAllowed})
				seq++
			}
		}
		items = append(items, &chat.Item{Seq: seq, Kind: chat.Assistant, Text: "The full test suite passes."})
		busy.conv.Items = items
		h.d.chat.show(busy)
		if got := h.d.chat.box.MinSize().Height; got != want {
			t.Fatalf("hidden chips add %v px of blank space", got-want)
		}
		h.d.setShowChips(true)
		if got := h.d.chat.box.MinSize().Height; got <= want {
			t.Fatalf("shown chips take their row again: %v <= %v", got, want)
		}
		h.d.setShowChips(false)
		if got := h.d.chat.box.MinSize().Height; got != want {
			t.Fatalf("hidden again: %v px extra", got-want)
		}
	})
}
