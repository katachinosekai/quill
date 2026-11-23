package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Phase 3: the read-only Memory view (find, get, fact). Placeholder data
// only (kmem rule 22).

const (
	memFindOut = "NOTICE (once): 2 shared rules changed or are unread this session: kmem rules (prints and marks all)\n" +
		"something kmem printed that is no fact\n" +
		"demo/decisions: [f1a2b3c] decision: Use the demo layout in /home/user/Projects/demo  WHY: asked by exampleuser  (pinned)\n" +
		"demo/notes: [f2b3c4d] fact: first line\nsecond line of the same fact\n" +
		"demo/old: [f3c4d5e] lesson: mail user@example.com  (EXPIRED: valid to 2026-01-01)\n"
	memGetOut = "[f4d5e6f] fact: Own fact of demo  WHY: placeholder\n" +
		"sections of demo (kmem get <section>, or --all):\n" +
		"  demo/decisions  40 facts, 13.2 KB\n" +
		"  demo/notes  1 facts, 0.4 KB\n" +
		"(1 expired fact(s) hidden: past their --valid-to; --all shows them)\n"
	memShowOut = "[f5e6f7a] decision: Old layout in /home/user/demo  WHY: by exampleuser  (superseded)\n" +
		"  topic demo/decisions, recorded 2026-11-20T10:00:00-08:00 (event 12) by exampleuser, source primary, valid ? .. 2026-12-31, from task t1a2b3c\n" +
		"  cites /home/user/demo/x.go:1-2\n" +
		"  2026-11-21T10:00:00-08:00 superseded by f1a2b3c (demo)\n"
)

type memFake struct {
	*phoneFakeKmem
	block chan struct{} // set: get waits for it
	in    chan struct{}
}

func (k *memFake) run(prog string, args ...string) (string, error) {
	a := strings.Join(args, " ")
	switch {
	case a == "find -- demo stuff":
		k.note(a)
		return memFindOut, nil
	case a == "find -- nothing here":
		k.note(a)
		return "no matches\n", nil
	case a == "find -- (a":
		k.note(a)
		return "bad regex: missing ), unterminated subpattern at position 0\n", errors.New("exit status 1")
	case a == "find -- many":
		k.note(a)
		var b strings.Builder
		for i := 0; i < 250; i++ {
			fmt.Fprintf(&b, "demo: [f%06x] fact: fact number %d\n", 0x100000+i, i)
		}
		return b.String(), nil
	case a == "get -- demo":
		k.note(a)
		k.mu.Lock()
		block, in := k.block, k.in
		k.mu.Unlock()
		if in != nil {
			in <- struct{}{}
		}
		if block != nil {
			<-block
		}
		return memGetOut, nil
	case a == "get -- missing":
		k.note(a)
		return "no current facts under missing; try kmem topics or kmem find\n", nil
	case a == "show -- f5e6f7a":
		k.note(a)
		return memShowOut, nil
	case strings.HasPrefix(a, "show -- "):
		k.note(a)
		return "no fact or task " + strings.TrimPrefix(a, "show -- ") + "\n", errors.New("exit status 1")
	}
	return k.phoneFakeKmem.run(prog, args...)
}

func (k *memFake) note(a string) {
	k.mu.Lock()
	k.calls = append(k.calls, a)
	k.mu.Unlock()
}

func TestPhoneKmemMemoryParse(t *testing.T) {
	facts, secs, none := parseKmemFacts(memGetOut, "demo")
	if none || len(facts) != 1 || len(secs) != 2 || facts[0].Topic != "demo" || facts[0].Why != "placeholder" ||
		secs[0] != (kmemSection{"demo/decisions", 40, 13.2}) {
		t.Fatalf("get: %+v %+v %v", facts, secs, none)
	}
	if _, _, none := parseKmemFacts("no matches\n", ""); !none {
		t.Fatal("no matches")
	}
	d, ok := parseKmemShow(memShowOut)
	if !ok || d.State != "superseded" || d.SupersededBy != "f1a2b3c" || d.ValidFrom != "" || d.ValidTo != "2026-12-31" || d.Task != "t1a2b3c" || len(d.History) != 1 {
		t.Fatalf("show: %+v", d)
	}
	if _, ok := parseKmemShow("[t1a2b3c] p3 open: a task  (demo)\n"); ok {
		t.Fatal("a task is no fact")
	}
}

func TestPhoneKmemMemory(t *testing.T) {
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	oldH, oldU, oldF := kmemScrubHome, kmemScrubUser, kmemFindEvery
	kmemScrubHome = func() string { return "/home/user" }
	kmemScrubUser = func() string { return "exampleuser" }
	kmemFindEvery = 0
	defer func() { kmemScrubHome, kmemScrubUser, kmemFindEvery = oldH, oldU, oldF }()

	h := newHarness(t)
	k := &memFake{phoneFakeKmem: &phoneFakeKmem{}}
	h.d.kmemOff = false
	h.d.kmemVer.reset()
	h.d.kmemRun = k.run
	var link string
	h.ui(func() {
		h.d.app.Preferences().SetString(memoryModeKey, memoryModeKmem)
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
	get := func(path string) (int, map[string]any, string, http.Header) {
		code, m, hd := a.do("GET", path, "", nil)
		js, _ := json.Marshal(m)
		return code, m, string(js), hd
	}
	noPersonal := func(what, js string) {
		for _, bad := range []string{"/home/user", "exampleuser", "user@example.com"} {
			if strings.Contains(js, bad) {
				t.Fatalf("%s leaks %q: %s", what, bad, js)
			}
		}
	}

	// find: rows, scrubbing, a multi-line fact, the fallback line (a line
	// after a fact continues it, so only one before the first fact), kmem's
	// notice left out
	code, m, js, _ := get("/phone/kmem/find?q=" + url.QueryEscape("demo stuff"))
	if code != 200 || m["v"] != float64(1) || m["truncated"] != false {
		t.Fatalf("find: %d %v", code, m)
	}
	noPersonal("find", js)
	fs := m["facts"].([]any)
	if len(fs) != 4 || strings.Contains(js, "NOTICE") {
		t.Fatalf("find rows: %s", js)
	}
	f3, f0, f1, f2 := fs[0].(map[string]any), fs[1].(map[string]any), fs[2].(map[string]any), fs[3].(map[string]any)
	if f0["id"] != "f1a2b3c" || f0["kind"] != "decision" || f0["topic"] != "demo/decisions" || f0["text"] != "Use the demo layout in ~/Projects/demo" ||
		f0["why"] != "asked by user" || f0["pinned"] != true {
		t.Fatalf("find row 0: %v", f0)
	}
	if f1["text"] != "first line\nsecond line of the same fact" || f1["pinned"] != false {
		t.Fatalf("multi-line fact: %v", f1)
	}
	if f2["text"] != "mail (e-mail)" || f2["expired"] != true || f2["kind"] != "lesson" {
		t.Fatalf("expired fact: %v", f2)
	}
	if _, ok := f3["id"]; ok || f3["text"] != "something kmem printed that is no fact" {
		t.Fatalf("fallback row: %v", f3)
	}
	if code, m, _, _ := get("/phone/kmem/find?q=" + url.QueryEscape("nothing here")); code != 200 || len(m["facts"].([]any)) != 0 {
		t.Fatalf("no matches: %d %v", code, m)
	}
	if code, m, _, _ := get("/phone/kmem/find?q=many"); code != 200 || len(m["facts"].([]any)) != kmemPhoneFactsMax || m["truncated"] != true {
		t.Fatalf("cap: %d %v", code, m["truncated"])
	}
	for _, bad := range []string{"/phone/kmem/find", "/phone/kmem/find?q=x", "/phone/kmem/find?q=" + strings.Repeat("a", 201)} {
		if code, m, _, _ := get(bad); code != 400 || errCode(m) != "bad_request" {
			t.Fatalf("%s: %d %v", bad, code, m)
		}
	}
	if code, m, _, _ := get("/phone/kmem/find?q=" + url.QueryEscape("(a")); code != 400 || errCode(m) != "bad_request" {
		t.Fatalf("bad regex: %d %v", code, m)
	}

	// get: own facts and sections; unknown topic 404, bad name 400
	code, m, js, _ = get("/phone/kmem/get?topic=demo")
	if code != 200 || m["topic"] != "demo" || len(m["facts"].([]any)) != 1 || m["truncated"] != false {
		t.Fatalf("get: %d %s", code, js)
	}
	if secs, _ := json.Marshal(m["sections"]); string(secs) != `[{"facts":40,"kb":13.2,"name":"demo/decisions"},{"facts":1,"kb":0.4,"name":"demo/notes"}]` {
		t.Fatalf("sections: %s", secs)
	}
	if code, m, _, _ := get("/phone/kmem/get?topic=missing"); code != 404 || errCode(m) != "not_found" {
		t.Fatalf("missing topic: %d %v", code, m)
	}
	for _, bad := range []string{"", "--all", "../x", "a b", "-x"} {
		if code, m, _, _ := get("/phone/kmem/get?topic=" + url.QueryEscape(bad)); code != 400 || errCode(m) != "bad_request" {
			t.Fatalf("topic %q: %d %v", bad, code, m)
		}
	}

	// fact: kmem show
	code, m, js, _ = get("/phone/kmem/fact?id=f5e6f7a")
	if code != 200 {
		t.Fatalf("fact: %d %v", code, m)
	}
	noPersonal("fact", js)
	f := m["fact"].(map[string]any)
	if f["id"] != "f5e6f7a" || f["kind"] != "decision" || f["topic"] != "demo/decisions" || f["text"] != "Old layout in ~/demo" ||
		f["why"] != "by user" || f["state"] != "superseded" || f["supersededBy"] != "f1a2b3c" || f["recorded"] != "2026-11-20T10:00:00-08:00" ||
		f["by"] != "user" || f["source"] != "primary" || f["validTo"] != "2026-12-31" || f["task"] != "t1a2b3c" || f["binned"] != false {
		t.Fatalf("fact: %s", js)
	}
	if hs := f["history"].([]any); len(hs) != 1 || hs[0].(map[string]any)["what"] != "superseded by f1a2b3c" || hs[0].(map[string]any)["by"] != "demo" {
		t.Fatalf("history: %v", f["history"])
	}
	if code, m, _, _ := get("/phone/kmem/fact?id=f0000000"); code != 404 || errCode(m) != "not_found" {
		t.Fatalf("unknown fact: %d %v", code, m)
	}
	for _, bad := range []string{"", "t1a2b3c", "f1", "--all", "F1A2B3C"} {
		if code, m, _, _ := get("/phone/kmem/fact?id=" + url.QueryEscape(bad)); code != 400 || errCode(m) != "bad_request" {
			t.Fatalf("id %q: %d %v", bad, code, m)
		}
	}

	// find at most once a second per phone
	kmemFindEvery = time.Minute
	h.ui(func() { // forget the finds above
		l := &h.d.phoneSync.api.kmem.find
		l.mu.Lock()
		l.last = nil
		l.mu.Unlock()
	})
	if code, _, _, _ := get("/phone/kmem/find?q=" + url.QueryEscape("demo stuff")); code != 200 {
		t.Fatalf("first find: %d", code)
	}
	code, m, _, hd := get("/phone/kmem/find?q=" + url.QueryEscape("demo stuff"))
	if code != 429 || errCode(m) != "rate_limited" || hd.Get("Retry-After") == "" || hd.Get("Retry-After") == "0" {
		t.Fatalf("second find: %d %v %v", code, m, hd)
	}
	if code, _, _, _ := get("/phone/kmem/get?topic=demo"); code != 200 {
		t.Fatalf("get is not rate-limited: %d", code)
	}
	kmemFindEvery = 0

	// one kmem process at a time per phone
	block, in := make(chan struct{}), make(chan struct{}, 1)
	k.mu.Lock()
	k.block, k.in = block, in
	k.mu.Unlock()
	first := make(chan int, 1)
	go func() {
		c, _, _ := a.do("GET", "/phone/kmem/get?topic=demo", "", nil)
		first <- c
	}()
	select {
	case <-in:
	case <-time.After(10 * time.Second):
		t.Fatal("get never ran kmem")
	}
	for _, p := range []string{"/phone/kmem/fact?id=f5e6f7a", "/phone/kmem/find?q=" + url.QueryEscape("demo stuff")} {
		if code, m, _, _ := get(p); code != 409 || errCode(m) != "kmem_busy" {
			t.Fatalf("busy %s: %d %v", p, code, m)
		}
	}
	close(block)
	if c := <-first; c != 200 {
		t.Fatalf("blocked get: %d", c)
	}
	k.mu.Lock()
	k.block, k.in = nil, nil
	k.mu.Unlock()

	// read-only: no writes reach kmem, POST is no route
	if code, m, _ := a.do("POST", "/phone/kmem/find?q=demo", "kmem-find-post-0001", map[string]string{}); code == 200 {
		t.Fatalf("POST find: %d %v", code, m)
	}
	k.mu.Lock()
	for _, c := range k.calls {
		for _, w := range []string{"add", "retire", "trash", "pin", "unpin", "restore", "empty"} {
			if strings.HasPrefix(c, w+" ") {
				t.Fatalf("a Memory read ran a kmem write: %q", c)
			}
		}
	}
	k.mu.Unlock()

	paths := []string{"/phone/kmem/find?q=demo", "/phone/kmem/get?topic=demo", "/phone/kmem/fact?id=f5e6f7a"}
	// kmem off -> 503
	h.ui(func() { h.d.app.Preferences().SetString(memoryModeKey, memoryModeClaude) })
	for _, p := range paths {
		if code, m, _, _ := get(p); code != 503 || errCode(m) != "kmem_unavailable" {
			t.Fatalf("kmem off %s: %d %v", p, code, m)
		}
	}
	h.ui(func() { h.d.app.Preferences().SetString(memoryModeKey, memoryModeKmem) })
	// locked 401, revoked 403
	locked := *a
	locked.pw, locked.tok = "", ""
	for _, p := range paths {
		if code, m, _ := locked.do("GET", p, "", nil); code != 401 || errCode(m) != "locked" {
			t.Fatalf("locked %s: %d %v", p, code, m)
		}
	}
	h.ui(func() { h.d.revokePhone(q.Get("i")) })
	for _, p := range paths {
		if code, m, _ := a.do("GET", p, "", nil); code != 403 || errCode(m) != "forbidden" {
			t.Fatalf("revoked %s: %d %v", p, code, m)
		}
	}
}
