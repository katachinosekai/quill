package main

import (
	"strings"
	"sync"
	"testing"

	"fyne.io/fyne/v2/test"
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
