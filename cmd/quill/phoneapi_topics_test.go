package main

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// topicPhone: a phone client against a harness in topic mode (kmem memory
// on). Placeholder data only. features: kmem 1.10.0 (end-check).
func topicPhone(t *testing.T, features bool) (*harness, *apiClient) {
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	h := newHarness(t)
	resetTopicCache()
	t.Cleanup(resetTopicCache)
	oldCfg := kmemCfgPath
	kmemCfgPath = func() string { return t.TempDir() + "/none.json" }
	resetKmemSessions()
	t.Cleanup(func() { kmemCfgPath = oldCfg; resetKmemSessions() })
	var mu sync.Mutex
	h.d.kmemOff = false
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		a := strings.Join(args, " ")
		switch {
		case prog != "kmem":
		case a == "version --json":
			if features {
				return `{"version": "1.10.0", "api": 1, "features": ["topic-context", "end-check", "sessions"]}`, nil
			}
			return `{"version": "1.9.1", "api": 1}`, nil
		case a == "brief --json":
			return `{"brief": "rules: read", "api": 1}`, nil
		case a == "topics":
			return "alpha/core  2 facts, 1 KB\nalpha/status  1 facts, 1 KB\nbeta/open  1 facts, 1 KB\n", nil
		case a == "task list --json":
			return `{"tasks": [` +
				`{"id": "t1", "prio": 3, "status": "claimed", "title": "Ship it", "topic": "alpha", "owner": "alpha", "checkpoint": "tests green, docs left", "line": "[t1] p3 claimed: Ship it  (alpha)  <- alpha"},` +
				`{"id": "t2", "prio": 2, "status": "open", "title": "Later", "topic": "alpha/core", "owner": null, "line": "[t2] p2 open: Later  (alpha/core)"},` +
				`{"id": "t3", "prio": 2, "status": "open", "title": "Other", "topic": "beta", "owner": null, "line": "[t3] p2 open: Other  (beta)"}], "api": 1}`, nil
		case a == "get alpha/status":
			return "[f1] fact: alpha is half done\n", nil
		case a == "handoff list --json":
			return `{"handoffs": [{"name": "old-work", "items": 3, "saved": "2026-11-21T06:00:00-08:00"}], "api": 1}`, nil
		case a == "rollout status --json":
			return `{"paused": null, "api": 1}`, nil
		}
		return "", errors.New("unexpected " + a)
	}
	h.d.kmemVer.reset()
	h.d.kmemAPILevel()
	h.d.loadTopics()
	var link string
	h.ui(func() {
		h.d.app.Preferences().SetString(memoryModeKey, memoryModeKmem)
		h.d.app.Preferences().SetBool("phoneControl", true)
		h.d.app.Preferences().SetInt(topicMigratedKey, 1) // the migration ran
		h.d.app.Preferences().SetInt("phonePort", 0)
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
		link = h.d.pairLink()
	})
	t.Cleanup(func() { h.ui(h.d.stopPhone) })
	u, _ := url.Parse(link)
	q := u.Query()
	a := unlockedClient(h, &apiClient{t: t, base: "https://127.0.0.1:" + q.Get("p"), key: q.Get("k"),
		c: &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}})
	return h, a
}

var topicKeyN int

// wkey: a fresh Idempotency-Key, spaced like the other write tests.
func wkey() string {
	topicKeyN++
	time.Sleep(110 * time.Millisecond)
	return "topic-key-" + strconv.Itoa(1000000+topicKeyN)
}

func topicByslug(m map[string]any, slug string) map[string]any {
	list, _ := m["topics"].([]any)
	for _, x := range list {
		if e, _ := x.(map[string]any); e["slug"] == slug {
			return e
		}
	}
	return nil
}

func sessionByID(m map[string]any, id string) map[string]any {
	list, _ := m["sessions"].([]any)
	for _, x := range list {
		if e, _ := x.(map[string]any); e["id"] == id {
			return e
		}
	}
	return nil
}

func TestPhoneTopicsHello(t *testing.T) {
	for _, features := range []bool{true, false} {
		_, a := topicPhone(t, features)
		code, m, _ := a.do("POST", "/phone/hello", wkey(), map[string]string{"name": "Tablet", "app": "quill-android"})
		tp, _ := m["topics"].(map[string]any)
		if code != 200 || tp["api"] != float64(1) || tp["on"] != features || tp["endCheck"] != features {
			t.Fatalf("hello topics (features %v): %d %v", features, code, m["topics"])
		}
	}
}

func TestPhoneTopicsSessions(t *testing.T) {
	h, a := topicPhone(t, true)
	var legacy *deskSession
	h.ui(func() {
		legacy = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[legacy.id] = "old-name"
	})
	code, m, _ := a.do("GET", "/phone/sessions", "", nil)
	if code != 200 {
		t.Fatalf("sessions: %d %v", code, m)
	}
	al := topicByslug(m, "alpha")
	if al == nil || al["title"] != "Alpha" || al["live"] != "" || al["status"] != "alpha is half done" || al["claimed"] != float64(1) || al["open"] != float64(1) {
		t.Fatalf("topics[] alpha: %v", m["topics"])
	}
	if topicByslug(m, "kmem") != nil || topicByslug(m, "beta") == nil {
		t.Fatalf("the kmem topic is not a tab: %v", m["topics"])
	}
	if sessionByID(m, editsID) != nil {
		t.Fatal("topic mode lists live sessions only; an old stopped one is not shown")
	}
	ls := sessionByID(m, legacy.id)
	if ls == nil || ls["legacyName"] != "old-name" || ls["topic"] != nil || ls["kmem"] != "old-name" {
		t.Fatalf("legacy session keeps kmem and gets legacyName: %v", ls)
	}
	// Open alpha: its session carries the topic fields, group = title.
	code, m, _ = a.do("POST", "/phone/sessions/new", wkey(), map[string]any{"openTopic": map[string]string{"topic": "alpha"}})
	ns, _ := m["session"].(map[string]any)
	if code != 200 || ns["topic"] != "alpha" || ns["topicTitle"] != "Alpha" || ns["group"] != "Alpha" || ns["legacyName"] != nil || m["switched"] != false {
		t.Fatalf("openTopic: %d %v", code, m)
	}
	id := ns["id"].(string)
	_, m, _ = a.do("GET", "/phone/sessions", "", nil)
	if al = topicByslug(m, "alpha"); al["live"] != id {
		t.Fatalf("topics[].live is the session id: %v", al)
	}
	if s := sessionByID(m, id); s == nil || s["group"] != "Alpha" || s["agents"] != nil || s["endWait"] != nil {
		t.Fatalf("no agents or endWait while there are none: %v", s)
	}
}

func TestPhoneTopicsAgentsAndEndWait(t *testing.T) {
	h, a := topicPhone(t, true)
	s := topicSession(h, "alpha")
	h.ui(func() {
		start := time.Now()
		h.d.agentStrip.now = func() time.Time { return start }
		feed(t, h, s, `{"type":"system","subtype":"task_started","task_id":"agentA","tool_use_id":"toolu_A","description":"Fix join stall","subagent_type":"Explore","task_type":"local_agent","is_backgrounded":true}`)
		feed(t, h, s, `{"type":"system","subtype":"task_progress","task_id":"agentA","tool_use_id":"toolu_A","description":"Fix join stall","subagent_type":"Explore","usage":{"total_tokens":41000,"tool_uses":7,"duration_ms":1000},"last_tool_name":"Grep"}`)
		feed(t, h, s, `{"type":"system","subtype":"task_started","task_id":"agentB","tool_use_id":"toolu_B","description":"Release notes","subagent_type":"general-purpose","task_type":"local_agent","is_backgrounded":true}`)
		feed(t, h, s, `{"type":"system","subtype":"task_progress","task_id":"agentB","tool_use_id":"toolu_B","description":"Release notes","usage":{"total_tokens":9000,"tool_uses":2,"duration_ms":1000},"last_tool_name":"Read"}`)
		s.endWait = &endWait{why: endForEnd, name: "alpha", asked: true, blockers: []endBlocker{
			{Kind: "claim", ID: "t1", Title: "Ship it", Need: "checkpoint or release"},
			{Kind: "lock", Name: "alpha:batch", Need: "release"}}}
	})
	_, m, _ := a.do("GET", "/phone/sessions", "", nil)
	ss := sessionByID(m, s.id)
	if ss == nil || ss["topic"] != "alpha" {
		t.Fatalf("session: %v", ss)
	}
	ag, _ := ss["agents"].([]any)
	if len(ag) != 2 || ss["agentTokens"] != float64(50000) {
		t.Fatalf("agents: %v tokens %v", ss["agents"], ss["agentTokens"])
	}
	first := ag[0].(map[string]any)
	if first["name"] != "Fix join stall" || first["description"] != "Fix join stall" || first["type"] != "Explore" || first["tokens"] != float64(41000) || first["done"] != false || first["startedMs"].(float64) <= 0 {
		t.Fatalf("agent shape: %v", first)
	}
	ew, _ := ss["endWait"].(map[string]any)
	bs, _ := ew["blockers"].([]any)
	if ew["why"] != "end" || ew["asked"] != true || ew["checking"] != false || len(bs) != 2 {
		t.Fatalf("endWait: %v", ew)
	}
	b0 := bs[0].(map[string]any)
	if b0["kind"] != "claim" || b0["id"] != "t1" || !strings.Contains(b0["text"].(string), "t1") || b0["need"] != "checkpoint or release" {
		t.Fatalf("blocker: %v", b0)
	}
	h.ui(func() { s.endWait = nil })
	_, m, _ = a.do("GET", "/phone/sessions", "", nil)
	if sessionByID(m, s.id)["endWait"] != nil {
		t.Fatal("endWait goes away with the check")
	}
}

func TestPhoneOpenTopicOneLive(t *testing.T) {
	h, a := topicPhone(t, true)
	open := func(body any) (int, map[string]any) {
		code, m, _ := a.do("POST", "/phone/sessions/new", wkey(), body)
		return code, m
	}
	sid := func(m map[string]any) string {
		s, _ := m["session"].(map[string]any)
		id, _ := s["id"].(string)
		return id
	}
	code, m := open(map[string]any{"openTopic": map[string]string{"topic": "alpha"}})
	if code != 200 || m["switched"] != false {
		t.Fatalf("first open: %d %v", code, m)
	}
	first := sid(m)
	var s *deskSession
	h.ui(func() { s = h.d.sessions[first] })
	h.eventually("fresh topic prompt", func() bool { return strings.Contains(sentText(s), "You are the Alpha coordinator") })
	// The same topic again, by slug or by a free name: the live session.
	for _, body := range []any{
		map[string]any{"openTopic": map[string]string{"topic": "alpha"}},
		map[string]any{"openTopic": map[string]string{"new": "ALPHA"}},
	} {
		if code, m = open(body); code != 200 || sid(m) != first || m["switched"] != true {
			t.Fatalf("one live session per topic: %d %v", code, m)
		}
	}
	h.ui(func() {
		n := 0
		for id, k := range h.d.org.Kmem {
			if k == "alpha" && h.d.sessions[id] != nil {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("%d sessions named alpha", n)
		}
	})
	// A new topic starts fresh with the new-topic prompt.
	code, m = open(map[string]any{"openTopic": map[string]string{"new": "Garden Planner"}})
	if code != 200 || m["switched"] != false || sid(m) == first {
		t.Fatalf("new topic: %d %v", code, m)
	}
	tp, _ := m["topic"].(map[string]any)
	if tp["slug"] != "garden-planner" || tp["title"] != "Garden Planner" {
		t.Fatalf("topic: %v", tp)
	}
	var g *deskSession
	h.ui(func() { g = h.d.sessions[sid(m)] })
	h.eventually("new topic prompt", func() bool { return strings.Contains(sentText(g), "This topic is new") })
	if code, m = open(map[string]any{"openTopic": map[string]string{"topic": "garden-planner"}}); code != 200 || sid(m) != g.id || m["switched"] != true {
		t.Fatalf("a new topic is live too: %d %v", code, m)
	}
	// Errors.
	for _, c := range []struct {
		body any
		code int
		err  string
	}{
		{map[string]any{"openTopic": map[string]string{}}, 422, "bad_request"},
		{map[string]any{"openTopic": map[string]string{"topic": "alpha", "new": "X"}}, 422, "bad_request"},
		{map[string]any{"openTopic": map[string]string{"topic": "nope"}}, 404, "not_found"},
		{map[string]any{"openTopic": map[string]string{"topic": "kmem"}}, 422, "bad_request"},
		{map[string]any{"openTopic": map[string]string{"new": "kmem"}}, 422, "bad_request"},
		{map[string]any{"openTopic": map[string]string{"topic": "beta"}, "folder": "/nowhere"}, 400, "bad_request"},
	} {
		if code, m = open(c.body); code != c.code || errCode(m) != c.err {
			t.Fatalf("%v: %d %v", c.body, code, m)
		}
	}
	// Control off refuses it like any write.
	h.ui(func() { h.d.app.Preferences().SetBool("phoneControl", false) })
	if code, m = open(map[string]any{"openTopic": map[string]string{"topic": "beta"}}); code != 403 || errCode(m) != "control_off" {
		t.Fatalf("control off: %d %v", code, m)
	}
}

func TestPhoneOpenTopicKmemOff(t *testing.T) {
	h, a := topicPhone(t, true)
	h.ui(func() { h.d.app.Preferences().SetString(memoryModeKey, memoryModeClaude) })
	if code, m, _ := a.do("POST", "/phone/sessions/new", wkey(), map[string]any{"openTopic": map[string]string{"topic": "alpha"}}); code != 503 || errCode(m) != "kmem_unavailable" {
		t.Fatalf("openTopic without topic mode: %d %v", code, m)
	}
	for _, p := range []string{"/phone/topics", "/phone/topics/slug?title=x"} {
		if code, m, _ := a.do("GET", p, "", nil); code != 503 || errCode(m) != "kmem_unavailable" {
			t.Fatalf("%s: %d %v", p, code, m)
		}
	}
	_, m, _ := a.do("GET", "/phone/sessions", "", nil)
	if _, has := m["topics"]; has {
		t.Fatalf("outside topic mode the sessions list is as before: %v", m["topics"])
	}
}

func TestPhoneOldStartRefused(t *testing.T) {
	h, a := topicPhone(t, true)
	folder := ""
	h.ui(func() { folder = h.proj })
	h.ui(func() { h.d.createSession(h.proj, "default", "default") })
	for _, st := range []map[string]string{{"kmem": "new", "title": "Tablet Thing"}, {"kmem": "resume", "name": "old-work"}, {"kmem": "none"}} {
		code, m, _ := a.do("POST", "/phone/sessions/new", wkey(), map[string]any{"folder": folder, "start": st})
		e, _ := m["error"].(map[string]any)
		if code != 426 || errCode(m) != "update_required" || !strings.Contains(e["message"].(string), "Update Quill Android") {
			t.Fatalf("start %v: %d %v", st, code, m)
		}
	}
	code, m, _ := a.do("GET", "/phone/handoffs", "", nil)
	if list, _ := m["handoffs"].([]any); code != 200 || len(list) != 0 || m["readOnly"] != true {
		t.Fatalf("handoffs are read-only and empty of resumable entries: %d %v", code, m)
	}
	for _, p := range []string{"/phone/handoffs/old-work/resume", "/phone/handoffs/old-work/delete"} {
		if code, m, _ = a.do("POST", p, wkey(), map[string]any{}); code != 426 || errCode(m) != "update_required" {
			t.Fatalf("%s: %d %v", p, code, m)
		}
	}
	// A plain new session in a known folder still works for older phones.
	if code, m, _ = a.do("POST", "/phone/sessions/new", wkey(), map[string]any{"folder": folder}); code != 200 {
		t.Fatalf("plain new session: %d %v", code, m)
	}
}

func TestPhoneTopicsView(t *testing.T) {
	_, a := topicPhone(t, true)
	code, m, _ := a.do("GET", "/phone/topics", "", nil)
	if code != 200 {
		t.Fatalf("topics: %d %v", code, m)
	}
	al := topicByslug(m, "alpha")
	if al == nil || al["status"] != "alpha is half done" || al["claimed"] != float64(1) || al["open"] != float64(1) || al["live"] != "" {
		t.Fatalf("alpha: %v", al)
	}
	tasks, _ := al["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("claimed tasks: %v", al["tasks"])
	}
	t1 := tasks[0].(map[string]any)
	if t1["id"] != "t1" || t1["title"] != "Ship it" || t1["owner"] != "alpha" || t1["checkpoint"] != "tests green, docs left" || t1["claim"] != "live" {
		t.Fatalf("task: %v", t1)
	}
	if be := topicByslug(m, "beta"); be == nil || len(be["tasks"].([]any)) != 0 {
		t.Fatalf("beta: %v", be)
	}
	hist, _ := m["history"].([]any)
	if len(hist) != 1 || hist[0].(map[string]any)["name"] != "old-work" || hist[0].(map[string]any)["items"] != float64(3) || hist[0].(map[string]any)["savedMs"].(float64) <= 0 {
		t.Fatalf("history: %v", m["history"])
	}
}

func TestPhoneTopicSlugAndFolders(t *testing.T) {
	h, a := topicPhone(t, true)
	code, m, _ := a.do("GET", "/phone/topics/slug?title=Garden+Planner", "", nil)
	if code != 200 || m["slug"] != "garden-planner" || m["exists"] != false {
		t.Fatalf("new slug: %d %v", code, m)
	}
	if _, m, _ = a.do("GET", "/phone/topics/slug?title=ALPHA", "", nil); m["slug"] != "alpha" || m["exists"] != true || m["title"] != "Alpha" {
		t.Fatalf("existing topic: %v", m)
	}
	if code, m, _ = a.do("GET", "/phone/topics/slug?title=kmem", "", nil); code != 422 {
		t.Fatalf("kmem: %d %v", code, m)
	}
	extra := filepath.Join(t.TempDir(), "extra")
	h.ui(func() { h.d.org.Dirs = map[string][]string{"Alpha": {extra}} })
	code, m, _ = a.do("GET", "/phone/folders?topic=alpha", "", nil)
	fl, _ := m["folders"].([]any)
	if code != 200 || m["topic"] != "alpha" || len(fl) != 1 || fl[0].(map[string]any)["path"] != extra || fl[0].(map[string]any)["name"] != "extra" {
		t.Fatalf("topic folders (project folders only while not live): %d %v", code, m)
	}
	// Live: the coordinator's folder comes first; the folder is accepted.
	code, m, _ = a.do("POST", "/phone/sessions/new", wkey(), map[string]any{"openTopic": map[string]string{"topic": "alpha"}, "folder": extra})
	ns, _ := m["session"].(map[string]any)
	if code != 200 || ns["folder"] != extra {
		t.Fatalf("openTopic with a topic folder: %d %v", code, m)
	}
	_, m, _ = a.do("GET", "/phone/folders?topic=alpha", "", nil)
	if fl, _ = m["folders"].([]any); len(fl) != 1 || fl[0].(map[string]any)["path"] != extra {
		t.Fatalf("live coordinator folder: %v", m)
	}
	if code, m, _ = a.do("GET", "/phone/folders?topic=nope", "", nil); code != 404 {
		t.Fatalf("unknown topic: %d %v", code, m)
	}
	// Without topic: every known folder, as before.
	_, m, _ = a.do("GET", "/phone/folders", "", nil)
	if fl, _ = m["folders"].([]any); len(fl) < 2 || m["topic"] != nil {
		t.Fatalf("all folders: %v", m)
	}
}

// Topic mode lists live sessions only: a stopped one that is no open tab is
// left out, and shows again outside topic mode.
func TestPhoneTopicsListLiveOnly(t *testing.T) {
	h, a := topicPhone(t, true)
	h.ui(func() { h.d.groups = append(h.d.groups, bookGroup{key: "old", title: "Old", ids: []string{editsID}}) })
	_, m, _ := a.do("GET", "/phone/sessions", "", nil)
	if sessionByID(m, editsID) != nil {
		t.Fatal("a stopped session without a tab is not listed in topic mode")
	}
	h.ui(func() { h.d.app.Preferences().SetString(memoryModeKey, memoryModeClaude) })
	_, m, _ = a.do("GET", "/phone/sessions", "", nil)
	if sessionByID(m, editsID) == nil {
		t.Fatal("outside topic mode it is listed")
	}
}

// Today's state, kmem memory on but topic tabs not (kmem without end-check, or
// the topic migration not run): the phone API behaves exactly as before.
func TestPhoneTopicModeGate(t *testing.T) {
	for _, c := range []struct {
		name     string
		features bool
		marker   int
	}{{"no end-check feature", false, 1}, {"no migration marker", true, 0}} {
		t.Run(c.name, func(t *testing.T) {
			h, a := topicPhone(t, c.features)
			folder := ""
			h.ui(func() {
				h.d.app.Preferences().SetInt(topicMigratedKey, c.marker)
				folder = h.proj
				h.d.groups = append(h.d.groups, bookGroup{key: "old", title: "Old", ids: []string{editsID}})
			})
			_, m, _ := a.do("POST", "/phone/hello", wkey(), map[string]string{"name": "Tablet", "app": "quill-android"})
			tp, _ := m["topics"].(map[string]any)
			if tp["on"] != false || m["kmem"].(map[string]any)["on"] != true {
				t.Fatalf("hello: topics off, kmem on: %v", m)
			}
			_, m, _ = a.do("GET", "/phone/sessions", "", nil)
			if _, has := m["topics"]; has || sessionByID(m, editsID) == nil {
				t.Fatalf("sessions as before (not live-only, no topics): %v", m["topics"])
			}
			code, m, _ := a.do("GET", "/phone/handoffs", "", nil)
			if list, _ := m["handoffs"].([]any); code != 200 || len(list) != 1 || m["readOnly"] != nil {
				t.Fatalf("handoffs as before: %d %v", code, m)
			}
			code, m, _ = a.do("POST", "/phone/sessions/new", wkey(), map[string]any{"folder": folder, "start": map[string]string{"kmem": "new", "title": "Tablet Thing"}})
			if code != 200 {
				t.Fatalf("start.kmem still works: %d %v", code, m)
			}
			for _, p := range []string{"/phone/topics", "/phone/topics/slug?title=x"} {
				if code, m, _ = a.do("GET", p, "", nil); code != 503 {
					t.Fatalf("%s: %d %v", p, code, m)
				}
			}
			if code, m, _ = a.do("POST", "/phone/sessions/new", wkey(), map[string]any{"openTopic": map[string]string{"topic": "alpha"}}); code != 503 {
				t.Fatalf("openTopic: %d %v", code, m)
			}
		})
	}
}

func TestPhoneTopicSlugNonLatin(t *testing.T) {
	_, a := topicPhone(t, true)
	resetTopicExtra(t)
	for i, title := range nonLatinNames {
		want := "topic-" + string(rune('1'+i))
		q := url.QueryEscape(title)
		code, m, _ := a.do("GET", "/phone/topics/slug?title="+q, "", nil)
		if code != 200 || m["slug"] != want || m["exists"] != false {
			t.Fatalf("slug %q: %d %v", title, code, m)
		}
		code, m, _ = a.do("POST", "/phone/sessions/new", wkey(), map[string]any{"openTopic": map[string]string{"new": title}})
		tp, _ := m["topic"].(map[string]any)
		if code != 200 || tp["slug"] != want || tp["title"] != strings.TrimSpace(title) {
			t.Fatalf("open %q: %d %v", title, code, m)
		}
	}
}
