package main

import (
	"crypto/tls"
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
		h.d.startRemote(conf)
	})
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

	// Our own phones never see the remote copies.
	u, _ := url.Parse(link)
	q := u.Query()
	a := &apiClient{t: t, base: "https://127.0.0.1:" + q.Get("p"), key: q.Get("k"),
		c: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}}
	_, m, _ := a.do("GET", "/phone/sessions", "", nil)
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
}
