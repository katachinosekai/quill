package main

import (
	"os"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/katachinosekai/quill/internal/chat"
)

// STATUS_SHOTS=dir renders the status line in the main window and the kmem
// window to PNGs.
func TestStatusScreenshots(t *testing.T) {
	dir := os.Getenv("STATUS_SHOTS")
	if dir == "" {
		t.Skip("set STATUS_SHOTS=<dir> to render the status line")
	}
	os.MkdirAll(dir, 0o755)
	h, _, rec := assistSetup(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "haiku", "default")
		h.d.send("demo")
	})
	h.eventually("the turn ends", func() bool { return s.running() && len(s.conv.Items) > 1 })
	time.Sleep(3 * time.Second)
	h.ui(func() {
		s.conv.CancelPending()
		s.conv.Busy = false
		for _, it := range s.conv.Items {
			it.Streaming = false
		}
	})
	snap := func(name string) {
		time.Sleep(200 * time.Millisecond)
		h.ui(func() { shot(t, h.w, dir, name) })
	}
	h.ui(func() { h.d.updateChrome() })
	snap("main-ready.png")
	h.ui(func() {
		bgTool(s.conv, "a1", "Agent", `{"prompt":"x","run_in_background":true}`, "Async agent launched. agentId: agent12345")
		bgTool(s.conv, "a2", "Agent", `{"prompt":"y","run_in_background":true}`, "Async agent launched. agentId: agent67890")
		s.conv.Busy, s.conv.Thinking = true, true
		s.lastAct = time.Now().Add(-12 * time.Second)
		s.turnStart = time.Now().Add(-45 * time.Second)
		h.d.updateChrome()
	})
	snap("main-thinking-subagents.png")
	h.ui(func() {
		s.conv.Thinking = false
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Streaming: true, Text: "Here is the first part of the answer"})
		s.lastAct = time.Now()
		s.turnStart = time.Now()
		h.d.updateChrome()
	})
	snap("main-writing.png")
	h.ui(func() { s.conv.Items[len(s.conv.Items)-1].Streaming = false; s.conv.Busy = false })

	w := &kmemWindow{}
	h.ui(func() { h.d.kmemWin = w; h.d.kmemAssistOpen(w) })
	h.eventually("the kmem chat", func() bool {
		return rec.chat != nil && h.d.assist().view != nil && h.d.assist().view.live.Text == "Ready"
	})
	h.ui(func() {
		kw := test.NewWindow(rec.chat)
		kw.Resize(fyne.NewSize(700, 600))
		time.Sleep(100 * time.Millisecond)
		shot(t, kw, dir, "kmem-ready.png")
		a := h.d.assist()
		a.s.conv.Busy, a.s.conv.Thinking = true, true
		h.d.kmemAssistRefresh(a)
		shot(t, kw, dir, "kmem-thinking.png")
		a.s.conv.Busy, a.s.conv.Thinking = false, false
		kw.Close()
	})
}
