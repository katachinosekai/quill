package main

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

// bgTool applies a tool call and its result (a background launch registers a task).
func bgTool(c *chat.Conversation, id, name, input, result string) {
	c.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant", Content: []byte(`[{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}]`)}})
	b, _ := json.Marshal(result)
	c.Apply(&protocol.Event{Type: "user", Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"tool_result","tool_use_id":"` + id + `","content":` + string(b) + `}]`)}})
}

// remoteSession is a running session without a process: its state comes from
// the paired PC's status word.
func remoteSession(status string) *deskSession {
	return &deskSession{conv: chat.New(), remote: &remoteSess{status: status}}
}

func TestLiveStatusTable(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	type row struct {
		name   string
		mk     func() *deskSession
		want   string
		active bool
		busy   bool
	}
	busy := func() *deskSession {
		s := remoteSession("running")
		s.conv.Busy = true
		s.turnStart, s.lastAct = t0, t0
		return s
	}
	rows := []row{
		{"no session", func() *deskSession { return nil }, "", false, false},
		{"no process", func() *deskSession { return &deskSession{conv: chat.New()} }, "Not running", false, false},
		{"failed start", func() *deskSession { return &deskSession{conv: chat.New(), failed: "boom"} }, "Stopped", false, false},
		{"starting", func() *deskSession { return &deskSession{conv: chat.New(), restarting: true} }, "Starting…", true, false},
		{"idle", func() *deskSession { return remoteSession("idle") }, "Ready", false, false},
		{"remote not running", func() *deskSession { return remoteSession("") }, "Not running", false, false},
		{"remote error", func() *deskSession { return remoteSession("error") }, "Stopped", false, false},
		{"interrupted", func() *deskSession {
			s := remoteSession("idle")
			s.conv.AddNotice("Interrupted")
			return s
		}, "Stopped", false, false},
		{"thinking", func() *deskSession {
			s := busy()
			s.conv.Thinking = true
			return s
		}, "✻ Thinking…", true, true},
		{"working", busy, "✻ Working…", true, true},
		{"writing", func() *deskSession {
			s := busy()
			s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Streaming: true, Text: "Hel"})
			return s
		}, "Writing…", true, true},
		{"running tool", func() *deskSession {
			s := busy()
			s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Tool, ToolName: "Bash", Input: json.RawMessage(`{"command":"make test"}`)})
			return s
		}, "● Bash", true, true},
		{"permission", func() *deskSession {
			s := busy()
			s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Permission, PermState: chat.PermPending})
			return s
		}, "Waiting for your answer above", true, false},
		{"plan card", func() *deskSession {
			s := busy()
			s.conv.AddPlan()
			return s
		}, "Waiting for your approval", true, false},
		{"plan wait", func() *deskSession {
			s := remoteSession("idle")
			s.planWait = true
			return s
		}, "Waiting for your approval", true, false},
		{"subagent and job", func() *deskSession {
			s := remoteSession("idle")
			bgTool(s.conv, "a1", "Agent", `{"prompt":"x","run_in_background":true}`, "Async agent launched. agentId: agent12345")
			bgTool(s.conv, "b1", "Bash", `{"command":"./t.sh","run_in_background":true}`, "Command running in background with ID: real12345. Output is being written to: /tmp/o")
			bgTool(s.conv, "b2", "Bash", `{"command":"./u.sh","run_in_background":true}`, "Command running in background with ID: real67890. Output is being written to: /tmp/p")
			return s
		}, "Ready · 1 subagent running · 2 background jobs", false, false},
	}
	for _, r := range rows {
		st := liveStatus(r.mk(), t0)
		if r.name == "running tool" {
			if st.text[:len("● Bash")] != r.want {
				t.Errorf("%s: %q", r.name, st.text)
			}
		} else if st.text != r.want {
			t.Errorf("%s: %q want %q", r.name, st.text, r.want)
		}
		if st.active != r.active || st.busy != r.busy {
			t.Errorf("%s: active %v busy %v want %v %v", r.name, st.active, st.busy, r.active, r.busy)
		}
	}

	// The age suffix shows only while busy, and from a few seconds on.
	s := busy()
	if st := liveStatus(s, t0.Add(2*time.Second)); st.text != "✻ Working…" {
		t.Errorf("fresh activity: %q", st.text)
	}
	if st := liveStatus(s, t0.Add(12*time.Second)); st.text != "✻ Working… · last activity 12 s ago" {
		t.Errorf("12 s: %q", st.text)
	}
	if st := liveStatus(s, t0.Add(150*time.Second)); st.text != "✻ Working… · last activity 2 min ago" {
		t.Errorf("150 s: %q", st.text)
	}
	bgTool(s.conv, "b1", "Bash", `{"command":"./t.sh","run_in_background":true}`, "Command running in background with ID: real12345. Output is being written to: /tmp/o")
	if st := liveStatus(s, t0.Add(12*time.Second)); st.text != "✻ Working… · 1 background job · last activity 12 s ago" {
		t.Errorf("suffix order: %q", st.text)
	}
	s.conv.Busy, s.remote.status = false, "idle"
	if st := liveStatus(s, t0.Add(12*time.Second)); st.text != "Ready · 1 background job" {
		t.Errorf("idle has no age: %q", st.text)
	}
}

func withClock(t *testing.T, now *time.Time) {
	old := statusNow
	statusNow = func() time.Time { return *now }
	t.Cleanup(func() { statusNow = old })
}

func TestStatusPulseAfter30s(t *testing.T) {
	h := newHarness(t)
	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	withClock(t, &now)
	s := remoteSession("running")
	s.conv.Busy = true
	h.ui(func() {
		v := h.d.chat
		v.setStatus(s)
		if v.pulsing || v.live.Text != "✻ Working…" {
			t.Fatalf("fresh turn pulses or shows %q", v.live.Text)
		}
		now = now.Add(29 * time.Second)
		v.setStatus(s)
		if v.pulsing {
			t.Fatal("29 s: no pulse yet")
		}
		now = now.Add(2 * time.Second)
		v.setStatus(s)
		if !v.pulsing || v.pulse == nil {
			t.Fatal("31 s: the text pulses")
		}
		// The text animation settings apply: Instant (or animations off) means no pulse.
		h.d.app.Preferences().SetString(prefAnimateSpeed, "instant")
		v.setStatus(s)
		if v.pulsing {
			t.Fatal("Instant speed: no pulse")
		}
		h.d.app.Preferences().SetString(prefAnimateSpeed, "slow")
		v.setStatus(s)
		if !v.pulsing {
			t.Fatal("Slow speed pulses again")
		}
		h.d.app.Preferences().SetBool(prefAnimateText, false)
		v.setStatus(s)
		if v.pulsing {
			t.Fatal("animations off: no pulse")
		}
		h.d.app.Preferences().SetBool(prefAnimateText, true)
		v.setStatus(s)
		s.conv.Busy, s.remote.status = false, "idle"
		v.setStatus(s)
		if v.pulsing || v.pulse != nil || v.live.Text != "Ready" {
			t.Fatalf("idle: pulse %v text %q", v.pulsing, v.live.Text)
		}
	})
}

func TestStatusTickerStopsWhenIdle(t *testing.T) {
	h := newHarness(t)
	oldEvery := statusEvery
	statusEvery = 15 * time.Millisecond
	defer func() { statusEvery = oldEvery }()
	var reads atomic.Int64
	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	old := statusNow
	statusNow = func() time.Time { reads.Add(1); return now }
	defer func() { statusNow = old }()

	s := remoteSession("running")
	s.conv.Busy = true
	var running bool
	h.ui(func() {
		h.d.chat.s = s
		h.d.chat.setStatus(s)
		running = h.d.statusRun
	})
	if !running {
		t.Fatal("a busy session starts the ticker")
	}
	h.eventually("the ticker redraws the age", func() bool {
		now = now.Add(time.Second)
		return h.d.chat.live.Text != "" && len(h.d.chat.live.Text) > len("✻ Working…")
	})
	h.ui(func() { s.conv.Busy, s.remote.status = false, "idle" })
	h.eventually("the ticker stops", func() bool { return !h.d.statusRun })
	time.Sleep(100 * time.Millisecond)
	n := reads.Load()
	time.Sleep(150 * time.Millisecond)
	if reads.Load() != n {
		t.Fatal("the ticker still runs while idle")
	}
	h.ui(func() {
		if h.d.chat.live.Text != "Ready" {
			t.Fatalf("idle text %q", h.d.chat.live.Text)
		}
	})
	// An idle session never starts it.
	h.ui(func() { h.d.chat.setStatus(s) })
	if h.d.statusRun {
		t.Fatal("idle must not start the ticker")
	}
}

func TestSplitPanesShowStatus(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		a := h.d.createSession(h.proj, "default", "default")
		b := h.d.createSession(h.proj, "default", "default")
		h.d.open(a.id)
		h.d.openInSplit(b.id)
		for i, p := range h.d.panes {
			if p.view.live.Text != "Not running" {
				t.Fatalf("pane %d shows %q", i, p.view.live.Text)
			}
		}
		b.restarting = true
		h.d.refreshStatuses()
		if got := h.d.panes[1].view.live.Text; got != "Starting…" {
			t.Fatalf("the refresh reaches every pane: %q", got)
		}
		b.restarting = false
		b.failed = "x"
		h.d.panes[1].queue(nil)
	})
	h.eventually("the pane's queue path draws it", func() bool { return h.d.panes[1].view.live.Text == "Stopped" })
}

func TestKmemWindowShowsStatus(t *testing.T) {
	h, _, _ := assistSetup(t)
	w := &kmemWindow{}
	s := openAssist(h, w)
	h.eventually("Ready under the kmem chat", func() bool {
		v := h.d.assist().view
		return v != nil && v.live.Text == "Ready"
	})
	h.ui(func() {
		a := h.d.assist()
		s.conv.Busy, s.conv.Thinking = true, true
		h.d.kmemAssistRefresh(a)
		if got := a.view.live.Text; got != "✻ Thinking…" {
			t.Fatalf("kmem window while thinking: %q", got)
		}
		// Its header label would repeat the plain state: hidden (user
		// 2026-11-24); it shows only starting/saving/stopping/failures.
		if !a.stateLbl.Hidden {
			t.Fatalf("kmem chat header shown while thinking: %q", a.stateLbl.Text)
		}
		s.conv.Busy, s.conv.Thinking = false, false
		h.d.kmemAssistRefresh(a)
		if !a.stateLbl.Hidden {
			t.Fatalf("kmem chat header shown idle: %q", a.stateLbl.Text)
		}
		a.state = asStopping
		h.d.kmemAssistRefresh(a)
		if a.stateLbl.Hidden || a.stateLbl.Text != "● stopping" {
			t.Fatalf("kmem chat header while stopping: hidden=%v %q", a.stateLbl.Hidden, a.stateLbl.Text)
		}
		a.state = asLive
		h.d.kmemAssistRefresh(a)
	})
}
