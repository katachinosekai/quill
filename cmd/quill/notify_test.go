package main

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/katachinosekai/quill/internal/folders"
	"github.com/katachinosekai/quill/internal/protocol"
)

func TestDesktopNotifications(t *testing.T) {
	h := newHarness(t)
	var mu sync.Mutex
	var got []string
	h.d.notifySink = func(sid, title, body string) {
		mu.Lock()
		got = append(got, title+" | "+body)
		mu.Unlock()
	}
	has := func(part string) bool {
		mu.Lock()
		defer mu.Unlock()
		for _, g := range got {
			if strings.Contains(g, part) {
				return true
			}
		}
		return false
	}
	var s *deskSession
	h.ui(func() {
		h.d.foreground = false
		s = h.d.createSession(h.proj, "haiku", "default")
		h.d.org.Titles[s.id] = "Launcher"
		h.d.send("demo")
	})
	h.eventually("approval notification", func() bool { return has("Launcher needs approval") })
	h.eventually("allow button", func() bool { return h.button("Allow") != nil })
	h.ui(func() { test.Tap(h.button("Allow")) })
	h.eventually("finished notification", func() bool { return has("Launcher finished | done allow") })

	h.ui(func() {
		mu.Lock()
		got = nil
		mu.Unlock()
		h.d.foreground = true
		h.d.send("demo")
	})
	h.eventually("allow button again", func() bool { return h.button("Allow") != nil })
	h.ui(func() { test.Tap(h.button("Allow")) })
	h.eventually("turn done", func() bool {
		return !s.conv.Busy && s.conv.Items[len(s.conv.Items)-1].Text == "done allow" && len(s.conv.PendingPermissions()) == 0
	})
	if has("Launcher") {
		t.Fatalf("no notifications for the session you're looking at: %v", got)
	}
	h.ui(func() {
		h.d.app.Preferences().SetBool("desktopNotify", false)
		h.d.foreground = false
		h.d.send("demo")
	})
	h.eventually("allow button off", func() bool { return h.button("Allow") != nil })
	if has("Launcher") {
		t.Fatalf("the setting turns notifications off: %v", got)
	}
}

func TestPhoneEvents(t *testing.T) {
	h := newHarness(t)
	cert, _, err := folders.LoadOrCreateCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := folders.Listen(cert, 0, []folders.Phone{{ID: "aaaa0001", KeyHash: folders.HashKey("secret-key-123456")}})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	events := func() []folders.Event {
		req, _ := http.NewRequest("GET", "https://127.0.0.1:"+strconv.Itoa(srv.Port)+"/events?since=0", nil)
		req.Header.Set("X-Quill-Key", "secret-key-123456")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var p folders.EventPage
		json.NewDecoder(resp.Body).Decode(&p)
		return p.Events
	}
	var s *deskSession
	h.ui(func() {
		h.d.phone().server = srv
		h.d.foreground = true
		s = h.d.createSession(h.proj, "haiku", "default")
		h.d.org.Titles[s.id] = "Launcher"
		h.d.phone().rc["session_01PhoneAAA"] = s.id
		h.d.send("demo")
	})
	h.eventually("allow button", func() bool { return h.button("Allow") != nil })
	h.ui(func() { test.Tap(h.button("Allow")) })
	var got []folders.Event
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if got = events(); len(got) >= 2 {
			break
		}
	}
	if len(got) != 2 || got[0].Type != "approval" || got[1].Type != "finished" {
		t.Fatalf("approval then finished, even while looking at the session: %+v", got)
	}
	if e := got[0]; e.Quill != s.id || e.Title != "Launcher" || e.RC != "session_01PhoneAAA" {
		t.Fatalf("event names the session: %+v", e)
	}
}

func TestReattachedSessionKeepsPhoneLink(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "haiku", "default")
		s.rcOn, s.rcURL = true, "https://claude.ai/code/session_01ReattachAAA"
		h.d.reclaimRC(s)
		if h.d.phone().rc["session_01ReattachAAA"] != s.id {
			t.Fatal("a session handed over by a reload keeps its phone link, so notifications can open it")
		}
	})
}

func TestPhoneUsageSnapshot(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "haiku", "default")
		s.conv.Stats.ContextUsed, s.conv.Stats.ContextMax = 1200, 200000
		h.d.phone().rc["session_01UsageAAA"] = s.id
		h.d.setRateLimit(&protocol.RateLimit{Status: "allowed", Type: "five_hour", Utilization: 0.37, ResetsAt: 1795162800, OverageStatus: "rejected", OverageReason: "out_of_credits"})
		u := h.d.phoneUsage()
		m, ok := u.Meta["session_01UsageAAA"]
		if !ok || m.Status != "none" || m.Context != 1200 || m.ContextMax != 200000 {
			t.Fatalf("meta for each phone-linked session: %+v %v", m, ok)
		}
		if u.Usage.FiveHour == nil || u.Usage.FiveHour.Utilization != 0.37 || u.Usage.Credits != "out of credits" || u.Usage.At == 0 {
			t.Fatalf("usage mirrors the strip: %+v", u.Usage)
		}
	})
}
