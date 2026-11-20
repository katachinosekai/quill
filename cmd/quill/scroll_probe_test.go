package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"

	"github.com/katachinosekai/quill/internal/chat"
)

func TestFollowShowsWholeLastMessage(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		for i := 0; i < 20; i++ {
			s.conv.AddUser(fmt.Sprintf("q %d", i))
		}
		h.d.chat.show(s)
	})
	long := "## Result\n\n" + strings.Repeat("- a fairly long bullet point that will wrap across the chat width when rendered\n", 12) + "\n" + strings.Repeat("Closing paragraph text. ", 40)
	var it *chat.Item
	h.ui(func() {
		it = &chat.Item{Seq: 9999, Kind: chat.Assistant, Text: "", Streaming: true}
		s.conv.Items = append(s.conv.Items, it)
		h.d.chat.update([]*chat.Item{it})
	})
	for n := 0; n < len(long); n += 60 {
		h.ui(func() {
			it.Text = long[:min(len(long), n+60)]
			h.d.chat.update([]*chat.Item{it})
		})
		time.Sleep(5 * time.Millisecond)
	}
	h.ui(func() {
		it.Streaming = false
		h.d.chat.update([]*chat.Item{it})
	})
	time.Sleep(400 * time.Millisecond)
	h.ui(func() {
		v := h.d.chat
		gap := v.scroll.Content.Size().Height - (v.scroll.Offset.Y + v.scroll.Size().Height)
		gapMin := v.scroll.Content.MinSize().Height - (v.scroll.Offset.Y + v.scroll.Size().Height)
		t.Logf("follow=%v gap(size)=%.1f gap(min)=%.1f", v.follow, gap, gapMin)
		if gap > 2 || gapMin > 2 {
			t.Fatalf("last message not fully shown: %.1f px hidden", max(gap, gapMin))
		}
	})
}

func TestFollowSurvivesViewportShrink(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		for i := 0; i < 60; i++ {
			s.conv.AddUser(fmt.Sprintf("q %d", i))
		}
		h.d.chat.show(s)
		v := h.d.chat
		sz := v.scroll.Size()
		v.scroll.Resize(fyne.NewSize(sz.Width, sz.Height-40))
		if v.pinned() {
			t.Skip("viewport change did not hide anything in the test driver")
		}
		v.stick()
		if !v.pinned() {
			t.Fatal("follow guard should bring the bottom back into view")
		}
	})
}
