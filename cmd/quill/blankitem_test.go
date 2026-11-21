package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

// Finished items with nothing to show take no row; streaming ones and items
// with other content still show.
func TestBlankItemsTakeNoRow(t *testing.T) {
	h := newHarness(t)
	h.ui(func() { h.d.open(editsID) })
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	var blank, live, tool, text []*chat.Item
	h.ui(func() {
		s := h.d.sessions[editsID]
		for _, k := range []chat.Kind{chat.User, chat.Assistant, chat.Thinking, chat.Notice, chat.Error} {
			it := s.conv.AddNotice(" \n")
			it.Kind = k
			blank = append(blank, it)
		}
		l := s.conv.AddNotice("")
		l.Kind, l.Streaming = chat.Assistant, true
		live = append(live, l)
		tl := s.conv.AddNotice("")
		tl.Kind, tl.ToolName, tl.ToolID = chat.Tool, "Bash", "toolu_test"
		tool = append(tool, tl)
		text = append(text, s.conv.AddNotice("still here"))
		all := append(append(append(append([]*chat.Item{}, blank...), live...), tool...), text...)
		h.d.chat.update(all)
		for _, it := range blank {
			if iv := h.d.chat.views[it.Seq]; iv == nil || iv.holder.Visible() || iv.holder.MinSize().Height != 0 {
				t.Fatalf("blank kind %d takes a row", it.Kind)
			}
		}
		for _, it := range append(append(live, tool...), text...) {
			if iv := h.d.chat.views[it.Seq]; iv == nil || !iv.holder.Visible() {
				t.Fatalf("kind %d (streaming %v) is shown", it.Kind, it.Streaming)
			}
		}
		// A blank item that gets text later shows up.
		blank[1].Text = "late reply"
		h.d.chat.update([]*chat.Item{blank[1]})
		if iv := h.d.chat.views[blank[1].Seq]; !iv.holder.Visible() || iv.holder.MinSize().Height == 0 {
			t.Fatal("a blank reply that gets text is shown")
		}
	})
	if os.Getenv("DESK_SHOTS") != "" {
		dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
		os.MkdirAll(dir, 0o755)
		h.ui(func() { shot(t, h.w, dir, "blank-items-hidden.png") })
	}
}
