package main

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The phone's open chat updates live: with a long poll on /phone/stream open
// and no other phone request, a reply the session produces must come back as
// a session change with a higher rev within a few seconds (user 2026-11-22:
// Quill Android only showed new replies after a send or Refresh).
func TestPhoneStreamLiveReply(t *testing.T) {
	for _, tc := range []struct {
		name  string
		open  bool // opened in Quill Desktop first (its transcript is loaded)
		async bool // fyne.Do-like d.do that returns before f runs
	}{
		{"opened", true, false},
		{"opened-async", true, true},
		{"not-opened", false, false},
		{"not-opened-async", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) { phoneStreamLive(t, tc.open, tc.async) })
	}
}

func phoneStreamLive(t *testing.T, open, async bool) {
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	h := newHarness(t)
	if async {
		doWait := h.d.doWait // gateUI's: drops calls once the test ended
		h.d.do = func(f func()) { go doWait(f) }
	}
	var link string
	h.ui(func() {
		h.d.app.Preferences().SetInt("phonePort", 0)
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
		link = h.d.pairLink()
		if open {
			h.d.open(editsID)
		} else {
			h.d.open(smokeID) // something else is open in Quill Desktop
		}
	})
	defer h.ui(h.d.stopPhone)
	if open {
		h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	}
	u, _ := url.Parse(link)
	q := u.Query()
	a := unlockedClient(h, &apiClient{t: t, base: "https://127.0.0.1:" + q.Get("p"), key: q.Get("k"),
		c: &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}})

	// The phone shows the chat: the full window first (as Quill Android does).
	_, m, _ := a.do("GET", "/phone/session/"+editsID+"/items", "", nil)
	epoch, rev := int(m["epoch"].(float64)), int(m["rev"].(float64))

	// Quill Desktop runs a turn in it (no phone request involved).
	say := func(text string) {
		h.ui(func() {
			h.d.sendTo(h.d.session(editsID), text)
		})
	}
	say("first")
	h.eventually("first reply", func() bool { return strings.Contains(lastText(h, editsID), "echo: first") })

	_, sess, _ := a.do("GET", "/phone/sessions", "", nil)
	cursor := int(sess["cursor"].(float64))
	for _, x := range sess["sessions"].([]any) {
		if s := x.(map[string]any); s["id"] == editsID {
			if int(s["epoch"].(float64)) != epoch || int(s["rev"].(float64)) <= rev {
				// the phone would fetch now; do it, like Quill Android
				_, m, _ := a.do("GET", "/phone/session/"+editsID+"/items?epoch="+strconv.Itoa(epoch)+"&since="+strconv.Itoa(rev), "", nil)
				epoch, rev = int(m["epoch"].(float64)), int(m["rev"].(float64))
			}
		}
	}
	_, m, _ = a.do("GET", "/phone/session/"+editsID+"/items?epoch="+strconv.Itoa(epoch)+"&since="+strconv.Itoa(rev), "", nil)
	epoch, rev = int(m["epoch"].(float64)), int(m["rev"].(float64))

	type res struct{ m map[string]any }
	done := make(chan res, 1)
	go func() {
		_, m, _ := a.do("GET", "/phone/stream?cursor="+strconv.Itoa(cursor)+"&wait=25", "", nil)
		done <- res{m}
	}()
	time.Sleep(300 * time.Millisecond)
	say("second")
	deadline := time.After(5 * time.Second)
	for {
		var st map[string]any
		select {
		case r := <-done:
			st = r.m
		case <-deadline:
			t.Fatal("the stream did not report the new reply within 5 s")
		}
		if st["reset"] == true {
			t.Fatalf("stream reset: %v", st)
		}
		for _, x := range st["changes"].([]any) {
			c := x.(map[string]any)
			if c["type"] != "session" {
				continue
			}
			if s := c["session"].(map[string]any); s["id"] == editsID && (int(s["epoch"].(float64)) != epoch || int(s["rev"].(float64)) > rev) {
				_, m, _ := a.do("GET", "/phone/session/"+editsID+"/items?epoch="+strconv.Itoa(epoch)+"&since="+strconv.Itoa(rev), "", nil)
				for _, it := range m["items"].([]any) {
					if strings.Contains(it.(map[string]any)["text"].(string), "echo: second") {
						return
					}
				}
				epoch, rev = int(m["epoch"].(float64)), int(m["rev"].(float64))
			}
		}
		cursor = int(st["cursor"].(float64))
		go func() {
			_, m, _ := a.do("GET", "/phone/stream?cursor="+strconv.Itoa(cursor)+"&wait=25", "", nil)
			done <- res{m}
		}()
	}
}

func lastText(h *harness, id string) string {
	var out string // called inside h.eventually, which holds uiMu
	if s := h.d.sessions[id]; s != nil {
		for _, it := range s.conv.Items {
			out += it.Text + "\n"
		}
	}
	return out
}
