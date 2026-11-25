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
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
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
			t.Fatalf("running subagents listed (a CLI-only shell task is not a tracked job): %q", got)
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
	chip.host = h.d.agentStrip.tips
	h.ui(func() {
		h.d.agentStrip.row.Objects = []fyne.CanvasObject{chip}
		chip.MouseIn(nil)
	})
	if chip.host.pop == nil {
		t.Skip("no canvas for the chip in this harness")
	}
	pop, size := chip.host.pop, chip.host.pop.MinSize()
	h.ui(func() {
		g.Tokens, g.LastTool = 123456, "Edit"
		cur = &g
		h.d.agentStrip.sync()
	})
	if chip.host.pop != pop || chip.host.pop.MinSize() != size {
		t.Fatalf("tooltip rebuilt or resized: %v -> %v", size, chip.host.pop.MinSize())
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
	host := newAgentTipHost(h.d.tipLayer)
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
		h.d.agentStrip.root.Show() // hit-testing needs a visible strip
		relayoutTree(h.w.Content())
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
		pos, sz := fyne.CurrentApp().Driver().AbsolutePositionForObject(host.pop), host.pop.Size()
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

// The real cause of the open/close loop: with the tooltip in a Fyne overlay,
// hover goes to the overlay only, so the chip and the tooltip never saw the
// pointer. Now the tooltip is a plain layer object; moving the pointer along
// the chip, through the gap and along the tooltip opens it once and closes it
// once, after the pointer leaves both.
func TestAgentTipPointerPath(t *testing.T) {
	h := newHarness(t)
	a, _, host := hoverChips(h)
	var opens, closes int
	var last *tipHover
	check := func() {
		h.ui(func() {
			if host.pop != last {
				if last != nil {
					closes++
				}
				if host.pop != nil {
					opens++
				}
				last = host.pop
			}
		})
	}
	cv := h.w.Canvas()
	var chipPos fyne.Position
	var csz fyne.Size
	h.ui(func() {
		chipPos = fyne.CurrentApp().Driver().AbsolutePositionForObject(a)
		csz = a.Size()
	})
	if csz.Width == 0 {
		t.Skip("chip not laid out in this harness")
	}
	move := func(p fyne.Position) {
		h.ui(func() { test.MoveMouse(cv, p) })
		check()
	}
	y := chipPos.Y + csz.Height/2
	// along the chip
	for x := chipPos.X + 2; x < chipPos.X+csz.Width-2; x += 3 {
		move(fyne.NewPos(x, y))
	}
	if opens != 1 || closes != 0 {
		t.Fatalf("on the chip: %d opens, %d closes", opens, closes)
	}
	if last == nil {
		t.Fatal("tooltip not open on the chip")
	}
	var pos fyne.Position
	var size fyne.Size
	h.ui(func() {
		pos = fyne.CurrentApp().Driver().AbsolutePositionForObject(host.pop)
		size = host.pop.Size()
	})
	// down through the gap (if any) and along the tooltip, slower than the grace is long in total
	for yy := y; yy < pos.Y+size.Height-2; yy += 2 {
		move(fyne.NewPos(chipPos.X+csz.Width/2, yy))
	}
	for x := pos.X + 2; x < pos.X+size.Width-2; x += 4 {
		move(fyne.NewPos(x, pos.Y+size.Height/2))
		time.Sleep(2 * time.Millisecond)
	}
	// resting on the tooltip longer than the grace keeps it open
	time.Sleep(2 * host.grace)
	check()
	if opens != 1 || closes != 0 {
		t.Fatalf("on the tooltip: %d opens, %d closes", opens, closes)
	}
	// away from both: one close
	move(fyne.NewPos(cv.Size().Width-2, cv.Size().Height-2))
	time.Sleep(3 * host.grace)
	check()
	if opens != 1 || closes != 1 {
		t.Fatalf("after leaving: %d opens, %d closes", opens, closes)
	}
}

// A background shell job (a long test run) shows in the strip after the
// agents, with its label; a tap opens a menu with the tasks dialog, a
// right-click offers Stop everything only for a running local session.
func TestAgentStripShellJob(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.current = s
		s.conv.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant", Content: []byte(`[{"type":"tool_use","id":"bg1","name":"Bash","input":{"command":"go test ./...","run_in_background":true}}]`)}})
		s.conv.Apply(&protocol.Event{Type: "user", Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"tool_result","tool_use_id":"bg1","content":"Command running in background with ID: job1. Output is being written to: /tmp/job1.output"}]`)}})
		h.d.agentStrip.sync()
		got := stripTexts(h.d.agentStrip)
		if !h.d.agentStrip.root.Visible() || len(got) != 1 || got[0] != "● go test ./..." {
			t.Fatalf("the strip lists the shell job: %q", got)
		}
		c := h.d.agentStrip.row.Objects[0].(*agentChip)
		if tip := c.tip(); !strings.Contains(tip, "job1") || !strings.Contains(tip, "/tmp/job1.output") {
			t.Fatalf("tooltip: %q", tip)
		}
		labels := menuLabels(h.d.agentStrip.shellItems(s, s.conv.BackgroundTasks()[0]))
		if len(labels) < 2 || labels[0] != "Open log" || labels[1] != "Show background tasks..." {
			t.Fatalf("job menu: %v", labels)
		}
		// Not running: no Stop everything anywhere.
		for _, l := range menuLabels(c.onMenu()) {
			if l == stopEverythingLabel && !s.running() {
				t.Fatalf("Stop everything offered for a session that is not running")
			}
		}
		// The job ends: the strip hides again.
		s.conv.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant", Content: []byte(`[{"type":"tool_use","id":"stop1","name":"TaskStop","input":{"task_id":"job1"}}]`)}})
		s.conv.Apply(&protocol.Event{Type: "user", Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"tool_result","tool_use_id":"stop1","content":"stopped"}]`)}})
		h.d.agentStrip.sync()
		if h.d.agentStrip.root.Visible() {
			t.Fatalf("hidden once the job ended: %q", stripTexts(h.d.agentStrip))
		}
	})
}

func TestPortraitSessionSubagentsAndActiveOperator(t *testing.T) {
	h := newDeskHarness(t, false)
	h.ui(func() {
		d := h.d
		claude := d.addSession(h.proj, "default", "default", false)
		codex := d.addSessionWithBackend(h.proj, "default", "default", true, true)
		d.open(claude.id)
		feed(t, h, claude, `{"type":"system","subtype":"task_started","task_id":"claude-worker","description":"Claude worker","task_type":"local_agent"}`)
		feed(t, h, codex, `{"type":"system","subtype":"task_progress","task_id":"codex-worker","description":"Codex worker","task_type":"local_agent"}`)
		d.setLayoutMode(layoutVertical)
		d.rebuildRows()

		parents := func(id string) []sideRow {
			t.Helper()
			for i, row := range d.rows {
				if row.id == id {
					var out []sideRow
					for _, child := range d.rows[i+1:] {
						if child.agentSession != id {
							break
						}
						out = append(out, child)
					}
					return out
				}
			}
			t.Fatalf("no parent row for %s", id)
			return nil
		}
		if len(parents(claude.id)) != 0 || len(parents(codex.id)) != 0 {
			t.Fatal("subagent rows start collapsed")
		}
		var toggle *widget.Button
		for _, item := range d.sideItems {
			if row, ok := item.row(); ok && row.id == claude.id && item.agentToggle.Visible() {
				toggle = item.agentToggle
				break
			}
		}
		if toggle == nil {
			t.Fatal("the active Portrait parent should have a visible expansion control")
		}
		test.Tap(toggle)
		if rows := parents(claude.id); len(rows) != 1 || rows[0].agent.Name != "Claude worker" || len(parents(codex.id)) != 0 {
			t.Fatalf("expanding one session should show only its own worker: %+v", rows)
		}
		d.toggleSidebarAgents(codex.id)
		feed(t, h, codex, `{"type":"system","subtype":"task_notification","task_id":"codex-worker","status":"completed"}`)
		d.syncSidebarAgents()
		if rows := parents(codex.id); len(rows) != 1 || rows[0].agent.Status != "completed" {
			t.Fatalf("completed workers should remain under the correct parent: %+v", rows)
		}
		d.setLayoutMode(layoutHorizontal)
		if len(parents(claude.id)) != 0 || len(parents(codex.id)) != 0 {
			t.Fatal("Landscape should use the strip")
		}

		a := d.codexAssistant(codexQuillRole)
		op := d.addSessionWithBackend(h.proj, "default", "default", false, true)
		d.markHiddenAs(op, roleCodexQuill)
		a.s = op
		feed(t, h, op, `{"type":"system","subtype":"task_progress","task_id":"operator-worker","description":"Operator worker","task_type":"local_agent"}`)
		d.openAssistBox(roleCodexQuill)
		d.agentStrip.sync()
		if got := stripTexts(d.agentStrip); len(got) != 1 || !strings.Contains(got[0], "Operator worker") {
			t.Fatalf("the active operator should own the strip: %v", got)
		}
		d.closeAssistBox()
		d.agentStrip.sync()
		if got := stripTexts(d.agentStrip); len(got) != 1 || !strings.Contains(got[0], "Claude worker") {
			t.Fatalf("closing the operator should restore the tab strip: %v", got)
		}
	})
}
