package main

import (
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// TestKmemCardNewTopicVisible: with dozens of topics loaded, the card's
// "New topic…" entry and Open button stay visible, sized, inside the card and
// the chat viewport, and nothing (the topics list) draws over them.
func TestKmemCardNewTopicVisible(t *testing.T) {
	for _, tc := range []struct {
		name   string
		topics int
		win    fyne.Size
	}{
		{"many", 40, fyne.NewSize(1280, 860)},
		{"many-short-window", 40, fyne.NewSize(1100, 700)},
		{"few", 2, fyne.NewSize(1280, 860)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			resetTopicCache()
			t.Cleanup(resetTopicCache)
			h.d.kmemOff = false
			var topics strings.Builder
			for i := range tc.topics {
				fmt.Fprintf(&topics, "topic-%02d/notes  2 facts, 1 KB\n", i)
			}
			h.d.kmemRun = func(prog string, args ...string) (string, error) {
				if prog == "kmem" && len(args) > 0 && args[0] == "topics" {
					return topics.String(), nil
				}
				return "", errors.New("unexpected")
			}
			h.ui(func() {
				h.w.Resize(tc.win)
				h.d.newSessionDialog()
			})
			browseAll(h)
			h.ui(func() { cardSearchEntry(h).SetText("Topic") }) // all of them match; New topic shows
			var entry *widget.Entry
			var open *widget.Button
			var list *container.Scroll
			h.eventually("topic rows", func() bool {
				entry, open, list = nil, nil, nil
				n := 0
				deepWalk(h.d.chat.box, func(o fyne.CanvasObject) {
					switch x := o.(type) {
					case *widget.Entry:
						entry = x
					case *widget.Button:
						if strings.HasPrefix(x.Text, "New topic:") {
							open = x
						} else if strings.HasPrefix(x.Text, "Topic") {
							n++
						}
					case *container.Scroll: // the topics list, not an entry's own scroller
						if c, ok := x.Content.(*fyne.Container); ok && len(c.Objects) > 0 {
							// each row is a Border: the topic button plus its hide button
							if r, ok := c.Objects[0].(*fyne.Container); ok && len(r.Objects) > 0 {
								if b, ok := r.Objects[0].(*widget.Button); ok && strings.HasPrefix(b.Text, "Topic") {
									list = x
								}
							}
						}
					}
				})
				return n == tc.topics && entry != nil && open != nil && list != nil
			})
			drv := fyne.CurrentApp().Driver()
			rect := func(o fyne.CanvasObject) (fyne.Position, fyne.Position) {
				p := drv.AbsolutePositionForObject(o)
				return p, p.Add(o.Size())
			}
			h.ui(func() {
				if dir := os.Getenv("QUILL_SHOT_DIR"); dir != "" {
					img := h.w.Canvas().Capture()
					f, err := os.Create(filepath.Join(dir, "kmemcard-"+tc.name+".png"))
					if err == nil {
						png.Encode(f, img)
						f.Close()
					}
				}
				vp := h.d.chat.scroll
				v0, v1 := rect(vp)
				l0, l1 := rect(list)
				for _, o := range []fyne.CanvasObject{entry, open} {
					o0, o1 := rect(o)
					if !o.Visible() || o.Size().Width < 20 || o.Size().Height < 20 {
						t.Fatalf("%T size %v visible %v", o, o.Size(), o.Visible())
					}
					if o0.Y < v0.Y || o1.Y > v1.Y || o0.X < v0.X || o1.X > v1.X {
						t.Fatalf("%T at %v-%v outside the chat viewport %v-%v", o, o0, o1, v0, v1)
					}
					if o0.Y < l1.Y && l0.Y < o1.Y {
						t.Fatalf("%T at %v-%v overlaps the topics list %v-%v", o, o0, o1, l0, l1)
					}
				}
				// The card is part of the chat column's content, so the column
				// scrolls to its end instead of the card overflowing it.
				b0, _ := rect(h.d.chat.box)
				if bh := h.d.chat.box.MinSize().Height; l1.Y > b0.Y+bh+1 {
					t.Fatalf("topics list ends at %v, past the chat content (top %v, height %v)", l1.Y, b0.Y, bh)
				}
				if list.Size().Height < 40 {
					t.Fatalf("topics list squeezed to %v", list.Size())
				}
			})
		})
	}
}
