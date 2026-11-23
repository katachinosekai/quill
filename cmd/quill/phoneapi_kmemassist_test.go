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

	"github.com/katachinosekai/quill/internal/chat"
)

// kmem on Quill Android, phase 2 (docs/design/kmem-android.md §4.3, §4.5,
// §5): the kmem assistant's routes. Placeholder data only (kmem rule 22).

type kmemAssistPhone struct {
	h *harness
	k *kmemAssistStub
	a *apiClient
	q url.Values
	n int
}

func kmemAssistPhoneSetup(t *testing.T) *kmemAssistPhone {
	h, k, _ := assistSetup(t)
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	var link string
	h.ui(func() {
		h.d.app.Preferences().SetInt("phonePort", 0)
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
	return &kmemAssistPhone{h: h, k: k, a: a, q: q}
}

// post sends one write with a fresh Idempotency-Key, under the 10 writes/s limit.
func (p *kmemAssistPhone) post(op string, body any) (int, map[string]any) {
	p.n++
	time.Sleep(110 * time.Millisecond)
	code, m, _ := p.a.do("POST", "/phone/kmem/assistant/"+op, "kmem-assist-"+strconv.Itoa(1000000+p.n), body)
	return code, m
}

func (p *kmemAssistPhone) items(query string) map[string]any {
	code, m, _ := p.a.do("GET", "/phone/kmem/assistant/items"+query, "", nil)
	if code != 200 || m["v"] != float64(1) || m["id"] != kmemAssistPhoneID {
		p.h.t.Fatalf("items: %d %v", code, m)
	}
	return m
}

func assistOf(m map[string]any) map[string]any {
	as, _ := m["assistant"].(map[string]any)
	return as
}

func hasAction(as map[string]any, action string) bool {
	acts, _ := as["actions"].([]any)
	return slices.Contains(acts, any(action))
}

// kmemEvents: the phone events of type "kmem" so far.
func (p *kmemAssistPhone) kmemEvents() []map[string]any {
	_, m, _ := p.a.do("GET", "/events?since=0", "", nil)
	var out []map[string]any
	evs, _ := m["events"].([]any)
	for _, e := range evs {
		if e := e.(map[string]any); e["type"] == "kmem" {
			out = append(out, e)
		}
	}
	return out
}

// waitEvents polls the kmem events (off the UI thread: the phone server
// needs it) until ok.
func (p *kmemAssistPhone) waitEvents(what string, ok func([]map[string]any) bool) []map[string]any {
	p.h.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if ev := p.kmemEvents(); ok(ev) {
			return ev
		}
	}
	p.h.t.Fatalf("timeout waiting for %s", what)
	return nil
}

func (p *kmemAssistPhone) idle() *deskSession {
	var s *deskSession
	p.h.eventually("the assistant runs, idle", func() bool {
		as := p.h.d.assist()
		s = as.s
		return s != nil && as.state == asLive && s.running() && !s.conv.Busy && !s.inTurn
	})
	return s
}

func TestPhoneKmemAssistant(t *testing.T) {
	p := kmemAssistPhoneSetup(t)
	h, a := p.h, p.a

	// Before it ever ran: an empty chat, Start offered.
	m := p.items("")
	if len(m["items"].([]any)) != 0 || assistOf(m)["state"] != "off" || !hasAction(assistOf(m), "start") || hasAction(assistOf(m), "stop") {
		t.Fatalf("no assistant yet: %v", m)
	}
	// Every write needs control mode.
	for _, op := range kmemAssistOps {
		if code, m := p.post(op, map[string]any{"text": "hi", "choice": 0, "index": 0}); code != 403 || errCode(m) != "control_off" {
			t.Fatalf("%s with control off: %d %v", op, code, m)
		}
	}
	h.ui(func() { h.d.app.Preferences().SetBool("phoneControl", true) })
	if code, m := p.post("nope", map[string]any{}); code != 404 {
		t.Fatalf("unknown op: %d %v", code, m)
	}
	for _, op := range []string{"stop", "restart", "save", "choose", "send"} {
		if code, m := p.post(op, map[string]any{"choice": 0, "text": "hi"}); code != 409 {
			t.Fatalf("%s before a start: %d %v", op, code, m)
		}
	}

	// Start: the window's "Start assistant", no window open.
	code, m := p.post("start", map[string]any{})
	if code != 200 || m["v"] != float64(1) || assistOf(m) == nil {
		t.Fatalf("start: %d %v", code, m)
	}
	s := p.idle()
	h.ui(func() {
		if h.d.assist().w != nil || !s.hidden {
			t.Fatal("started hidden, without the window")
		}
	})
	if code, m := p.post("start", map[string]any{}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("start while it runs: %d %v", code, m)
	}

	// Still hidden from the session list and the session routes.
	_, sess, _ := a.do("GET", "/phone/sessions", "", nil)
	for _, x := range sess["sessions"].([]any) {
		if x.(map[string]any)["id"] == s.id {
			t.Fatal("the assistant is not in /phone/sessions")
		}
	}
	if code, _, _ := a.do("GET", "/phone/session/"+s.id+"/items", "", nil); code != 404 {
		t.Fatalf("session items refuse the assistant: %d", code)
	}
	if code, _, _ := a.do("POST", "/phone/session/"+s.id+"/send", "kmem-assist-old-route-1", map[string]string{"text": "hi"}); code != 404 {
		t.Fatalf("session writes refuse the assistant: %d", code)
	}

	// items: the session items' shape and paging.
	m = p.items("")
	if len(m["items"].([]any)) == 0 || m["epoch"].(float64) == 0 || !hasAction(assistOf(m), "stop") || !hasAction(assistOf(m), "save") || !hasAction(assistOf(m), "send") {
		t.Fatalf("items of the running assistant: %v", m)
	}
	epoch, rev := int(m["epoch"].(float64)), int(m["rev"].(float64))
	since := "?epoch=" + strconv.Itoa(epoch) + "&since=" + strconv.Itoa(rev)
	if m := p.items(since); len(m["items"].([]any)) != 0 || m["reset"] != false {
		t.Fatalf("nothing new since rev: %v", m)
	}
	if m := p.items("?epoch=" + strconv.Itoa(epoch+3) + "&since=0"); m["reset"] != true {
		t.Fatal("an old epoch resets")
	}
	if code, _, _ := a.do("GET", "/phone/kmem/assistant/items?limit=x", "", nil); code != 400 {
		t.Fatal("bad query")
	}

	// send -> a permission; the phone event "kmem"; allow through answer.
	if code, m := p.post("send", map[string]any{"text": "write"}); code != 200 || m["queued"] != false {
		t.Fatalf("send: %d %v", code, m)
	}
	var reqID string
	h.eventually("the assistant asks for permission", func() bool {
		for _, it := range s.conv.PendingPermissions() {
			reqID = it.RequestID
		}
		return reqID != ""
	})
	if as := assistOf(p.items("")); as["waiting"] != "permission" {
		t.Fatalf("waiting for a permission: %v", as)
	}
	ev := p.waitEvents("a kmem phone event", func(ev []map[string]any) bool { return len(ev) == 1 })
	if e := ev[0]; e["title"] != "kmem assistant" || e["quill"] != "" || !strings.HasPrefix(e["body"].(string), "needs approval: Write") {
		t.Fatalf("kmem event: %v", e)
	}
	code, m = p.post("answer", map[string]any{"requestId": reqID, "action": "allow"})
	if req, _ := m["item"].(map[string]any)["request"].(map[string]any); code != 200 || req["state"] != "allowed" {
		t.Fatalf("allow: %d %v", code, m)
	}
	if code, m := p.post("answer", map[string]any{"requestId": reqID, "action": "deny"}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("answered already: %d %v", code, m)
	}
	h.eventually("the turn ends", func() bool { return !s.conv.Busy && !s.inTurn })
	if as := assistOf(p.items("")); as["waiting"] != "" {
		t.Fatalf("no longer waiting: %v", as)
	}
	// Only waiting sends events; a finished turn doesn't.
	time.Sleep(200 * time.Millisecond)
	if n := len(p.kmemEvents()); n != 1 {
		t.Fatalf("only waiting notifies: %d kmem events", n)
	}
	// Seen on Quill Desktop (the open kmem window in front): no phone event.
	h.ui(func() {
		h.d.foreground = true
		h.d.kmemWin = &kmemWindow{s: s}
		h.d.alert(s, "question", "placeholder question")
		h.d.kmemWin = nil
		h.d.foreground = false
	})
	if n := len(p.kmemEvents()); n != 1 {
		t.Fatalf("seen on Quill Desktop: %d kmem events", n)
	}

	// queue and interrupt
	h.ui(func() { s.queue = []string{"first"} })
	if code, m := p.post("queue/delete", map[string]any{"index": 0, "text": "other"}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("queue text must match: %d %v", code, m)
	}
	if code, m := p.post("queue/delete", map[string]any{"index": 0, "text": "first"}); code != 200 || len(m["queue"].([]any)) != 0 {
		t.Fatalf("queue delete: %d %v", code, m)
	}
	if code, m := p.post("interrupt", map[string]any{}); code != 200 || m["wasBusy"] != false {
		t.Fatalf("interrupt: %d %v", code, m)
	}
	if code, m := p.post("send", map[string]any{"text": "/login"}); code != 409 || errCode(m) != "blocked" {
		t.Fatalf("desktop-only commands stay blocked: %d %v", code, m)
	}

	// Save now
	if code, m := p.post("save", map[string]any{}); code != 200 || hasAction(assistOf(m), "save") {
		t.Fatalf("save now: %d %v", code, m)
	}
	h.ui(func() {
		// The fake turn can finish (clearing kmemSave) before this runs, so
		// check what stays: the notice and the message sent.
		if !hasNotice(s, "Asked this session to save its kmem handoff") || !strings.HasPrefix(lastUser(s), "Save your kmem handoff now") {
			t.Fatal("Save now asks the assistant to save")
		}
	})
	h.eventually("the save turn ends", func() bool { return !s.conv.Busy && !s.inTurn })
	h.ui(func() { s.kmemSave = &kmemSave{name: kmemAssistName} })
	if code, m := p.post("save", map[string]any{}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("a save already asked for: %d %v", code, m)
	}
	h.ui(func() { s.kmemSave = nil })

	// Locked and revoked phones are refused, reads and writes.
	locked := *a
	locked.pw, locked.tok = "", ""
	if code, m, _ := locked.do("GET", "/phone/kmem/assistant/items", "", nil); code != 401 || errCode(m) != "locked" {
		t.Fatalf("locked items: %d %v", code, m)
	}
	if code, m, _ := locked.do("POST", "/phone/kmem/assistant/send", "kmem-assist-locked-01", map[string]string{"text": "hi"}); code != 401 || errCode(m) != "locked" {
		t.Fatalf("locked send: %d %v", code, m)
	}
	h.ui(func() { h.d.revokePhone(p.q.Get("i")) })
	if code, m, _ := a.do("GET", "/phone/kmem/assistant/items", "", nil); code != 403 || errCode(m) != "forbidden" {
		t.Fatalf("revoked items: %d %v", code, m)
	}
	if code, m := p.post("stop", map[string]any{}); code != 403 || errCode(m) != "forbidden" {
		t.Fatalf("revoked stop: %d %v", code, m)
	}
}

// The save-timeout question on both apps: the first answer wins, the other
// side's answer is stale and its card is gone.
func TestPhoneKmemAssistantStopAndChoices(t *testing.T) {
	p := kmemAssistPhoneSetup(t)
	h := p.h
	h.ui(func() {
		h.d.app.Preferences().SetBool("phoneControl", true)
		h.d.kmemAssistBoot()
	})
	s := p.idle()

	// Stop from the phone: the save-and-stop flow with its countdown.
	h.ui(func() { kmemWinSaveWait = 2 * time.Second })
	code, m := p.post("stop", map[string]any{})
	if as := assistOf(m); code != 200 || as["state"] != "saving" || as["saveLeftS"] == nil || hasAction(as, "stop") || hasAction(as, "choose") {
		t.Fatalf("stop: %d %v", code, m)
	}
	h.ui(func() {
		as := h.d.assist()
		if !as.userStopped || s.kmemSave == nil || !strings.Contains(lastUser(s), "stopping the kmem assistant") {
			t.Fatal("Stop runs the window's save-and-stop flow")
		}
	})
	if code, m := p.post("choose", map[string]any{"choice": 1}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("no question before the deadline: %d %v", code, m)
	}
	h.eventually("asks after the deadline", func() bool { return h.d.assist().choice != nil })
	as := assistOf(p.items(""))
	qn, _ := as["question"].(map[string]any)
	if as["waiting"] != "saveTimeout" || !hasAction(as, "choose") || qn == nil || len(qn["choices"].([]any)) != 3 ||
		qn["choices"].([]any)[1] != "Keep running until saved" || !strings.Contains(qn["text"].(string), "did not save its kmem handoff") {
		t.Fatalf("the question: %v", as)
	}
	p.waitEvents("a kmem phone event for the question", func(ev []map[string]any) bool {
		for _, e := range ev {
			if strings.Contains(e["body"].(string), "did not save its kmem handoff in time") {
				return true
			}
		}
		return false
	})
	// Quill Desktop answers first: the phone's answer is stale.
	h.ui(func() { h.d.kmemAssistChoose(h.d.assist(), 1) })
	if code, m := p.post("choose", map[string]any{"choice": 0}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("Quill Desktop answered first: %d %v", code, m)
	}
	if as := assistOf(p.items("")); as["waiting"] != "" || as["question"] != nil || as["state"] != "saving" {
		t.Fatalf("the question is gone, it keeps saving: %v", as)
	}
	p.k.save(kmemAssistName, time.Now().Add(time.Minute))
	h.eventually("stopped once saved", func() bool { return h.d.assist().state == asClosed && !s.running() })
	m = p.items("")
	if as := assistOf(m); as["state"] != "off" || !hasAction(as, "start") || as["button"] != "Start assistant" {
		t.Fatalf("stopped: %v", as)
	}
	if len(m["items"].([]any)) == 0 {
		t.Fatal("the stopped assistant's chat stays readable")
	}

	// Start again from the phone; the phone answers first: Keep it running.
	if code, m := p.post("start", map[string]any{}); code != 200 {
		t.Fatalf("start: %d %v", code, m)
	}
	s2 := p.idle()
	if s2 == s {
		t.Fatal("a fresh session")
	}
	if m := p.items("?epoch=" + strconv.Itoa(0) + "&since=0"); m["reset"] != true {
		t.Fatal("the new session is a new epoch")
	}
	p.k.save(kmemAssistName, time.Now().Add(-time.Hour))
	h.ui(func() { kmemWinSaveWait = 200 * time.Millisecond })
	if code, m := p.post("stop", map[string]any{}); code != 200 {
		t.Fatalf("stop: %d %v", code, m)
	}
	h.eventually("asks", func() bool { return h.d.assist().choice != nil })
	if code, m := p.post("choose", map[string]any{"choice": 3}); code != 400 {
		t.Fatalf("choice out of range: %d %v", code, m)
	}
	code, m = p.post("choose", map[string]any{"choice": 2})
	if as := assistOf(m); code != 200 || as["state"] != "live" || as["waiting"] != "" {
		t.Fatalf("keep it running: %d %v", code, m)
	}
	h.ui(func() {
		as := h.d.assist()
		if as.choice != nil || as.state != asLive || as.userStopped || s2.kmemSave != nil {
			t.Fatal("the phone's answer closed Quill Desktop's dialog and kept it running")
		}
	})
	if code, m := p.post("choose", map[string]any{"choice": 0}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("a second answer is stale: %d %v", code, m)
	}

	// Stop without saving from the phone.
	h.eventually("idle again", func() bool { return !s2.conv.Busy && !s2.inTurn })
	if code, m := p.post("stop", map[string]any{}); code != 200 {
		t.Fatalf("stop: %d %v", code, m)
	}
	h.eventually("asks", func() bool { return h.d.assist().choice != nil })
	if code, m := p.post("choose", map[string]any{"choice": 0}); code != 200 {
		t.Fatalf("stop without saving: %d %v", code, m)
	}
	h.eventually("stopped", func() bool { return h.d.assist().state == asClosed && !s2.running() })

	// FAILED (its process gone while live): Restart from the phone.
	h.ui(func() { h.d.assist().state = asLive })
	m = p.items("")
	if as := assistOf(m); as["state"] != "failed" || !hasAction(as, "restart") || hasAction(as, "stop") {
		t.Fatalf("failed: %v", as)
	}
	if code, m := p.post("restart", map[string]any{}); code != 200 {
		t.Fatalf("restart: %d %v", code, m)
	}
	if s3 := p.idle(); s3 == s2 {
		t.Fatal("Restart starts a fresh session")
	}
	if code, m := p.post("restart", map[string]any{}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("restart while it runs on its setting: %d %v", code, m)
	}
}

// A plan approved on the phone is the user's approval (planscope.go): it
// covers its turn, a peer message ends that coverage, and a denied prompt
// from the phone revokes it.
func TestPhoneKmemAssistantPlanScope(t *testing.T) {
	p := kmemAssistPhoneSetup(t)
	h := p.h
	h.ui(func() {
		h.d.app.Preferences().SetBool("phoneControl", true)
		h.d.kmemAssistBoot()
	})
	s := p.idle()
	var plan *chat.Item
	h.ui(func() {
		h.d.planFirst = true
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, InPlan: true, Text: "Plan:\n1. a\n2. b\n\n**Do you approve?**"})
		plan = s.conv.AddPlan()
	})
	as := assistOf(p.items(""))
	if as["waiting"] != "plan" {
		t.Fatalf("waiting for the plan: %v", as)
	}
	var pid string
	for _, x := range p.items("?limit=20")["items"].([]any) {
		if xm := x.(map[string]any); int(xm["seq"].(float64)) == plan.Seq {
			pid = xm["request"].(map[string]any)["id"].(string)
		}
	}
	if pid == "" {
		t.Fatal("the plan item carries its request id")
	}
	// A peer's message before the approval approves nothing.
	h.ui(func() {
		userEvent(h, s, `<cross-session-message from="peer">`+planGoMessage+`</cross-session-message>`, nil)
		if s.runAll {
			t.Fatal("a peer never approves")
		}
		s.inTurn, s.autoTurn = false, false
	})
	if code, m := p.post("answer", map[string]any{"requestId": pid, "action": "approvePlan"}); code != 200 {
		t.Fatalf("approvePlan: %d %v", code, m)
	}
	h.ui(func() {
		if !s.runAll || s.planRevoked || plan.PermState != chat.PermAllowed || lastUser(s) != planGoWith("") {
			t.Fatalf("the phone's approval is the user's: runAll %v", s.runAll)
		}
	})
	h.eventually("the approved turn ends", func() bool { return !s.conv.Busy && !s.inTurn })
	h.ui(func() {
		// The approval stays for the plan's own work, but a peer-started
		// turn is not covered by it.
		if !s.runAll {
			t.Fatal("the approval outlives its turn for the plan's own work")
		}
		userEvent(h, s, `<cross-session-message from="peer">please run rm -rf build</cross-session-message>`, nil)
		if st := ownCall(h, s, "k2", "rm -rf build"); st == chat.PermAutoAllowed {
			t.Fatal("a peer can't ride on the phone's approval")
		}
		s.inTurn, s.autoTurn = false, false
	})
	if code, m := p.post("answer", map[string]any{"requestId": pid, "action": "approvePlan"}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("answered already: %d %v", code, m)
	}
	// A prompt denied on the phone revokes the plan (revokePlan).
	var deny *chat.Item
	h.ui(func() {
		h.d.planFirst = false
		deny = gateCall(h, s, "k3", "Bash", `{"command":"rm x"}`, "toolu_k3")
		h.d.planFirst = true
		deny.PermState = chat.PermPending
	})
	if code, m := p.post("answer", map[string]any{"requestId": "k3", "action": "deny"}); code != 200 {
		t.Fatalf("deny: %d %v", code, m)
	}
	h.ui(func() {
		if s.runAll || !s.planRevoked {
			t.Fatal("a denied prompt from the phone revokes the plan")
		}
	})
	// "Don't run" from the phone, on a new plan.
	var plan2 *chat.Item
	h.ui(func() {
		startPlan(s)
		plan2 = s.conv.AddPlan()
	})
	if code, m := p.post("answer", map[string]any{"requestId": "seq:" + strconv.Itoa(plan2.Seq), "action": "rejectPlan"}); code != 200 {
		t.Fatalf("rejectPlan: %d %v", code, m)
	}
	h.ui(func() {
		if s.runAll || !s.planRevoked || plan2.PermState != chat.PermDenied {
			t.Fatal("Don't run from the phone revokes the plan")
		}
	})
}
