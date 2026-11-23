package main

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"github.com/katachinosekai/quill/internal/chat"
)

func tableReply(rows int) string {
	var b strings.Builder
	b.WriteString("| Item | Before | Now | Note |\n|---|---|---|---|\n")
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&b, "| row %d | 7.7 GB | 4.2 GB | a fairly long note that makes this column wide enough to wrap sometimes |\n", i)
	}
	return "Here is the breakdown:\n\n" + b.String() + "\nAnd after:\n\n" + b.String() + "\nDone."
}

func TestScrollUpStaysPutOnRefreshes(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		for i := 0; i < 30; i++ {
			s.conv.AddUser(fmt.Sprintf("message %d with enough words to take some room in the chat", i))
		}
		s.conv.Items = append(s.conv.Items, &chat.Item{Seq: 1000, Kind: chat.Assistant, Text: tableReply(20)})
		v := h.d.chat
		v.show(s)
		if !v.follow || !v.atBottom() {
			t.Fatal("opens at the bottom, following")
		}
		for _, up := range []float32{12, 30, 300} {
			v.scrollBottom()
			v.scroll.Offset = fyne.NewPos(0, v.scroll.Offset.Y-up)
			v.scroll.OnScrolled(v.scroll.Offset)
			y := v.scroll.Offset.Y
			if v.follow {
				t.Fatalf("scrolling up %.0f px should stop following", up)
			}
			v.stick()
			v.stickSoon()
			v.update([]*chat.Item{s.conv.Items[len(s.conv.Items)-1]})
			v.reflow()
			v.stick()
			if v.scroll.Offset.Y != y {
				t.Fatalf("up %.0f: offset moved %.0f -> %.0f", up, y, v.scroll.Offset.Y)
			}
		}
	})
}

func TestHeightChangeAboveViewKeepsAnchor(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		for i := 0; i < 30; i++ {
			s.conv.AddUser(fmt.Sprintf("message %d with enough words to take some room in the chat", i))
		}
		v := h.d.chat
		v.show(s)
		v.scroll.Offset = fyne.NewPos(0, v.scroll.Offset.Y-200)
		v.scroll.OnScrolled(v.scroll.Offset)
		anchor := v.views[s.conv.Items[20].Seq]
		before := anchor.holder.Position().Y - v.scroll.Offset.Y
		first := v.views[s.conv.Items[2].Seq]
		first.holder.Objects = nil
		first.holder.Add(blockOf(300))
		first.holder.Refresh()
		v.settleHeight(first)
		after := anchor.holder.Position().Y - v.scroll.Offset.Y
		if d := after - before; d > 2 || d < -2 {
			t.Fatalf("view jumped by %.0f when content above grew", d)
		}
	})
}

func blockOf(h float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(10, h))
	return r
}
