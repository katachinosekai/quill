package main

import (
	"fmt"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
)

// overrideBenchMessages returns n placeholder chat messages of mixed length.
func overrideBenchMessages(n int) []string {
	msgs := make([]string, n)
	for i := range msgs {
		var b strings.Builder
		fmt.Fprintf(&b, "Message %d explains a **change** with `inline code` and a few plain sentences. ", i)
		b.WriteString(strings.Repeat("The text wraps over several lines in the chat column, like a normal reply. ", 1+i%5))
		if i%3 == 0 {
			b.WriteString("\n\n- one item\n- another item with `code`\n")
		}
		msgs[i] = b.String()
	}
	return msgs
}

// overrideBenchChat lays out n messages, each in its own msgText override
// like the chat view, plus an empty holder for a streaming reply.
func overrideBenchChat(b *testing.B, n int) (fyne.Window, *fyne.Container, *chatView) {
	a := test.NewTempApp(b)
	a.Settings().SetTheme(deskTheme{})
	v := &chatView{d: &desk{}}
	box := container.NewVBox()
	for _, m := range overrideBenchMessages(n) {
		box.Add(msgText(v.markdown(m)))
	}
	live := container.NewStack()
	box.Add(live)
	w := test.NewWindow(container.NewVScroll(box))
	w.Resize(fyne.NewSize(900, 1400))
	b.Cleanup(w.Close)
	return w, live, v
}

// BenchmarkOverrideRedraw measures what a theme override costs per chat
// redraw (bundled Fyne cleared its SVG and text-size caches on every
// override apply, i.e. on every message redraw):
//   - open: build and lay out N messages, each in its own msgText override;
//   - stream: replace the streaming reply's msgText with a longer one under
//     N laid-out messages and lay the chat out again;
//   - stream+paint: the same plus painting the window (software painter).
func BenchmarkOverrideRedraw(b *testing.B) {
	const n = 40
	reply := themeLeakReply()[:4000]
	b.Run("open", func(b *testing.B) {
		w, _, _ := overrideBenchChat(b, 0)
		v := &chatView{d: &desk{}}
		msgs := overrideBenchMessages(n)
		b.ResetTimer()
		for range b.N {
			box := container.NewVBox()
			for _, m := range msgs {
				box.Add(msgText(v.markdown(m)))
			}
			w.SetContent(container.NewVScroll(box))
		}
	})
	stream := func(b *testing.B, paint bool) {
		w, live, v := overrideBenchChat(b, n)
		c := w.Canvas().(test.WindowlessCanvas)
		step := 0
		b.ResetTimer()
		for range b.N {
			step = step%40 + 1
			live.Objects = []fyne.CanvasObject{msgText(v.markdown(reply[:step*len(reply)/40]))}
			live.Refresh()
			w.Content().Refresh()
			if paint {
				c.Capture()
			}
		}
	}
	b.Run("stream", func(b *testing.B) { stream(b, false) })
	b.Run("stream+paint", func(b *testing.B) { stream(b, true) })
}
