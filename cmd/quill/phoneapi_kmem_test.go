package main

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// phoneFakeKmem: a fake kmem with placeholder data only (kmem rule 22).
// block, when set, holds `brief --json` until it is closed.
type phoneFakeKmem struct {
	mu    sync.Mutex
	calls []string
	block chan struct{}
	inBr  chan struct{} // gets a value when brief --json starts
}

const phoneKmemBrief = "rules: read, unchanged\nbin: 2 item(s) waiting for the user\n" +
	"maint: 3 h ago, 3 warnings (/home/user/.local/share/kmem/maint-20261122-114600.txt)\n" +
	"AUDIT PROBLEMS: 1 orphan in /home/user/Projects/demo\nnote by exampleuser, mail user@example.com\n"

func (k *phoneFakeKmem) run(prog string, args ...string) (string, error) {
	a := strings.Join(args, " ")
	k.mu.Lock()
	k.calls = append(k.calls, a)
	block, in := k.block, k.inBr
	k.mu.Unlock()
	switch a {
	case "version --json":
		return `{"version": "test", "api": 1}`, nil
	case "brief --json":
		if in != nil {
			in <- struct{}{}
		}
		if block != nil {
			<-block
		}
		b, _ := json.Marshal(map[string]any{"brief": phoneKmemBrief, "api": 1})
		return string(b), nil
	case "topics":
		return "demo/decisions  40 facts, 13 KB\ndemo/status  1 fact, 1 KB\nuser  2 facts, 1 KB\n", nil
	case "handoff list --json":
		return `{"handoffs": [{"name": "old-work", "items": 3, "saved": "2026-11-21T06:00:00-08:00"}], "api": 1}`, nil
	case "task list --json", "task list --all --json":
		done := ""
		if strings.Contains(a, "--all") {
			done = `, {"id": "t5", "prio": 2, "status": "done", "title": "Finished", "topic": "demo", "owner": null, "line": "[t5] p2 done: Finished  (demo)"}`
		}
		return `{"tasks": [` +
			`{"id": "t1", "prio": 4, "status": "open", "title": "Read /home/user/Projects/demo/notes.txt", "topic": "demo/sub", "owner": null, "line": "[t1] p4 open: x  (demo/sub)"},` +
			`{"id": "t2", "prio": 3, "status": "claimed", "title": "Mine", "topic": "demo", "owner": "demo", "line": "[t2] p3 claimed: Mine  (demo)  <- demo  [gone]"},` +
			`{"id": "t3", "prio": 1, "status": "open", "title": "kmem issue: lock stuck", "topic": "kmem", "owner": null, "line": "[t3] p1 open: kmem issue: lock stuck  (kmem)"},` +
			`{"id": "t4", "prio": 2, "status": "claimed", "title": "USER: check exampleuser settings", "topic": "other", "owner": "other", "line": "[t4] p2 claimed: USER: check  (other)  <- other"}` +
			done + `], "api": 1}`, nil
	case "rollout status --json":
		return `{"paused": null, "api": 1}`, nil
	}
	return "", nil
}

func (k *phoneFakeKmem) count(call string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for _, c := range k.calls {
		if c == call {
			n++
		}
	}
	return n
}

func TestPhoneKmemPure(t *testing.T) {
	oldH, oldU := kmemScrubHome, kmemScrubUser
	kmemScrubHome = func() string { return "/home/user" }
	kmemScrubUser = func() string { return "exampleuser" }
	defer func() { kmemScrubHome, kmemScrubUser = oldH, oldU }()
	sc := kmemScrubber()
	if got := sc("see /home/user/x by exampleuser (exampleusers) user@example.com"); got != "see ~/x by user (exampleusers) (e-mail)" {
		t.Fatalf("scrub: %q", got)
	}
	for line, want := range map[string]string{
		"[t] p1 claimed: a  (q)  <- q":                               "live",
		"[t] p1 claimed: a  (q)  <- q [gone]":                        "gone",
		"[t] p1 claimed: a  (q)  <- q [lease]":                       "lease",
		"[t] p1 claimed: a  (q)  <- q [lease expired]":               "leaseExpired",
		"[t] p1 claimed: a  (q)  <- q [handoff, waiting for --as q]": "handoff",
	} {
		if got := kmemClaim(kmemTask{Status: "claimed", Line: line}); got != want {
			t.Fatalf("claim of %q = %q, want %q", line, got, want)
		}
	}
	if kmemClaim(kmemTask{Status: "open", Line: "[t] p1 open: a"}) != "" {
		t.Fatal("open tasks have no claim")
	}
}

func TestPhoneKmemAPI(t *testing.T) {
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	oldH, oldU, oldC := kmemScrubHome, kmemScrubUser, kmemPhoneCacheFor
	kmemScrubHome = func() string { return "/home/user" }
	kmemScrubUser = func() string { return "exampleuser" }
	kmemPhoneCacheFor = time.Minute
	defer func() { kmemScrubHome, kmemScrubUser, kmemPhoneCacheFor = oldH, oldU, oldC }()

	h := newHarness(t)
	k := &phoneFakeKmem{}
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

	// hello's kmem entry
	_, hello, _ := a.do("POST", "/phone/hello", "hello-kmem-000000001", map[string]string{"name": "Test Pixel", "app": "1.0"})
	if km, _ := hello["kmem"].(map[string]any); km == nil || km["api"] != float64(1) || km["on"] != true {
		t.Fatalf("hello kmem: %v", hello)
	}

	raw := func(path string) (int, map[string]any, string) {
		code, m, _ := a.do("GET", path, "", nil)
		js, _ := json.Marshal(m)
		return code, m, string(js)
	}
	noPersonal := func(what, js string) {
		for _, bad := range []string{"/home/user", "exampleuser", "user@example.com", ".local/share"} {
			if strings.Contains(js, bad) {
				t.Fatalf("%s leaks %q: %s", what, bad, js)
			}
		}
	}

	// overview
	code, ov, js := raw("/phone/kmem/overview")
	if code != 200 || ov["v"] != float64(1) || ov["boot"] == "" {
		t.Fatalf("overview: %d %v", code, ov)
	}
	noPersonal("overview", js)
	needs := ov["needs"].(map[string]any)
	// bin 2 + audit 1 + maint 1 + issue t3 + user task t4 = 6
	if needs["count"] != float64(6) || needs["bin"] != float64(2) || needs["maint"] != float64(3) ||
		needs["maintReport"] != "maint-20261122-114600.txt" || needs["audit"] != "1 orphan in ~/Projects/demo" || needs["rolloutWait"] != "" {
		t.Fatalf("needs: %v", needs)
	}
	if is := needs["issues"].([]any); len(is) != 1 || is[0].(map[string]any)["id"] != "t3" || is[0].(map[string]any)["prio"] != float64(1) {
		t.Fatalf("issues: %v", needs["issues"])
	}
	if ut := needs["userTasks"].([]any); len(ut) != 1 || ut[0].(map[string]any)["title"] != "USER: check user settings" {
		t.Fatalf("user tasks: %v", needs["userTasks"])
	}
	as := ov["assistant"].(map[string]any)
	for _, f := range []string{"state", "waiting", "model", "modelSource", "rev", "epoch"} {
		if _, ok := as[f]; !ok {
			t.Fatalf("assistant misses %s: %v", f, as)
		}
	}
	if !strings.Contains(ov["brief"].(string), "~/Projects/demo") || !strings.Contains(ov["brief"].(string), "by user, mail (e-mail)") {
		t.Fatalf("brief: %q", ov["brief"])
	}
	if big := ov["bigTopics"].([]any); len(big) != 1 || big[0].(map[string]any)["name"] != "demo/decisions" || big[0].(map[string]any)["kb"] != float64(13) {
		t.Fatalf("bigTopics: %v", ov["bigTopics"])
	}
	if c := ov["counts"].(map[string]any); c["topics"] != float64(3) || c["handoffs"] != float64(1) || c["openTasks"] != float64(4) {
		t.Fatalf("counts: %v", c)
	}
	if ov["readMs"].(float64) <= 0 {
		t.Fatal("readMs")
	}
	h.eventually("the overview's read updates the PC's bubble too", func() bool { return h.d.kmemNeedsNow.Count() == 6 })

	// tasks and topics come from the same cached read
	code, ts, js := raw("/phone/kmem/tasks")
	if code != 200 || ts["v"] != float64(1) {
		t.Fatalf("tasks: %d %v", code, ts)
	}
	noPersonal("tasks", js)
	list := ts["tasks"].([]any)
	if len(list) != 4 || list[0].(map[string]any)["id"] != "t3" {
		t.Fatalf("tasks by priority: %v", list)
	}
	byID := map[string]map[string]any{}
	for _, x := range list {
		byID[x.(map[string]any)["id"].(string)] = x.(map[string]any)
	}
	if t2 := byID["t2"]; t2["claim"] != "gone" || t2["owner"] != "demo" || t2["status"] != "claimed" || t2["needsUser"] != false || t2["issue"] != false {
		t.Fatalf("t2: %v", t2)
	}
	if byID["t4"]["claim"] != "live" || byID["t4"]["needsUser"] != true || byID["t3"]["issue"] != true {
		t.Fatalf("t3/t4: %v %v", byID["t3"], byID["t4"])
	}
	if _, ok := byID["t1"]["owner"]; ok || byID["t1"]["title"] != "Read ~/Projects/demo/notes.txt" {
		t.Fatalf("t1: %v", byID["t1"])
	}
	if acts, _ := byID["t2"]["actions"].([]any); acts == nil || len(acts) != 0 {
		t.Fatalf("no task actions in phase 1: %v", byID["t2"]["actions"])
	}
	if tops, _ := json.Marshal(ts["topics"]); string(tops) != `["demo","kmem","other"]` {
		t.Fatalf("topics: %s", tops)
	}
	if _, f, _ := raw("/phone/kmem/tasks?topic=demo"); len(f["tasks"].([]any)) != 2 {
		t.Fatalf("topic filter includes subtopics: %v", f["tasks"])
	}
	code, tp, js := raw("/phone/kmem/topics")
	if code != 200 || tp["v"] != float64(1) || js == "" {
		t.Fatalf("topics: %d %v", code, tp)
	}
	if tl := tp["topics"].([]any); len(tl) != 3 || tl[0].(map[string]any)["name"] != "demo/decisions" || tl[0].(map[string]any)["facts"] != float64(40) || tl[0].(map[string]any)["kb"] != float64(13) {
		t.Fatalf("topics: %v", tl)
	}
	if n := k.count("brief --json"); n != 1 {
		t.Fatalf("overview, tasks and topics share one cached read: brief ran %d times", n)
	}
	if code, all, _ := raw("/phone/kmem/tasks?all=1"); code != 200 || len(all["tasks"].([]any)) != 5 || k.count("task list --all --json") != 1 {
		t.Fatalf("all=1: %d %v", code, all)
	}
	if code, m, _ := a.do("POST", "/phone/kmem/overview", "kmem-post-000000001", map[string]string{}); code == 200 {
		t.Fatalf("no kmem writes in phase 1: %d %v", code, m)
	}

	// the cache expires; one kmem read at a time per phone
	kmemPhoneCacheFor = 0
	block := make(chan struct{})
	in := make(chan struct{}, 4)
	k.mu.Lock()
	k.block, k.inBr = block, in
	k.mu.Unlock()
	first := make(chan int, 1)
	go func() {
		c, _, _ := a.do("GET", "/phone/kmem/overview", "", nil)
		first <- c
	}()
	select {
	case <-in:
	case <-time.After(10 * time.Second):
		t.Fatal("the expired cache reads kmem again")
	}
	if code, m, _ := a.do("GET", "/phone/kmem/tasks", "", nil); code != 409 || errCode(m) != "kmem_busy" {
		t.Fatalf("second read of the same phone: %d %v", code, m)
	}
	close(block)
	if c := <-first; c != 200 {
		t.Fatalf("first read: %d", c)
	}
	k.mu.Lock()
	k.block, k.inBr = nil, nil
	k.mu.Unlock()
	kmemPhoneCacheFor = time.Minute

	// the "kmem" stream change: once per change, not on every scan
	_, st, _ := a.do("GET", "/phone/sessions", "", nil)
	cursor := int64(st["cursor"].(float64))
	kmemChanges := func() ([]map[string]any, int64) {
		_, m, _ := a.do("GET", "/phone/stream?cursor="+strconv.FormatInt(cursor, 10)+"&wait=1", "", nil)
		var out []map[string]any
		for _, c := range m["changes"].([]any) {
			if c := c.(map[string]any); c["type"] == "kmem" {
				out = append(out, c)
			}
		}
		return out, int64(m["cursor"].(float64))
	}
	ch, next := kmemChanges()
	cursor = next
	if len(ch) > 1 {
		t.Fatalf("at most one kmem change so far: %v", ch)
	}
	h.ui(func() {
		n := h.d.kmemNeedsNow
		n.Bin = 9
		h.d.setKmemNeeds(n)
	})
	ch, cursor = kmemChanges()
	if len(ch) != 1 || ch[0]["needs"].(map[string]any)["count"] != float64(13) {
		t.Fatalf("needs change: %v", ch)
	}
	if as := ch[0]["assistant"].(map[string]any); as["state"] != "off" || as["waiting"] != "" {
		t.Fatalf("assistant in the change: %v", as)
	}
	if ch, _ := kmemChanges(); len(ch) != 0 {
		t.Fatalf("no change, no kmem change: %v", ch)
	}
	h.ui(func() {
		as := h.d.assist()
		as.state = asPanels
		h.d.kmemAssistReason(as, kmemAssistOffReason)
	})
	ch, cursor = kmemChanges()
	if len(ch) != 1 || ch[0]["assistant"].(map[string]any)["state"] != "panels" {
		t.Fatalf("assistant change: %v", ch)
	}
	_, ov, _ = raw("/phone/kmem/overview")
	if as := ov["assistant"].(map[string]any); as["state"] != "panels" || as["reason"] != kmemAssistOffReason {
		t.Fatalf("assistant reason line: %v", as)
	}
	if n := k.count("brief --json"); n != 2 {
		t.Fatalf("scans and stream never run kmem: brief ran %d times", n)
	}

	// kmem off -> 503 and hello's kmem.on false
	h.ui(func() { h.d.app.Preferences().SetString(memoryModeKey, memoryModeClaude) })
	if code, m, _ := a.do("GET", "/phone/kmem/overview", "", nil); code != 503 || errCode(m) != "kmem_unavailable" {
		t.Fatalf("kmem off: %d %v", code, m)
	}
	_, hello, _ = a.do("POST", "/phone/hello", "hello-kmem-000000002", map[string]string{"name": "Test Pixel", "app": "1.0"})
	if km, _ := hello["kmem"].(map[string]any); km == nil || km["on"] != false {
		t.Fatalf("hello kmem off: %v", hello)
	}
	h.ui(func() { h.d.app.Preferences().SetString(memoryModeKey, memoryModeKmem) })

	// same auth rules as the other phone reads: a locked phone without its
	// unlock token is refused (401 locked), a revoked phone gets 403
	locked := *a
	locked.pw, locked.tok = "", ""
	for _, p := range []string{"/phone/kmem/overview", "/phone/kmem/tasks", "/phone/kmem/topics"} {
		if code, m, _ := locked.do("GET", p, "", nil); code != 401 || errCode(m) != "locked" {
			t.Fatalf("locked %s: %d %v", p, code, m)
		}
	}
	h.ui(func() { h.d.revokePhone(q.Get("i")) })
	for _, p := range []string{"/phone/kmem/overview", "/phone/kmem/tasks", "/phone/kmem/topics"} {
		if code, m, _ := a.do("GET", p, "", nil); code != 403 || errCode(m) != "forbidden" {
			t.Fatalf("revoked %s: %d %v", p, code, m)
		}
	}
}
