package main

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestPhoneAPISuggestion(t *testing.T) {
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	h := newHarness(t)
	var link string
	var s *deskSession
	h.ui(func() {
		h.d.app.Preferences().SetInt("phonePort", 0)
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
		link = h.d.pairLink()
		s = h.d.createSession(h.proj, "default", "default")
		h.d.open(s.id)
	})
	defer h.ui(h.d.stopPhone)
	u, _ := url.Parse(link)
	q := u.Query()
	a := unlockedClient(h, &apiClient{t: t, base: "https://127.0.0.1:" + q.Get("p"), key: q.Get("k"),
		c: &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}})
	find := func(m map[string]any) map[string]any {
		for _, x := range m["sessions"].([]any) {
			if x.(map[string]any)["id"] == s.id {
				return x.(map[string]any)
			}
		}
		t.Fatal("session not listed")
		return nil
	}
	_, m, _ := a.do("GET", "/phone/sessions", "", nil)
	if _, ok := find(m)["suggestion"]; ok {
		t.Fatal("no suggestion: the field is left out")
	}
	cursor := int(m["cursor"].(float64))
	done := make(chan map[string]any, 1)
	go func() {
		_, st, _ := a.do("GET", "/phone/stream?cursor="+strconv.Itoa(cursor)+"&wait=10", "", nil)
		done <- st
	}()
	time.Sleep(300 * time.Millisecond)
	h.ui(func() { s.conv.Suggestion = "run the tests" })
	select {
	case st := <-done:
		got := ""
		for _, x := range st["changes"].([]any) {
			c := x.(map[string]any)
			if c["type"] == "session" {
				if ss := c["session"].(map[string]any); ss["id"] == s.id {
					got, _ = ss["suggestion"].(string)
				}
			}
		}
		if got != "run the tests" {
			t.Fatalf("the stream sends the new suggestion: %v", st)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("stream did not answer")
	}
	_, m, _ = a.do("GET", "/phone/sessions", "", nil)
	if find(m)["suggestion"] != "run the tests" {
		t.Fatal("sessions carry the suggestion")
	}
	h.ui(func() { s.conv.Suggestion = "/clear" })
	_, m, _ = a.do("GET", "/phone/sessions", "", nil)
	if _, ok := find(m)["suggestion"]; ok {
		t.Fatal("the clear-suggestion rule hides /clear at low context, like the desktop")
	}
}
