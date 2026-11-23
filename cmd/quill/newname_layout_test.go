package main

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

// TestNewNameDialogLayout: the "Name this session" dialog shows its entry,
// hint and (when kmem is available) the Memory label and select, each sized,
// inside the card and not drawn over one another.
func TestNewNameDialogLayout(t *testing.T) {
	for _, tc := range []struct {
		name string
		kmem bool
	}{
		{"no-kmem", false},
		{"kmem", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.d.kmemOff = !tc.kmem
			if tc.kmem {
				h.d.kmemRun = func(prog string, args ...string) (string, error) {
					if prog == "kmem" && len(args) >= 2 && args[0] == "handoff" && args[1] == "list" {
						return "alpha-notes  3 items, saved 2026-01-02T03:04:05Z\nbeta-notes  1 item, saved 2026-01-03T03:04:05Z\ngamma-notes  2 items, saved 2026-01-04T03:04:05Z\n", nil
					}
					return "", errors.New("unexpected")
				}
			}
			h.ui(func() {
				h.w.Resize(fyne.NewSize(1280, 860))
				h.d.askNewName(&deskSession{id: "layout-test"}, "fallback-name")
			})
			var entry *nameEntry
			var sel *widget.Select
			var hint, memory *canvas.Text
			var card fyne.CanvasObject
			h.eventually("dialog content", func() bool {
				entry, sel, hint, memory, card = nil, nil, nil, nil, nil
				ov := h.w.Canvas().Overlays().Top()
				if ov == nil {
					return false
				}
				deepWalk(ov, func(o fyne.CanvasObject) {
					switch x := o.(type) {
					case *nameEntry:
						entry = x
					case *widget.Select:
						sel = x
					case *canvas.Text:
						if strings.HasPrefix(x.Text, "Enter to keep it") {
							hint = x
						} else if x.Text == "Memory" {
							memory = x
						}
					}
				})
				if entry == nil || hint == nil {
					return false
				}
				if tc.kmem && (sel == nil || memory == nil || len(sel.Options) < 4) {
					return false
				}
				card = ov
				return true
			})
			drv := fyne.CurrentApp().Driver()
			h.ui(func() {
				if dir := os.Getenv("QUILL_SHOT_DIR"); dir != "" {
					if f, err := os.Create(filepath.Join(dir, "newname-"+tc.name+".png")); err == nil {
						png.Encode(f, h.w.Canvas().Capture())
						f.Close()
					}
				}
				type box struct {
					name   string
					o      fyne.CanvasObject
					p0, p1 fyne.Position
				}
				var boxes []box
				add := func(n string, o fyne.CanvasObject) {
					p := drv.AbsolutePositionForObject(o)
					boxes = append(boxes, box{n, o, p, p.Add(o.Size())})
				}
				add("entry", entry)
				add("hint", hint)
				if tc.kmem {
					add("memory label", memory)
					add("select", sel)
				} else if sel != nil || memory != nil {
					t.Fatalf("kmem unavailable but memory row shown")
				}
				// The card is the visible background rectangle's parent: the
				// largest object under the overlay.
				var c0, c1 fyne.Position
				deepWalk(card, func(o fyne.CanvasObject) {
					if r, ok := o.(*canvas.Rectangle); ok && r.CornerRadius == 14 {
						c0 = drv.AbsolutePositionForObject(r)
						c1 = c0.Add(r.Size())
					}
				})
				if c1.X-c0.X < 100 || c1.Y-c0.Y < 50 {
					t.Fatalf("card not found or tiny: %v-%v", c0, c1)
				}
				for i, b := range boxes {
					if !b.o.Visible() || b.o.Size().Width < 20 || b.o.Size().Height < 8 {
						t.Fatalf("%s size %v visible %v", b.name, b.o.Size(), b.o.Visible())
					}
					if b.p0.X < c0.X || b.p0.Y < c0.Y || b.p1.X > c1.X || b.p1.Y > c1.Y {
						t.Fatalf("%s at %v-%v outside the card %v-%v", b.name, b.p0, b.p1, c0, c1)
					}
					for _, o := range boxes[i+1:] {
						if b.p0.Y < o.p1.Y && o.p0.Y < b.p1.Y && b.p0.X < o.p1.X && o.p0.X < b.p1.X {
							t.Fatalf("%s %v-%v overlaps %s %v-%v", b.name, b.p0, b.p1, o.name, o.p0, o.p1)
						}
					}
				}
				if entry.Size().Height < entry.MinSize().Height-0.5 {
					t.Fatalf("entry squeezed to %v (min %v)", entry.Size(), entry.MinSize())
				}
			})
		})
	}
}
