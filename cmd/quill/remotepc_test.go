package main

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
)

func TestParsePairLink(t *testing.T) {
	c, err := parsePairLink("quillpair://pair?h=192.168.1.5,office.tail.ts.net&p=8443&k=key123&fp=" + strings.Repeat("ab", 32) + "&i=a1b2c3d4&v=2&n=OFFICE-PC")
	if err != nil || c.Name != "OFFICE-PC" || len(c.Hosts) != 2 || c.Port != 8443 || c.Key != "key123" || c.ID == "" {
		t.Fatalf("parse: %+v %v", c, err)
	}
	for _, bad := range []string{"https://example.com", "quillpair://pair?h=x&p=1&k=k", "quillpair://pair?p=1&k=k&fp=" + strings.Repeat("ab", 32)} {
		if _, err := parsePairLink(bad); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
}

// One Quill pairs with itself: it serves its live sessions and shows them
// again as remote tabs.
func TestRemotePCSessionsAsTabs(t *testing.T) {
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	h := newHarness(t)
	var link string
	var other *deskSession
	h.ui(func() {
		h.d.app.Preferences().SetInt("phonePort", 0)
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
		h.d.app.Preferences().SetBool("phoneControl", true)
		link = h.d.pairLink()
		h.d.open(editsID)
		other = h.d.createSession(h.proj, "default", "default")
	})
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	conf, err := parsePairLink(link)
	if err != nil {
		t.Fatal(err)
	}
	conf.Hosts = []string{"127.0.0.1"}
	conf.Name = "Office PC"
	defer h.ui(h.d.stopPhone)
	defer h.ui(h.d.stopRemotes)
	h.ui(func() {
		h.d.saveRemotePCs([]remotePCConf{conf})
		h.d.startRemotePCs()
		if len(h.d.remotes) != 0 {
			t.Fatal("paired PCs don't connect on their own")
		}
	})
	connect := func(pw string) (err error) {
		time.Sleep(1100 * time.Millisecond) // the phone server allows 10 writes a second per device
		done := make(chan error, 1)
		h.ui(func() { h.d.unlockPC(conf, pw, func(e error) { done <- e }) })
		select {
		case err = <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("unlock timed out")
		}
		return err
	}
	if err := connect("secret-pass"); err == nil || !strings.Contains(err.Error(), "no_password") {
		t.Fatalf("without a password on that PC nothing connects: %v", err)
	}
	h.ui(func() { h.d.app.Preferences().SetString(prefPCPassword, hashPCPassword("secret-pass")) })
	if err := connect("wrong"); err == nil || !strings.Contains(err.Error(), "wrong password") {
		t.Fatalf("a wrong password is refused: %v", err)
	}
	if err := connect("secret-pass"); err != nil {
		t.Fatalf("the right password connects: %v", err)
	}
	rid := remoteLocalID(conf.ID, editsID)
	oid := remoteLocalID(conf.ID, other.id)
	inTabs := func(id string) bool {
		for _, t := range h.d.tabs {
			if t == id {
				return true
			}
		}
		return false
	}
	h.eventually("remote tabs appear", func() bool { return inTabs(rid) && inTabs(oid) })
	h.ui(func() {
		s := h.d.sessions[rid]
		if s == nil || s.remote == nil {
			t.Fatal("the remote tab is a remote session")
		}
		if _, meta, _ := h.d.rowText(rid); !strings.Contains(meta, "on Office PC") {
			t.Fatalf("the row says which PC: %q", meta)
		}
		if err := h.d.ensureProcess(s); err == nil {
			t.Fatal("a remote session never starts a local Claude")
		}
		h.d.open(rid)
	})
	h.eventually("remote chat loaded", func() bool { return len(h.d.sessions[rid].conv.Items) > 0 })

	// Our own phones never see the remote copies. (The test's phone uses the
	// same key, which is a PC now: it is locked without the token.)
	u, _ := url.Parse(link)
	q := u.Query()
	a := &apiClient{t: t, base: "https://127.0.0.1:" + q.Get("p"), key: q.Get("k"),
		c: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}}
	if code, _, _ := a.do("GET", "/phone/sessions", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("a locked PC gets 401 without its token: %d", code)
	}
	if code, _, _ := a.do("GET", "/folders.json", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("a locked PC doesn't get the folders file either: %d", code)
	}
	var devID string
	h.ui(func() {
		devID = h.d.phones()[0].ID
		if h.d.phones()[0].Kind != "pc" {
			t.Fatalf("the hello marked the device as a PC: %+v", h.d.phones()[0])
		}
		h.d.setDeviceLock(devID, false)
	})
	_, m, _ := a.do("GET", "/phone/sessions", "", nil)
	h.ui(func() { h.d.setDeviceLock(devID, true) })
	if err := connect("secret-pass"); err != nil {
		t.Fatalf("reconnect after the lock switch: %v", err)
	}
	h.eventually("remote tabs again", func() bool { return inTabs(rid) && inTabs(oid) })
	for _, x := range m["sessions"].([]any) {
		if id := x.(map[string]any)["id"].(string); strings.HasPrefix(id, remotePrefix) {
			t.Fatalf("remote sessions are not re-exported: %s", id)
		}
	}

	// Sending from the remote tab reaches the session on the other PC.
	h.ui(func() { h.d.sendTo(h.d.sessions[rid], "hello from the other pc") })
	h.eventually("message arrives there", func() bool {
		for _, it := range h.d.sessions[editsID].conv.Items {
			if it.Kind == chat.User && it.Text == "hello from the other pc" {
				return true
			}
		}
		return false
	})

	// A session that ends there leaves our tabs.
	h.ui(func() { h.d.closeTab(other.id) })
	h.eventually("ended session's tab goes", func() bool { return !inTabs(oid) && h.d.sessions[oid] == nil })

	// Memory only: nothing about the remote sessions is saved.
	h.ui(func() {
		h.d.open(rid)
		h.d.input.SetText("draft for the other pc")
		h.d.renameSession(rid, "should not stick")
		h.d.saveTabs()
		h.d.saveOrg()
		st := h.d.restoreHead()
		for name, v := range map[string]string{"tabs": h.d.app.Preferences().String("tabs"), "org": fmt.Sprint(h.d.org), "restore": fmt.Sprint(st)} {
			if strings.Contains(v, remotePrefix) {
				t.Errorf("%s keeps a remote session: %s", name, v)
			}
		}
	})

	// The PC locks us out (token expired there): we disconnect and forget.
	h.ui(func() { h.d.pcLock.reset() })
	h.ui(func() { h.d.sendTo(h.d.sessions[rid], "after the lock") })
	h.eventually("locked out", func() bool { return len(h.d.remotes) == 0 && h.d.sessions[rid] == nil && !inTabs(rid) })
	h.ui(func() {
		if !strings.Contains(h.d.pcNote[conf.ID], "locked") {
			t.Fatalf("the sidebar says why: %q", h.d.pcNote[conf.ID])
		}
	})

	// No activity: auto-off.
	if err := connect("secret-pass"); err != nil {
		t.Fatal(err)
	}
	h.eventually("tabs back", func() bool { return inTabs(rid) })
	h.ui(func() {
		l := h.d.remotes[conf.ID]
		l.mu.Lock()
		l.lastUser = time.Now().Add(-time.Hour)
		l.mu.Unlock()
		go l.idleWatch(40 * time.Millisecond)
	})
	h.eventually("idle disconnect", func() bool { return len(h.d.remotes) == 0 && h.d.sessions[rid] == nil })
	h.ui(func() {
		if !strings.Contains(h.d.pcNote[conf.ID], "without activity") {
			t.Fatalf("the sidebar says why: %q", h.d.pcNote[conf.ID])
		}
	})
}

func TestPCLock(t *testing.T) {
	stored := hashPCPassword("correct horse")
	if !checkPCPassword(stored, "correct horse") || checkPCPassword(stored, "wrong") || checkPCPassword("garbage", "x") {
		t.Fatal("password hash check")
	}
	if strings.Contains(stored, "correct") {
		t.Fatal("only a hash is stored")
	}
	now := time.Unix(1000, 0)
	l := newPCLock()
	l.now = func() time.Time { return now }
	if tok, msg := l.unlock("dev", "", "x"); tok != "" || !strings.HasPrefix(msg, "no password") {
		t.Fatalf("no password set: %q %q", tok, msg)
	}
	for i := 1; i <= pcMaxFails; i++ {
		if tok, _ := l.unlock("dev", stored, "nope"); tok != "" {
			t.Fatal("wrong password unlocks")
		}
	}
	if tok, msg := l.unlock("dev", stored, "correct horse"); tok != "" || !strings.HasPrefix(msg, "too many") {
		t.Fatalf("locked out after %d wrong tries: %q", pcMaxFails, msg)
	}
	now = now.Add(pcLockout + time.Second)
	tok, msg := l.unlock("dev", stored, "correct horse")
	if tok == "" {
		t.Fatalf("unlocks after the lock-out: %q", msg)
	}
	idle := 15 * time.Minute
	if !l.valid("dev", tok, idle, false) || l.valid("dev", "other", idle, false) || l.valid("dev2", tok, idle, false) {
		t.Fatal("token check")
	}
	now = now.Add(10 * time.Minute)
	if !l.valid("dev", tok, idle, true) {
		t.Fatal("still valid; a write counts as activity")
	}
	now = now.Add(10 * time.Minute)
	if !l.valid("dev", tok, idle, false) {
		t.Fatal("activity moved the idle clock")
	}
	now = now.Add(6 * time.Minute)
	if l.valid("dev", tok, idle, false) {
		t.Fatal("reads alone don't keep it: expired after the idle time")
	}
}
