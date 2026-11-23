package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
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
		if !h.d.agentStrip.root.Visible() || len(got) != 2 || got[0] != "● Fix join stall (Explore) · <1 min" || got[1] != "● Release build · <1 min" {
			t.Fatalf("running subagents listed, shells not: %q", got)
		}
		// Tokens follow task_progress; the time follows the clock.
		h.d.agentStrip.now = func() time.Time { return start.Add(6*time.Minute + 10*time.Second) }
		feed(t, h, s, `{"type":"system","subtype":"task_progress","task_id":"agentA","tool_use_id":"toolu_A","description":"Fix join stall","subagent_type":"Explore","usage":{"total_tokens":41000,"tool_uses":7,"duration_ms":360000},"last_tool_name":"Grep"}`)
		h.d.agentStrip.sync()
		if got := stripTexts(h.d.agentStrip); got[0] != "● Fix join stall (Explore) · 41.0k tok · 6 min" {
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
		if got := stripTexts(h.d.agentStrip); !h.d.agentStrip.root.Visible() || !strings.HasPrefix(got[0], "✓ ") {
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
