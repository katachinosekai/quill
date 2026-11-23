package main

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

func itemTexts(s *deskSession) string {
	var b strings.Builder
	for _, it := range s.conv.Items {
		b.WriteString(it.Text + "\n")
	}
	return b.String()
}

// openThenClose opens editsID, waits for its history and closes its tab.
func openThenClose(t *testing.T, h *harness) (before string, n int) {
	h.ui(func() { h.d.open(editsID) })
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	h.ui(func() {
		s := h.d.sessions[editsID]
		before, n = itemTexts(s), len(s.conv.Items)
		if n == 0 {
			t.Fatal("fixture has no items")
		}
		h.d.closeTab(editsID)
		h.d.createSession(h.proj, "default", "default") // something else on screen
	})
	return
}

func TestIdleUnloadAndReload(t *testing.T) {
	h := newHarness(t)
	before, n := openThenClose(t, h)
	h.ui(func() {
		s := h.d.sessions[editsID]
		now := time.Now()
		if h.d.unloadIdle(now.Add(idleUnloadAfter/2)) != 0 || !s.loaded {
			t.Fatal("unloaded before the idle time")
		}
		if h.d.unloadIdle(now.Add(idleUnloadAfter+time.Minute)) != 1 || s.loaded || len(s.conv.Items) != 0 {
			t.Fatal("an idle session without a tab keeps its items")
		}
		if _, ok := h.d.infos[editsID]; !ok || h.d.infos[editsID].Last.IsZero() {
			t.Fatal("the scan data (title, last time) must stay")
		}
		if title, _, _ := h.d.rowText(editsID); title == "" {
			t.Fatal("no title for an unloaded session")
		}
		h.d.open(editsID)
	})
	h.eventually("reloaded", func() bool { return h.d.sessions[editsID].loaded })
	h.ui(func() {
		s := h.d.sessions[editsID]
		if len(s.conv.Items) != n || itemTexts(s) != before {
			t.Fatalf("reload gave %d items, want %d", len(s.conv.Items), n)
		}
	})
}

func TestIdleUnloadKeepsBusyAndShown(t *testing.T) {
	h := newHarness(t)
	openThenClose(t, h)
	late := time.Now().Add(time.Hour)
	h.ui(func() {
		s := h.d.sessions[editsID]
		// A tab open: never.
		h.d.tabs = append(h.d.tabs, editsID)
		h.d.unloadIdle(time.Now())
		if h.d.unloadIdle(late) != 0 || !s.loaded {
			t.Fatal("a session with a tab was unloaded")
		}
		h.d.tabs = nil
		// The idle clock restarted by the tab: not yet.
		if h.d.unloadIdle(late.Add(time.Minute)) != 0 {
			t.Fatal("the idle time counts from the tab closing")
		}
		s.touched = time.Now()
		// A background subagent.
		s.conv.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant", Content: []byte(`[{"type":"tool_use","id":"ag1","name":"Agent","input":{"description":"Check docs","prompt":"look","run_in_background":true}}]`)}})
		s.conv.Apply(&protocol.Event{Type: "user", Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"tool_result","tool_use_id":"ag1","content":"Async agent launched successfully. agentId: a1b2c3d4e5"}]`)}})
		if s.conv.Background() != 1 {
			t.Fatal("subagent not tracked")
		}
		if h.d.unloadIdle(late) != 0 || !s.loaded {
			t.Fatal("a session with a subagent was unloaded")
		}
		s.conv.ClearBackground()
		// A pending turn.
		s.conv.Busy = true
		if h.d.unloadIdle(late) != 0 || !s.loaded {
			t.Fatal("a session with a pending turn was unloaded")
		}
		s.conv.Busy = false
		s.queue = []string{"later"}
		if h.d.unloadIdle(late) != 0 || !s.loaded {
			t.Fatal("a session with queued messages was unloaded")
		}
		s.queue = nil
		if h.d.unloadIdle(late.Add(idleUnloadAfter + time.Minute)) != 1 {
			t.Fatal("not unloaded once idle")
		}
	})
}

func TestIdleUnloadKeepsRunning(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("slow")
	})
	h.eventually("streaming text", func() bool {
		it := lastItem(s)
		return it != nil && it.Kind == chat.Assistant && it.Streaming
	})
	h.ui(func() {
		h.d.tabs = nil
		h.d.unloadIdle(time.Now())
		if h.d.unloadIdle(time.Now().Add(time.Hour)) != 0 || !s.loaded {
			t.Fatal("a running session was unloaded")
		}
	})
}

func TestPhoneReadReloadsAndResetsTracker(t *testing.T) {
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	h := newHarness(t)
	var link string
	h.ui(func() {
		h.d.app.Preferences().SetInt("phonePort", 0)
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
		link = h.d.pairLink()
	})
	defer h.ui(h.d.stopPhone)
	u, _ := url.Parse(link)
	q := u.Query()
	a := unlockedClient(h, &apiClient{t: t, base: "https://127.0.0.1:" + q.Get("p"), key: q.Get("k"),
		c: &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}})
	items := func() (float64, float64, int) {
		_, m, _ := a.do("GET", "/phone/session/"+editsID+"/items", "", nil)
		return m["epoch"].(float64), m["rev"].(float64), len(m["items"].([]any))
	}
	ep1, rev1, n1 := items() // loads it for the phone
	if n1 == 0 {
		t.Fatal("no items")
	}
	h.ui(func() {
		s := h.d.sessions[editsID]
		if !s.loaded {
			t.Fatal("a phone read loads the session")
		}
		h.d.phone().api.scan()
		if h.d.phone().api.tracks[editsID] == nil {
			t.Fatal("no tracker")
		}
		// Read just now: kept.
		if h.d.unloadIdle(time.Now().Add(idleUnloadAfter/2)) != 0 {
			t.Fatal("unloaded soon after a phone read")
		}
		if h.d.unloadIdle(time.Now().Add(idleUnloadAfter+time.Minute)) != 1 {
			t.Fatal("not unloaded after the phone stopped reading")
		}
		if h.d.phone().api.tracks[editsID] != nil {
			t.Fatal("the tracker of an unloaded session must go")
		}
	})
	trackClock = func() time.Time { return time.Now().Add(time.Minute) }
	t.Cleanup(func() { trackClock = time.Now })
	ep2, rev2, n2 := items()
	if n2 != n1 || ep2 == ep1 || rev2 <= rev1 {
		t.Fatalf("after the reload: items %d->%d epoch %v->%v rev %v->%v", n1, n2, ep1, ep2, rev1, rev2)
	}
}

func TestOSMemoryReturnIsRateLimited(t *testing.T) {
	var calls atomic.Int32
	m := &osMemReturner{gap: 200 * time.Millisecond, free: func() { calls.Add(1) }}
	m.request()
	time.Sleep(30 * time.Millisecond)
	for range 10 {
		m.request()
	}
	time.Sleep(60 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("the first request runs at once: %d", calls.Load())
	}
	time.Sleep(400 * time.Millisecond)
	if calls.Load() != 2 {
		t.Fatalf("the requests in the gap make one call at its end: %d", calls.Load())
	}
	time.Sleep(300 * time.Millisecond)
	if calls.Load() != 2 {
		t.Fatalf("no call without a request: %d", calls.Load())
	}
	m.request()
	time.Sleep(60 * time.Millisecond)
	if calls.Load() != 3 {
		t.Fatalf("a request after the gap runs at once: %d", calls.Load())
	}
}
