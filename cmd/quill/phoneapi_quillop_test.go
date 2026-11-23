package main

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Quill Android and the Quill operator (docs/design/quill-operator.md §4,
// §7 row G): the operator's phone routes. Placeholder data only.

type quillPhone struct {
	h *harness
	a *apiClient
	n int
}

func quillPhoneSetup(t *testing.T) *quillPhone {
	h, _ := quillSetup(t)
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	var link string
	h.ui(func() {
		p := h.d.app.Preferences()
		p.SetInt("phonePort", 0)
		p.SetInt(topicMigratedKey, 1)
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
		link = h.d.pairLink()
		h.d.foreground = false // nobody looks at Quill Desktop: phone events go out
	})
	t.Cleanup(func() { h.ui(h.d.stopPhone) })
	u, _ := url.Parse(link)
	q := u.Query()
	a := unlockedClient(h, &apiClient{t: t, base: "https://127.0.0.1:" + q.Get("p"), key: q.Get("k"),
		c: &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}})
	return &quillPhone{h: h, a: a}
}

// seedQuillTopic: topic quill in the (fresh) topic cache, as kmem lists it.
func seedQuillTopic() {
	topicCache.mu.Lock()
	topicCache.list = append(topicCache.list, topicInfo{Slug: quillOpName, Title: "Quill", Scope: []string{quillOpName}})
	topicCache.loaded, topicCache.at = true, time.Now()
	topicCache.mu.Unlock()
}

func (p *quillPhone) post(path string, body any) (int, map[string]any) {
	p.n++
	time.Sleep(110 * time.Millisecond)
	code, m, _ := p.a.do("POST", "/phone/quill/"+path, "quill-op-"+strconv.Itoa(1000000+p.n), body)
	return code, m
}

func (p *quillPhone) get(path string) (int, map[string]any) {
	code, m, _ := p.a.do("GET", "/phone/quill"+path, "", nil)
	return code, m
}

func (p *quillPhone) op(m map[string]any) map[string]any {
	o, _ := m["operator"].(map[string]any)
	return o
}

func (p *quillPhone) events() []map[string]any {
	_, m, _ := p.a.do("GET", "/events?since=0", "", nil)
	var out []map[string]any
	evs, _ := m["events"].([]any)
	for _, e := range evs {
		if e := e.(map[string]any); e["type"] == "quill" {
			out = append(out, e)
		}
	}
	return out
}

func TestPhoneQuillOperator(t *testing.T) {
	p := quillPhoneSetup(t)
	h, a := p.h, p.a

	// Before it runs: status off, Start offered, empty chat.
	code, m := p.get("")
	if code != 200 || m["v"] != float64(1) || m["state"] != "off" || m["status"] != "off" || !hasAction(m, "start") || hasAction(m, "stop") {
		t.Fatalf("GET /phone/quill before a start: %d %v", code, m)
	}
	if ch, ok := m["changes"].([]any); !ok || len(ch) != 0 {
		t.Fatalf("no changes yet: %v", m["changes"])
	}
	if code, m := p.get("/operator/items"); code != 200 || m["id"] != quillOpPhoneID || len(m["items"].([]any)) != 0 || p.op(m)["state"] != "off" {
		t.Fatalf("empty items: %d %v", code, m)
	}
	// Every write needs control mode.
	for _, op := range quillOpOps {
		if code, m := p.post("operator/"+op, map[string]any{"text": "hi"}); code != 403 || errCode(m) != "control_off" {
			t.Fatalf("%s with control off: %d %v", op, code, m)
		}
	}
	if code, m := p.post("settings/undo", map[string]any{"change": 1}); code != 403 || errCode(m) != "control_off" {
		t.Fatalf("undo with control off: %d %v", code, m)
	}
	h.ui(func() { h.d.app.Preferences().SetBool("phoneControl", true) })
	for _, op := range []string{"stop", "restart", "send"} {
		if code, m := p.post("operator/"+op, map[string]any{"text": "hi"}); code != 409 {
			t.Fatalf("%s before a start: %d %v", op, code, m)
		}
	}

	// Start, as the Quill button.
	if code, m := p.post("operator/start", map[string]any{}); code != 200 || p.op(m) == nil {
		t.Fatalf("start: %d %v", code, m)
	}
	var s *deskSession
	h.eventually("the operator runs, idle", func() bool {
		as := h.d.quillAssist()
		s = as.s
		return s != nil && as.state == asLive && s.running() && !s.conv.Busy && !s.inTurn
	})
	if code, m := p.post("operator/start", map[string]any{}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("start while it runs: %d %v", code, m)
	}
	_, m = p.get("")
	if m["state"] != "live" || m["status"] != "idle" || p.op(m)["dot"] != true || !hasAction(m, "stop") || !hasAction(m, "send") || hasAction(m, "start") {
		t.Fatalf("status of the running operator: %v", m)
	}

	// Hidden from the session list and the session routes.
	_, sess, _ := a.do("GET", "/phone/sessions", "", nil)
	if sessionByID(sess, s.id) != nil {
		t.Fatal("the operator is not in /phone/sessions")
	}
	if code, _, _ := a.do("GET", "/phone/session/"+s.id+"/items", "", nil); code != 404 {
		t.Fatalf("session items refuse the operator: %d", code)
	}

	// items: shape and paging.
	_, m = p.get("/operator/items")
	if len(m["items"].([]any)) == 0 || m["epoch"].(float64) == 0 {
		t.Fatalf("items of the running operator: %v", m)
	}
	epoch, rev := int(m["epoch"].(float64)), int(m["rev"].(float64))
	_, m = p.get("/operator/items?epoch=" + strconv.Itoa(epoch) + "&since=" + strconv.Itoa(rev))
	if len(m["items"].([]any)) != 0 || m["reset"] != false {
		t.Fatalf("nothing new since rev: %v", m)
	}
	if code, _ := p.get("/operator/items?limit=x"); code != 400 {
		t.Fatal("bad query")
	}

	// send -> a permission: status waiting, the phone event, allow via answer.
	if code, m := p.post("operator/send", map[string]any{"text": "write"}); code != 200 || m["queued"] != false {
		t.Fatalf("send: %d %v", code, m)
	}
	var reqID string
	h.eventually("the operator asks for permission", func() bool {
		for _, it := range s.conv.PendingPermissions() {
			reqID = it.RequestID
		}
		return reqID != ""
	})
	_, m = p.get("")
	if m["status"] != "waiting" || m["waiting"] != "permission" {
		t.Fatalf("waiting for a permission: %v", m)
	}
	var evs []map[string]any
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if evs = p.events(); len(evs) == 1 {
			break
		}
	}
	if len(evs) != 1 || evs[0]["title"] != "Quill operator" || evs[0]["quill"] != "" || !strings.HasPrefix(evs[0]["body"].(string), "needs approval: Write") {
		t.Fatalf("the notification is labelled Quill operator: %v", evs)
	}
	code, m = p.post("operator/answer", map[string]any{"requestId": reqID, "action": "allow"})
	if req, _ := m["item"].(map[string]any)["request"].(map[string]any); code != 200 || req["state"] != "allowed" {
		t.Fatalf("allow: %d %v", code, m)
	}
	if code, m := p.post("operator/answer", map[string]any{"requestId": reqID, "action": "deny"}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("answered already: %d %v", code, m)
	}
	h.eventually("the turn ends", func() bool { return !s.conv.Busy && !s.inTurn })

	// queue and interrupt.
	h.ui(func() { s.queue = []string{"first"} })
	if code, m := p.post("operator/queue/delete", map[string]any{"index": 0, "text": "first"}); code != 200 || len(m["queue"].([]any)) != 0 {
		t.Fatalf("queue delete: %d %v", code, m)
	}
	if code, m := p.post("operator/interrupt", map[string]any{}); code != 200 || m["wasBusy"] != false {
		t.Fatalf("interrupt: %d %v", code, m)
	}
	if code, m := p.post("operator/send", map[string]any{"text": "/login"}); code != 409 || errCode(m) != "blocked" {
		t.Fatalf("desktop-only commands stay blocked: %d %v", code, m)
	}

	// The kmem assistant's routes and stream change are not the operator's.
	if code, m, _ := a.do("GET", "/phone/kmem/assistant/items", "", nil); code != 200 || m["id"] != kmemAssistPhoneID {
		t.Fatalf("kmem assistant items stay: %d %v", code, m)
	}
}

func TestPhoneQuillSettingsUndo(t *testing.T) {
	p := quillPhoneSetup(t)
	h := p.h
	h.ui(func() { h.d.app.Preferences().SetBool("phoneControl", true) })
	var c1, c2 settingChange
	h.ui(func() {
		var err error
		if c1, err = h.d.changeSetting(nil, "animateSpeed", "fast", "make it faster", byOperator); err != nil {
			t.Fatal(err)
		}
		if c2, err = h.d.changeSetting(nil, "showMsgTimes", "off", "hide the message times", byOperator); err != nil {
			t.Fatal(err)
		}
	})
	code, m := p.get("/settings/changes")
	list, _ := m["changes"].([]any)
	if code != 200 || len(list) != 2 {
		t.Fatalf("changes list: %d %v", code, m)
	}
	first, _ := list[0].(map[string]any) // newest first
	if first["n"] != float64(c2.N) || first["id"] != "showMsgTimes" || first["canUndo"] != true || first["undone"] != false || first["why"] != "hide the message times" {
		t.Fatalf("newest change: %v", first)
	}
	if _, m := p.get(""); len(m["changes"].([]any)) != 2 {
		t.Fatalf("GET /phone/quill carries the changes: %v", m["changes"])
	}

	// Undo through the phone restores the old value.
	if code, m := p.post("settings/undo", map[string]any{}); code != 400 {
		t.Fatalf("undo needs a change number: %d %v", code, m)
	}
	code, m = p.post("settings/undo", map[string]any{"change": c2.N})
	if code != 200 || m["undone"] != float64(c2.N) {
		t.Fatalf("undo: %d %v", code, m)
	}
	h.ui(func() {
		if s, _ := settingByID("showMsgTimes"); s.get(h.d) != c2.Old {
			t.Fatalf("old value restored: %q", s.get(h.d))
		}
	})
	_, m = p.get("/settings/changes")
	if e := m["changes"].([]any)[0].(map[string]any); e["undone"] != true || e["canUndo"] != false {
		t.Fatalf("undone entry: %v", e)
	}
	if code, m := p.post("settings/undo", map[string]any{"change": c2.N}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("undo twice: %d %v", code, m)
	}
	if code, m := p.post("settings/undo", map[string]any{"change": 99}); code != 404 {
		t.Fatalf("no such change: %d %v", code, m)
	}
	// A setting changed again since: nothing undone.
	h.ui(func() { h.d.changeSetting(nil, "animateSpeed", "slow", "slower", byOperator) })
	if code, m := p.post("settings/undo", map[string]any{"change": c1.N}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("changed again since: %d %v", code, m)
	}
}

func TestPhoneQuillSetNotExposed(t *testing.T) {
	p := quillPhoneSetup(t)
	h := p.h
	h.ui(func() { h.d.app.Preferences().SetBool("phoneControl", true) })
	body := map[string]any{"id": "animateSpeed", "value": "fast", "text": "fast", "change": 1}
	for _, path := range []string{"settings/set", "settings", "operator/set", "set", "settings/change"} {
		if code, m := p.post(path, body); code != 404 && code != 405 {
			t.Fatalf("POST /phone/quill/%s must not exist: %d %v", path, code, m)
		}
	}
	h.ui(func() {
		if s, _ := settingByID("animateSpeed"); len(h.d.settingsChanges()) != 0 || s.get(h.d) == "fast" {
			t.Fatal("no setting changed through the phone")
		}
	})
	if slices.Contains(quillOpOps, "set") {
		t.Fatal("set is not an operator op")
	}
}

func TestPhoneQuillAdvertised(t *testing.T) {
	p := quillPhoneSetup(t)
	h, a := p.h, p.a
	seedQuillTopic()

	// hello and the sessions actions: older Quill Android ignores both and
	// hides the operator.
	code, m, _ := a.do("POST", "/phone/hello", wkey(), map[string]string{"name": "Tablet", "app": "quill-android"})
	if q, _ := m["quillOperator"].(map[string]any); code != 200 || q["api"] != float64(1) || q["on"] != true {
		t.Fatalf("hello quillOperator: %d %v", code, m["quillOperator"])
	}
	_, sess, _ := a.do("GET", "/phone/sessions", "", nil)
	acts, _ := sess["actions"].([]any)
	if !slices.Contains(acts, any("quillOperator")) || !slices.Contains(acts, any("rename")) {
		t.Fatalf("actions advertise quillOperator: %v", acts)
	}
	// topics[] (GET /phone/topics shares topicEntry): the quill topic is live: "operator".
	var quill map[string]any
	if quill = topicByslug(sess, quillOpName); quill == nil || quill["live"] != "operator" {
		t.Fatalf("topics[] marks quill live operator: %v", quill)
	}

	// Off: no capability for the operator.
	h.ui(func() { h.d.app.Preferences().SetString(prefQuillOpModel, kmemAssistOff) })
	_, m, _ = a.do("POST", "/phone/hello", wkey(), map[string]string{"name": "Tablet", "app": "quill-android"})
	if q, _ := m["quillOperator"].(map[string]any); q["on"] != false {
		t.Fatalf("hello with the operator off: %v", q)
	}
	_, sess, _ = a.do("GET", "/phone/sessions", "", nil)
	if acts, _ := sess["actions"].([]any); slices.Contains(acts, any("quillOperator")) || len(acts) != len(menuActions) {
		t.Fatalf("no quillOperator action while off: %v", acts)
	}
	if code, m := p.get(""); code != 503 || errCode(m) != "quill_unavailable" {
		t.Fatalf("GET /phone/quill while off: %d %v", code, m)
	}
}

// openTopic quill while the operator starts: a nil session, never a crash.
func TestPhoneOpenTopicQuillStarting(t *testing.T) {
	p := quillPhoneSetup(t)
	h, a := p.h, p.a
	h.ui(func() { h.d.app.Preferences().SetBool("phoneControl", true) })
	seedQuillTopic()

	var out map[string]any
	var fail *apiErr
	h.ui(func() {
		// Direct: the probe is asynchronous, so no session exists yet.
		out, fail = (&phoneAPI{d: h.d}).openTopicWrite(writeBody{OpenTopic: &apiOpenTopic{Topic: quillOpName}})
		if s := h.d.quillAssist().s; s != nil {
			t.Log("operator already has a session; the starting flag is then", out["starting"])
		}
	})
	if fail != nil || out["operator"] != true || out["switched"] != true || out["session"] != nil {
		t.Fatalf("openTopic quill while it starts: %v %v", out, fail)
	}
	if out["topic"].(map[string]string)["slug"] != quillOpName {
		t.Fatalf("topic: %v", out)
	}

	// Over HTTP, once it runs: the same answer, still no session.
	h.eventually("the operator runs", func() bool {
		as := h.d.quillAssist()
		return as.s != nil && as.state == asLive && as.s.running() && !as.s.conv.Busy && !as.s.inTurn
	})
	code, m, _ := a.do("POST", "/phone/sessions/new", wkey(), map[string]any{"openTopic": map[string]string{"topic": quillOpName}})
	if code != 200 || m["operator"] != true || m["switched"] != true || m["starting"] != false || m["session"] != nil {
		t.Fatalf("openTopic quill: %d %v", code, m)
	}
}

func TestPhoneQuillStreamChange(t *testing.T) {
	p := quillPhoneSetup(t)
	h := p.h
	a := p.a
	_, st, _ := a.do("GET", "/phone/sessions", "", nil)
	cursor := int64(st["cursor"].(float64))
	quillChanges := func() []map[string]any {
		_, m, _ := a.do("GET", "/phone/stream?cursor="+strconv.FormatInt(cursor, 10)+"&wait=1", "", nil)
		cursor = int64(m["cursor"].(float64))
		var out []map[string]any
		for _, c := range m["changes"].([]any) {
			if c := c.(map[string]any); c["type"] == "quill" {
				out = append(out, c)
			}
		}
		return out
	}
	quillChanges() // the first one
	h.ui(func() { h.d.changeSetting(nil, "animateSpeed", "fast", "faster", byOperator) })
	var ch []map[string]any
	for i := 0; i < 5 && len(ch) == 0; i++ {
		ch = quillChanges()
	}
	if len(ch) != 1 || ch[0]["lastChange"] != float64(1) || ch[0]["needs"] == nil {
		t.Fatalf("quill change after a settings change: %v", ch)
	}
	if o, _ := ch[0]["operator"].(map[string]any); o["state"] != "off" || o["waiting"] != "" {
		t.Fatalf("operator in the change: %v", o)
	}
}
