package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
)

// blockExtents lays the plan card box out in a window w x h and returns, for
// each direct child, its laid-out top, its laid-out bottom, its MinSize
// height and the lowest bottom of any text drawn inside it.
type blockExtent struct{ top, bottom, min, ink float32 }

func blockExtents(t *testing.T, win fyne.Window, o fyne.CanvasObject, w, h float32) []blockExtent {
	t.Helper()
	box, ok := o.(*fyne.Container)
	if !ok {
		t.Fatalf("plan card is %T, want a VBox", o)
	}
	win.SetContent(container.NewVScroll(box))
	win.Resize(fyne.NewSize(w, h))
	var out []blockExtent
	for _, c := range box.Objects {
		if !c.Visible() {
			continue
		}
		top := fyne.CurrentApp().Driver().AbsolutePositionForObject(c).Y
		e := blockExtent{top: top, bottom: top + c.Size().Height, min: c.MinSize().Height, ink: top}
		deepWalk(c, func(x fyne.CanvasObject) {
			if tx, ok := x.(*canvas.Text); ok && strings.TrimSpace(tx.Text) != "" {
				if b := fyne.CurrentApp().Driver().AbsolutePositionForObject(x).Y + x.Size().Height; b > e.ink {
					e.ink = b
				}
			}
		})
		out = append(out, e)
	}
	return out
}

// No block of the plan card draws over the next one at any chat width.
func TestPlanCardBlocksDoNotOverlap(t *testing.T) {
	long := "Plan:\n\n" +
		"1. Rebuild Quill Desktop from master `25309c0` and live-reload it, then check that the new process runs the new commit and that the old one has exited cleanly.\n" +
		"2. Add this case to gate bug task t4b7838, with the exact refused command quoted in the task text so the next session can reproduce the refusal.\n" +
		"3. Run the full suite with the wayland tag and report every failing test by name, one line each.\n" +
		"4. Short.\n\n" +
		"**Can I run steps 1–4? Yes (recommended) or no?**"
	for name, text := range map[string]string{"exact": stepsPlanText, "long": long} {
		for _, sz := range [][2]float32{{600, 700}, {900, 900}, {1280, 860}, {1720, 900}} {
			t.Run(fmt.Sprintf("%s/%.0fx%.0f", name, sz[0], sz[1]), func(t *testing.T) {
				h := newHarness(t)
				s, plan := planCardSession(t, h, text)
				h.ui(func() {
					o := h.d.chat.renderPlanReply(planReply(s.conv, plan))
					bs := blockExtents(t, h.w, o, sz[0], sz[1])
					if dir := os.Getenv("QUILL_SHOT_DIR"); dir != "" {
						// The harness window, as the author's screenshot was taken.
						h.w.SetContent(container.NewVScroll(o))
						h.w.Resize(fyne.NewSize(sz[0], sz[1]))
						if f, err := os.Create(filepath.Join(dir, fmt.Sprintf("overlap-%s-%.0f.png", name, sz[0]))); err == nil {
							png.Encode(f, h.w.Canvas().Capture())
							f.Close()
						}
					}
					for i, b := range bs {
						t.Logf("block %d top %.1f bottom %.1f min %.1f ink-bottom %.1f", i, b.top, b.bottom, b.min, b.ink)
						if b.ink > b.bottom+0.5 {
							t.Errorf("block %d draws to %.1f, below its own bottom %.1f", i, b.ink, b.bottom)
						}
						if i+1 < len(bs) && b.bottom > bs[i+1].top+0.5 {
							t.Errorf("block %d bottom %.1f is below block %d top %.1f", i, b.bottom, i+1, bs[i+1].top)
						}
					}
				})
			})
		}
	}
}
