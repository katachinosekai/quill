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
	if code != 200 || hello["phoneId"] != q.Get("i") || hello["control"] != false || hello["boot"] == "" || hello["quill"] != version {
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
	if _, ok := edits["tokens"].(float64); !ok {
		t.Fatalf("sessions carry their token total: %v", edits)
	}
	usage, _ := sess["usage"].(map[string]any)
	if usage["credits"] != "not reported" || usage["todayTokens"] == nil || usage["weekTokens"] == nil {
		t.Fatalf("usage carries credits and today/week tokens: %v", usage)
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

func TestPhoneAPIWrites(t *testing.T) {
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
	n := 0
	key := func() string {
		n++
		time.Sleep(110 * time.Millisecond)
		return "write-key-" + strconv.Itoa(1000000+n)
	}
	base := "/phone/session/" + editsID

	if code, m, _ := a.do("POST", base+"/send", key(), map[string]string{"text": "hi"}); code != 403 || errCode(m) != "control_off" {
		t.Fatalf("writes need the control setting: %d %v", code, m)
	}
	h.ui(func() { h.d.app.Preferences().SetBool("phoneControl", true) })

	sendKey := key()
	if code, m, _ := a.do("POST", base+"/send", sendKey, map[string]string{"text": "write"}); code != 200 || m["queued"] != false {
		t.Fatalf("send: %d %v", code, m)
	}
	s := h.d.sessions[editsID]
	var reqID string
	h.eventually("permission from the phone's message", func() bool {
		for _, it := range s.conv.PendingPermissions() {
			reqID = it.RequestID
		}
		return reqID != ""
	})
	if code, _, hdr := a.do("POST", base+"/send", sendKey, map[string]string{"text": "write"}); code != 200 || hdr.Get("Idempotent-Replay") != "true" {
		t.Fatal("a retried send is replayed, not sent again")
	}
	users := 0
	h.ui(func() {
		for _, it := range s.conv.Items {
			if it.Kind == 0 && it.Text == "write" {
				users++
			}
		}
	})
	if users != 1 {
		t.Fatalf("the message went out once: %d", users)
	}
	if code, m, _ := a.do("POST", base+"/answer", key(), map[string]string{"requestId": reqID, "action": "rejectPlan"}); code != 400 || errCode(m) != "bad_request" {
		t.Fatalf("wrong action for the item: %d %v", code, m)
	}
	code, m, _ := a.do("POST", base+"/answer", key(), map[string]string{"requestId": reqID, "action": "allow"})
	item, _ := m["item"].(map[string]any)
	req, _ := item["request"].(map[string]any)
	if code != 200 || req["state"] != "allowed" {
		t.Fatalf("allow: %d %v", code, m)
	}
	if code, m, _ := a.do("POST", base+"/answer", key(), map[string]string{"requestId": reqID, "action": "deny"}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("answered already: %d %v", code, m)
	}
	if code, m, _ := a.do("POST", base+"/answer", key(), map[string]string{"requestId": "nope", "action": "allow"}); code != 404 {
		t.Fatalf("unknown request: %d %v", code, m)
	}

	h.ui(func() { s.queue = []string{"first", "second"} })
	if code, m, _ := a.do("POST", base+"/queue/delete", key(), map[string]any{"index": 0, "text": "other"}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("queue text must match: %d %v", code, m)
	}
	if code, m, _ := a.do("POST", base+"/queue/delete", key(), map[string]any{"index": 0, "text": "first"}); code != 200 || len(m["queue"].([]any)) != 1 {
		t.Fatalf("queue delete: %d %v", code, m)
	}
	h.ui(func() {
		if len(s.queue) != 1 || s.queue[0] != "second" {
			t.Fatalf("queue after delete: %v", s.queue)
		}
		s.queue = nil
	})

	var folder string
	h.ui(func() { folder = s.cwd })
	if code, m, _ := a.do("POST", "/phone/sessions/new", key(), map[string]string{"folder": "/nowhere"}); code != 400 {
		t.Fatalf("unknown folder: %d %v", code, m)
	}
	code, m, _ = a.do("POST", "/phone/sessions/new", key(), map[string]string{"folder": folder, "backend": "online", "mode": "plan"})
	ns, _ := m["session"].(map[string]any)
	if code != 200 || ns["mode"] != "plan" || ns["folder"] != folder {
		t.Fatalf("new session: %d %v", code, m)
	}
	ks := &kmemStub{saved: map[string]bool{"demo-work": true, "busy-work": true}}
	h.ui(func() {
		h.d.kmemOff = false
		h.d.kmemRun = ks.run
		h.d.org.Kmem[editsID] = "busy-work"
	})
	code, m, _ = a.do("GET", "/phone/handoffs", "", nil)
	list, _ := m["handoffs"].([]any)
	if code != 200 || len(list) != 2 {
		t.Fatalf("handoffs: %d %v", code, m)
	}
	for _, x := range list {
		e := x.(map[string]any)
		if e["name"] == "busy-work" && e["openIn"] == nil || e["name"] == "demo-work" && e["openIn"] != nil || e["savedMs"].(float64) <= 0 {
			t.Fatalf("openIn marks the handoff that is open in a tab: %v", e)
		}
	}
	if code, m, _ = a.do("GET", "/phone/handoffs/slug?title=Demo+Work+2", "", nil); code != 200 || m["slug"] != "demo-work-2" {
		t.Fatalf("slug: %d %v", code, m)
	}
	if code, m, _ = a.do("POST", "/phone/sessions/new", key(), map[string]any{"folder": folder, "start": map[string]string{"kmem": "none"}}); code != 422 || errCode(m) != "bad_request" {
		t.Fatalf("no kmem is not offered any more: %d %v", code, m)
	}
	if code, m, _ = a.do("POST", "/phone/sessions/new", key(), map[string]any{"folder": folder, "start": map[string]string{"kmem": "resume", "name": "busy-work"}}); code != 409 || errCode(m) != "blocked" {
		t.Fatalf("resuming a handoff that is open elsewhere is blocked: %d %v", code, m)
	}
	code, m, _ = a.do("POST", "/phone/sessions/new", key(), map[string]any{"folder": folder, "start": map[string]string{"kmem": "resume", "name": "demo-work"}})
	rs, _ := m["session"].(map[string]any)
	if code != 200 {
		t.Fatalf("resume: %d %v", code, m)
	}
	code, m, _ = a.do("POST", "/phone/sessions/new", key(), map[string]any{"folder": folder, "start": map[string]string{"kmem": "new", "title": "Tablet Thing"}})
	ts, _ := m["session"].(map[string]any)
	if code != 200 {
		t.Fatalf("new with title: %d %v", code, m)
	}
	h.ui(func() {
		if h.d.org.Kmem[rs["id"].(string)] != "demo-work" || h.d.org.Titles[rs["id"].(string)] != "demo-work" {
			t.Fatalf("resume links the handoff: %q", h.d.org.Kmem[rs["id"].(string)])
		}
		if h.d.org.Kmem[ts["id"].(string)] != "tablet-thing" || h.d.org.Titles[ts["id"].(string)] != "Tablet Thing" {
			t.Fatalf("new gets a kmem name from its title: %q", h.d.org.Kmem[ts["id"].(string)])
		}
		h.d.kmemOff = true
	})
	if code, m, _ := a.do("POST", "/phone/session/"+ns["id"].(string)+"/interrupt", key(), map[string]any{}); code != 409 || errCode(m) != "not_running" {
		t.Fatalf("interrupt without a process: %d %v", code, m)
	}
	if code, m, _ := a.do("POST", base+"/send", key(), map[string]string{"text": "/login"}); code != 409 || errCode(m) != "blocked" {
		t.Fatalf("desktop-only commands are refused for devices: %d %v", code, m)
	}
	if _, m, _ := a.do("GET", "/phone/sessions", "", nil); m["control"] != true {
		t.Fatalf("/phone/sessions reports control: %v", m["control"])
	}
	h.ui(func() {
		if h.d.current == nil || h.d.current.id == ns["id"] {
			t.Fatal("a device-created session doesn't take over the desktop's current tab")
		}
	})
	if code, m, _ := a.do("POST", "/phone/usage/resume", key(), map[string]any{}); code != 200 || m["usage"] == nil {
		t.Fatalf("resume: %d %v", code, m)
	}
	h.ui(func() { h.d.app.Preferences().SetBool("phoneControl", false) })
	if code, m, _ := a.do("POST", "/phone/usage/resume", key(), map[string]any{}); code != 403 || errCode(m) != "control_off" {
		t.Fatalf("turning control off stops writes: %d %v", code, m)
	}
}
