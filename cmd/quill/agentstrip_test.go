package main

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// stripTexts: the chip texts the strip shows.
func stripTexts(a *agentStrip) []string {
	var out []string
	for _, o := range a.row.Objects {
		if c, ok := o.(*agentChip); ok {
			out = append(out, c.text.Text)
		}
	}
	return out
}

// shownOnCanvas: o and every container above it in the header are visible.
func shownOnCanvas(d *desk, o fyne.CanvasObject) bool {
	found := false
	var walkUp func(c fyne.CanvasObject) bool
	walkUp = func(c fyne.CanvasObject) bool {
		if c == o {
			found = true
			return c.Visible()
		}
		if !c.Visible() {
			return false
		}
		if box, ok := c.(*fyne.Container); ok {
			for _, ch := range box.Objects {
				if walkUp(ch) {
					return true
				}
			}
		}
		return false
	}
	return walkUp(d.headerBox) && found
}

// The event shapes are the CLI's; ids and descriptions are placeholders.
func TestAgentStrip(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	start := time.Now()
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.current = s
		h.d.agentStrip.now = func() time.Time { return start }
		h.d.agentStrip.sync()
		if h.d.agentStrip.root.Visible() {
			t.Fatal("hidden while no subagent runs")
		}
		feed(t, h, s, `{"type":"system","subtype":"task_started","task_id":"agentA","tool_use_id":"toolu_A","description":"Fix join stall","subagent_type":"Explore","task_type":"local_agent","is_backgrounded":true}`)
		feed(t, h, s, `{"type":"system","subtype":"task_started","task_id":"agentB","tool_use_id":"toolu_B","description":"Release build","subagent_type":"general-purpose","task_type":"local_agent","is_backgrounded":true}`)
		feed(t, h, s, `{"type":"system","subtype":"task_started","task_id":"sh1","tool_use_id":"toolu_S","description":"go test","task_type":"local_bash"}`)
		h.d.agentStrip.sync()
		got := stripTexts(h.d.agentStrip)
		if !h.d.agentStrip.root.Visible() || len(got) != 2 || got[0] != "● Fix join stall" || got[1] != "● Release build" {
			t.Fatalf("running subagents listed, shells not: %q", got)
		}
		// Tokens follow task_progress; the time follows the clock.
		h.d.agentStrip.now = func() time.Time { return start.Add(6*time.Minute + 10*time.Second) }
		feed(t, h, s, `{"type":"system","subtype":"task_progress","task_id":"agentA","tool_use_id":"toolu_A","description":"Fix join stall","subagent_type":"Explore","usage":{"total_tokens":41000,"tool_uses":7,"duration_ms":360000},"last_tool_name":"Grep"}`)
		h.d.agentStrip.sync()
		if got := stripTexts(h.d.agentStrip); got[0] != "● Fix join stall" {
			t.Fatalf("tokens update from events: %q", got)
		}
		items := h.d.agentStrip.items(s, s.conv.Agents()[0])
		var labels []string
		for _, it := range items {
			labels = append(labels, it.Label)
		}
		if !strings.Contains(strings.Join(labels, "|"), "41.0k tokens · 7 tool uses · last Grep") || items[len(items)-1].Label != "Stop this subagent" {
			t.Fatalf("menu: %q", labels)
		}
	})
	// Both layouts show it.
	for _, mode := range []string{layoutHorizontal, layoutVertical} {
		h.ui(func() {
			h.d.setLayoutMode(mode)
			if !shownOnCanvas(h.d, h.d.agentStrip.root) {
				t.Fatalf("%s layout: the strip is not shown", mode)
			}
		})
		if os.Getenv("DESK_SHOTS") != "" {
			dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
			os.MkdirAll(dir, 0o755)
			h.ui(func() { shot(t, h.w, dir, "agent-strip-"+mode+".png") })
		}
	}
	h.ui(func() {
		h.d.setLayoutMode(layoutHorizontal)
		// More than four: three chips and "+N".
		for _, id := range []string{"agentC", "agentD", "agentE"} {
			feed(t, h, s, `{"type":"system","subtype":"task_started","task_id":"`+id+`","description":"part `+id+`","task_type":"local_agent"}`)
		}
		h.d.agentStrip.sync()
		if got := stripTexts(h.d.agentStrip); len(got) != 4 || got[3] != "+2" {
			t.Fatalf("overflow: %q", got)
		}
		// Finished ones grey out, then the strip hides.
		h.d.agentStrip.now = time.Now
		for _, id := range []string{"agentA", "agentB", "agentC", "agentD", "agentE"} {
			feed(t, h, s, `{"type":"system","subtype":"task_notification","task_id":"`+id+`","status":"completed","usage":{"total_tokens":52000}}`)
		}
		h.d.agentStrip.sync()
		if got := stripTexts(h.d.agentStrip); !h.d.agentStrip.root.Visible() || len(got) == 0 {
			t.Fatalf("finished ones shown briefly: %q", got)
		}
		h.d.agentStrip.now = func() time.Time { return time.Now().Add(5 * time.Minute) }
		h.d.agentStrip.sync()
		if h.d.agentStrip.root.Visible() {
			t.Fatalf("hidden once none runs: %q", stripTexts(h.d.agentStrip))
		}
	})
}

// The strip sits in the chat column: centred with centring on, on the chat's
// left edge otherwise, never wider than the chat width.
func TestAgentStripAlignment(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		a := h.d.agentStrip
		a.row.Objects = []fyne.CanvasObject{widget.NewLabel("chip")}
		colW := float32(500)
		a.col.Layout = maxWidthLayout{width: func() float32 { return colW }}
		a.root.Show()
		a.root.Resize(fyne.NewSize(1400, 30))
		a.root.Refresh()
		inner := a.col.Objects[0]
		colX := (1400 - 16 - colW) / 2
		rowX := func() float32 { return a.row.Position().X }
		h.d.center = false
		a.root.Refresh()
		a.col.Layout.Layout(a.col.Objects, a.col.Size())
		inner.(*fyne.Container).Layout.Layout(inner.(*fyne.Container).Objects, inner.Size())
		if got := inner.Position().X; got < colX-1 || got > colX+1 || rowX() > theme.Padding()+0.5 {
			t.Fatalf("left: column at %v (want %v), row at %v", got, colX, rowX())
		}
		h.d.center = true
		inner.(*fyne.Container).Layout.Layout(inner.(*fyne.Container).Objects, inner.Size())
		if rowX() <= theme.Padding()+1 || inner.Size().Width > colW+0.5 {
			t.Fatalf("centred: row at %v in width %v", rowX(), inner.Size().Width)
		}
	})
}

func TestAgentChipDescription(t *testing.T) {
	now := time.Now()
	g := chat.AgentUse{Name: "Restore plan card overlay (both apps) and more", Type: "general-purpose", Model: "opus", Started: now.Add(-3 * time.Minute), Tokens: 12000, Status: "running"}
	if got := agentChipText(g, now); got != "● Restore plan card overlay (both…" {
		t.Fatalf("truncated chip: %q", got)
	}
	if tip := agentTip(g, now); tip != "Restore plan card overlay (both apps) and more\ngeneral-purpose · opus\nRunning for 3 min · 12.0k tok" {
		t.Fatalf("tooltip: %q", tip)
	}
	g.Name = "  "
	if got := agentChipText(g, now); got != "● general-purpose" || agentTip(g, now) != "general-purpose\ngeneral-purpose · opus\nRunning for 3 min · 12.0k tok" {
		t.Fatalf("type fallback: %q %q", got, agentTip(g, now))
	}
	g.Name, g.Type, g.Model, g.Tokens = "Quill Desktop RAM usage review", "", "", 0
	g.Done, g.Status = now, "completed"
	if got := agentChipText(g, now); got != "✓ Quill Desktop RAM usage review" || agentTip(g, now) != "Quill Desktop RAM usage review\nCompleted after 3 min" {
		t.Fatalf("short: %q %q", got, agentTip(g, now))
	}
	g.Status = "failed"
	if got := agentChipText(g, now); got != "✕ Quill Desktop RAM usage review" {
		t.Fatalf("failed mark: %q", got)
	}
}

// The open tooltip keeps its size while time and tokens change: the text is
// updated in place, the popup is neither rebuilt nor resized.
func TestAgentTipSteady(t *testing.T) {
	h := newHarness(t)
	start := time.Now()
	g := chat.AgentUse{ID: "a1", Name: "Fix join stall", Type: "Explore", LastTool: "Read", Tokens: 900, Started: start, Status: "running"}
	cur := &g
	chip := newAgentChip("● Fix join stall", colIdle, nil, func() string { return agentTip(*cur, start.Add(90*time.Second)) })
	h.ui(func() {
		h.d.agentStrip.row.Objects = []fyne.CanvasObject{chip}
		chip.MouseIn(nil)
	})
	if chip.host.pop == nil {
		t.Skip("no canvas for the chip in this harness")
	}
	pop, size := chip.host.pop, chip.host.pop.Content.MinSize()
	h.ui(func() {
		g.Tokens, g.LastTool = 123456, "Edit"
		cur = &g
		h.d.agentStrip.sync()
	})
	if chip.host.pop != pop || chip.host.pop.Content.MinSize() != size {
		t.Fatalf("tooltip rebuilt or resized: %v -> %v", size, chip.host.pop.Content.MinSize())
	}
	found := false
	for _, l := range chip.host.lines {
		found = found || l.Text == "now: Edit"
	}
	if !found {
		t.Fatalf("text not updated in place: %v", chip.host.lines)
	}
}

// hoverChips: two chips sharing one tooltip host, placed in the strip row.
func hoverChips(h *harness) (*agentChip, *agentChip, *agentTipHost) {
	host := newAgentTipHost()
	host.do = func(f func()) { f() }
	host.grace = 40 * time.Millisecond
	mk := func(name string) *agentChip {
		c := newAgentChip("● "+name, colIdle, nil, func() string { return name + "\nExplore\nRunning for 1 min" })
		c.host = host
		return c
	}
	a, b := mk("Fix join stall"), mk("Release build")
	h.ui(func() {
		h.d.agentStrip.row.Objects = []fyne.CanvasObject{a, b}
		h.d.agentStrip.row.Refresh()
	})
	return a, b, host
}

// The tooltip hangs just below the chip, centred on it; hovering any edge of
// the chip (not only the text) opens it.
func TestAgentTipAnchoredToChip(t *testing.T) {
	h := newHarness(t)
	a, _, host := hoverChips(h)
	h.ui(func() {
		a.MouseIn(&desktop.MouseEvent{}) // an edge: MouseIn carries no position dependency
		if host.pop == nil {
			t.Skip("no canvas for the chip in this harness")
		}
		p := fyne.CurrentApp().Driver().AbsolutePositionForObject(a)
		pos, sz := host.pop.Position(), host.pop.Size()
		if pos.Y != p.Y+a.Size().Height {
			t.Fatalf("tooltip y %v, chip bottom %v", pos.Y, p.Y+a.Size().Height)
		}
		cx := p.X + a.Size().Width/2
		if pos.X > 0 && absf(pos.X+sz.Width/2-cx) > 1 {
			t.Fatalf("tooltip centre %v, chip centre %v", pos.X+sz.Width/2, cx)
		}
	})
}

func absf(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

// A short MouseOut then MouseIn inside the grace keeps the same popup; a
// MouseOut alone hides it after the grace; the tooltip itself keeps it open.
func TestAgentTipGrace(t *testing.T) {
	h := newHarness(t)
	a, _, host := hoverChips(h)
	h.ui(func() { a.MouseIn(nil) })
	if host.pop == nil {
		t.Skip("no canvas for the chip in this harness")
	}
	pop := host.pop
	h.ui(func() { a.MouseOut(); a.MouseIn(nil) })
	time.Sleep(100 * time.Millisecond)
	h.ui(func() {
		if host.pop != pop {
			t.Fatal("re-entering within the grace rebuilt or hid the tooltip")
		}
		a.MouseOut() // the pointer moves onto the tooltip
		(&tipHover{h: host}).MouseIn(nil)
	})
	time.Sleep(100 * time.Millisecond)
	h.ui(func() {
		if host.pop != pop {
			t.Fatal("tooltip hidden while the pointer is on it")
		}
		(&tipHover{h: host}).MouseOut()
	})
	time.Sleep(150 * time.Millisecond)
	h.ui(func() {
		if host.pop != nil {
			t.Fatal("tooltip still up after the grace")
		}
	})
}

// Moving straight to another chip reuses the popup with the new text.
func TestAgentTipSwitchChips(t *testing.T) {
	h := newHarness(t)
	a, b, host := hoverChips(h)
	h.ui(func() { a.MouseIn(nil) })
	if host.pop == nil {
		t.Skip("no canvas for the chip in this harness")
	}
	pop := host.pop
	h.ui(func() {
		a.MouseOut()
		b.MouseIn(nil)
		if host.pop != pop || host.owner != b || host.lines[0].Text != "Release build" {
			t.Fatalf("switch: same popup %v, owner b %v, text %q", host.pop == pop, host.owner == b, host.lines[0].Text)
		}
		if host.lines[0].Color == color.Transparent {
			t.Fatal("a swapped tooltip must not fade in again")
		}
	})
	time.Sleep(100 * time.Millisecond)
	h.ui(func() {
		if host.pop != pop {
			t.Fatal("the old chip's MouseOut hid the swapped tooltip")
		}
	})
}
