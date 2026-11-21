package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

type apiClient struct {
	t    *testing.T
	base string
	key  string
	c    *http.Client
}

func (a *apiClient) do(method, path, idem string, body any) (int, map[string]any, http.Header) {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, a.base+path, rd)
	req.Header.Set("X-Quill-Key", a.key)
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	resp, err := a.c.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out, resp.Header
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func TestPhoneAPIReadSide(t *testing.T) {
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	h := newHarness(t)
	var link string
	h.ui(func() {
		h.d.app.Preferences().SetInt("phonePort", 0)
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
		link = h.d.pairLink()
		h.d.open(editsID)
	})
	defer h.ui(h.d.stopPhone)
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	u, _ := url.Parse(link)
	q := u.Query()
	a := &apiClient{t: t, base: "https://127.0.0.1:" + q.Get("p"), key: q.Get("k"),
		c: &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}}

	if code, m, _ := a.do("GET", "/phone/nope", "", nil); code != 404 || errCode(m) != "not_found" {
		t.Fatalf("unknown path: %d %v", code, m)
	}
	bad := *a
	bad.key = "wrong-key-wrong-key"
	if code, m, _ := bad.do("GET", "/phone/sessions", "", nil); code != 403 || errCode(m) != "forbidden" {
		t.Fatalf("unknown key: %d %v", code, m)
	}
	if code, m, _ := a.do("POST", "/phone/hello", "", map[string]string{"name": "Pixel"}); code != 400 || errCode(m) != "idempotency_key_missing" {
		t.Fatalf("POST without key: %d %v", code, m)
	}
	code, hello, _ := a.do("POST", "/phone/hello", "hello-key-0000000001", map[string]string{"name": "Test Pixel", "app": "1.0"})
	if code != 200 || hello["phoneId"] != q.Get("i") || hello["control"] != false || hello["boot"] == "" {
		t.Fatalf("hello: %d %v", code, hello)
	}
	if code, again, hdr := a.do("POST", "/phone/hello", "hello-key-0000000001", map[string]string{"name": "Test Pixel", "app": "1.0"}); code != 200 || hdr.Get("Idempotent-Replay") != "true" || again["phoneId"] != hello["phoneId"] {
		t.Fatalf("replay: %d %v", code, again)
	}
	if code, m, _ := a.do("POST", "/phone/hello", "hello-key-0000000001", map[string]string{"name": "Other"}); code != 422 || errCode(m) != "idempotency_mismatch" {
		t.Fatalf("mismatch: %d %v", code, m)
	}
	h.ui(func() {
		named := false
		for _, p := range h.d.phones() {
			named = named || p.ID == q.Get("i") && p.Name == "Test Pixel"
		}
		if !named {
			t.Fatal("hello names the phone in Quill's list")
		}
	})

	code, sess, _ := a.do("GET", "/phone/sessions", "", nil)
	if code != 200 || sess["boot"] != hello["boot"] {
		t.Fatalf("sessions: %d %v", code, sess)
	}
	var edits map[string]any
	for _, x := range sess["sessions"].([]any) {
		if m := x.(map[string]any); m["id"] == editsID {
			edits = m
		}
	}
	if edits == nil || edits["status"] != "stopped" || edits["focused"] != true || edits["backend"] != "online" || edits["group"] == "" {
		t.Fatalf("edits session: %v", edits)
	}
	cursor := int(sess["cursor"].(float64))

	code, items, _ := a.do("GET", "/phone/session/"+editsID+"/items", "", nil)
	list, _ := items["items"].([]any)
	if code != 200 || len(list) == 0 || items["reset"] != false {
		t.Fatalf("items: %d %v", code, items)
	}
	epoch, rev := int(items["epoch"].(float64)), int(items["rev"].(float64))
	kinds := map[string]bool{}
	for _, x := range list {
		kinds[x.(map[string]any)["kind"].(string)] = true
	}
	if !kinds["user"] || !kinds["assistant"] || !kinds["tool"] {
		t.Fatalf("item kinds: %v", kinds)
	}
	sinceQ := "/phone/session/" + editsID + "/items?epoch=" + strconv.Itoa(epoch) + "&since=" + strconv.Itoa(rev)
	if _, m, _ := a.do("GET", sinceQ, "", nil); len(m["items"].([]any)) != 0 {
		t.Fatalf("nothing changed since rev: %v", m["items"])
	}
	if _, m, _ := a.do("GET", "/phone/session/"+editsID+"/items?epoch="+strconv.Itoa(epoch+7)+"&since=0", "", nil); m["reset"] != true {
		t.Fatal("an old epoch gets the window with reset")
	}

	done := make(chan map[string]any, 1)
	go func() {
		_, m, _ := a.do("GET", "/phone/stream?cursor="+strconv.Itoa(cursor)+"&wait=10", "", nil)
		done <- m
	}()
	time.Sleep(300 * time.Millisecond)
	h.ui(func() {
		s := h.d.sessions[editsID]
		s.conv.AddNotice("hello from the test")
	})
	var st map[string]any
	select {
	case st = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("stream did not answer")
	}
	changed := false
	for _, x := range st["changes"].([]any) {
		c := x.(map[string]any)
		if c["type"] == "session" {
			if s := c["session"].(map[string]any); s["id"] == editsID && int(s["rev"].(float64)) > rev {
				changed = true
			}
		}
	}
	if !changed || st["reset"] != false {
		t.Fatalf("stream reports the session's new rev: %v", st)
	}
	_, m, _ := a.do("GET", sinceQ, "", nil)
	got := m["items"].([]any)
	if len(got) != 1 || got[0].(map[string]any)["text"] != "hello from the test" || got[0].(map[string]any)["kind"] != "notice" {
		t.Fatalf("since returns only the new item: %v", got)
	}
	if _, m, _ := a.do("GET", "/phone/stream?cursor=999999&wait=0", "", nil); m["reset"] != true {
		t.Fatal("a cursor from the future resets")
	}

	ended := make(chan int, 1)
	go func() {
		c, _, _ := a.do("GET", "/phone/stream?cursor="+strconv.Itoa(int(st["cursor"].(float64)))+"&wait=20", "", nil)
		ended <- c
	}()
	time.Sleep(300 * time.Millisecond)
	h.ui(func() { h.d.revokePhone(q.Get("i")) })
	select {
	case c := <-ended:
		if c != 403 {
			t.Fatalf("revoked stream: %d", c)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("revoke did not end the stream")
	}
}
