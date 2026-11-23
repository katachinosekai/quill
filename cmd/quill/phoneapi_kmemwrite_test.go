package main

import (
	"crypto/tls"
	"encoding/json"
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

// kmem on Quill Android, phase 4 (docs/design/kmem-android.md §4.6, §5.2):
// task Done/Release and bin Restore with two-step confirm ids. kmem is a stub
// (runKmem): no real kmem write ever runs here. Placeholder data only (kmem
// rule 22).

func ptr(s string) *string { return &s }

func TestPhoneKmemWritePure(t *testing.T) {
	line := func(st string) string { return "[tab1] p3 claimed: x  (demo)  <- demo" + st }
	for _, c := range []struct {
		t    kmemTask
		want string
	}{
		{kmemTask{ID: "tab1", Status: "open", Line: "[tab1] p3 open: x"}, "done"},
		{kmemTask{ID: "tab1", Status: "open", Line: "[tab1] p3 open: x  [blocked by tab2]"}, "done"},
		{kmemTask{ID: "tab1", Status: "done"}, ""},
		{kmemTask{ID: "tab1", Status: "claimed", Owner: ptr("demo"), Line: line("")}, ""},
		{kmemTask{ID: "tab1", Status: "claimed", Owner: ptr("demo"), Line: line(" [lease]")}, ""},
		{kmemTask{ID: "tab1", Status: "claimed", Owner: ptr("demo"), Line: line(" [gone]")}, "done,release"},
		{kmemTask{ID: "tab1", Status: "claimed", Owner: ptr("demo"), Line: line(" [lease expired]")}, "done,release"},
		{kmemTask{ID: "tab1", Status: "claimed", Owner: ptr("demo"), Line: line(" [handoff, waiting for --as demo]")}, "done,release"},
		{kmemTask{ID: "tab1", Status: "claimed", Owner: ptr("demo"), Line: line(" [gone]  [blocked by tab2]")}, ""},
		{kmemTask{ID: "tab1", Status: "claimed", Owner: nil, Line: line(" [gone]")}, ""},
		{kmemTask{ID: "tab1", Status: "claimed", Owner: ptr("--force"), Line: line(" [gone]")}, ""},
	} {
		if got := strings.Join(kmemTaskActions(c.t), ","); got != c.want {
			t.Fatalf("actions of %+v = %q, want %q", c.t, got, c.want)
		}
	}

	// Commands: the owner's name for claims, never --force, the result after --.
	gone := kmemTask{ID: "tab1", Status: "claimed", Owner: ptr("demo"), Line: line(" [gone]")}
	all := func(cmds [][]string) string {
		var s []string
		for _, c := range cmds {
			s = append(s, kmemCmdLine(c))
		}
		return strings.Join(s, "\n")
	}
	if got := all(kmemTaskCmds(gone, "release", "", "Test Pixel")); got !=
		"kmem --as demo task claim tab1\n"+
			`kmem --as demo task note tab1 -- "released by the user from Quill Android (Test Pixel)"`+"\n"+
			"kmem --as demo task release tab1" {
		t.Fatalf("release cmds:\n%s", got)
	}
	if got := all(kmemTaskCmds(gone, "done", "--force it", "Test Pixel")); got !=
		"kmem --as demo task claim tab1\n"+
			`kmem --as demo task done tab1 --verified-by "user: from Quill Android (Test Pixel)" -- "--force it"` {
		t.Fatalf("done cmds:\n%s", got)
	}
	open := kmemTask{ID: "tab1", Status: "open"}
	if got := all(kmemTaskCmds(open, "done", "ok", "")); got != `kmem --as "Quill Android" task done tab1 --verified-by "user: from Quill Android" -- ok` {
		t.Fatalf("open done: %s", got)
	}
	if got := kmemCleanResult(" a\tb\n\x00c d  "); got != "a b c d" {
		t.Fatalf("clean: %q", got)
	}
	if kmemCleanResult("\n\t\x01") != "" {
		t.Fatal("only control chars must be empty")
	}

	// Confirm ids: single-use, expiring, bound to phone, action and target.
	var k phoneKmem
	now := time.Now()
	mk := func() string {
		return k.putConfirm(&kmemConfirm{phone: "p1", action: "release", target: "task/tab1", expires: now.Add(time.Minute)}, now)
	}
	id := mk()
	if !kmemConfIDRe.MatchString(id) {
		t.Fatalf("confirm id %q", id)
	}
	if _, e := k.takeConfirm(id, "p2", "release", "task/tab1", now); e == nil || e.code != "confirm_invalid" {
		t.Fatalf("other phone: %v", e)
	}
	if c, e := k.takeConfirm(id, "p1", "release", "task/tab1", now); e != nil || c == nil {
		t.Fatalf("own phone after another phone tried: %v", e) // another phone doesn't spend it
	}
	if _, e := k.takeConfirm(id, "p1", "release", "task/tab1", now); e == nil || e.code != "confirm_invalid" {
		t.Fatalf("reuse: %v", e)
	}
	id = mk()
	if _, e := k.takeConfirm(id, "p1", "done", "task/tab1", now); e == nil || e.code != "confirm_invalid" {
		t.Fatalf("wrong action: %v", e)
	}
	if _, e := k.takeConfirm(id, "p1", "release", "task/tab1", now); e == nil {
		t.Fatal("a wrong-action try spends the id")
	}
	id = mk()
	if _, e := k.takeConfirm(id, "p1", "release", "task/tab2", now); e == nil || e.code != "confirm_invalid" {
		t.Fatalf("wrong target: %v", e)
	}
	id = mk()
	if _, e := k.takeConfirm(id, "p1", "release", "task/tab1", now.Add(time.Minute)); e == nil || e.code != "confirm_expired" {
		t.Fatalf("expired: %v", e)
	}
	for i := 0; i < kmemConfirmPerPh+3; i++ {
		mk()
	}
	if len(k.confirms) != kmemConfirmPerPh {
		t.Fatalf("open ids per phone: %d", len(k.confirms))
	}

	// Bin list parsing.
	b := kmemBinBody(parseBin("[f1a2b3c] fact, 2.5 days in bin, by demo: wrong path /home/user/x\n    (demo/notes) some text\n"+
		"[tab9] task, 0.1 days in bin, by other: duplicate\n    (demo) a title\n"), func(s string) string { return strings.ReplaceAll(s, "/home/user", "~") })
	js, _ := json.Marshal(b)
	if string(js) != `{"items":[{"by":"demo","days":2.5,"id":"f1a2b3c","kind":"fact","reason":"wrong path ~/x","title":"some text","topic":"demo/notes"},`+
		`{"by":"other","days":0.1,"id":"tab9","kind":"task","reason":"duplicate","title":"a title","topic":"demo"}],"truncated":false,"v":1}` {
		t.Fatalf("bin body: %s", js)
	}
}

// writeFakeKmem: a stub kmem for the write routes. tasks is the `task list
// --all --json` body; fail makes the named verb fail; block holds task list.
type writeFakeKmem struct {
	mu    sync.Mutex
	calls []string
	tasks string
	fail  string
	block chan struct{}
	in    chan struct{}
}

const writeFakeTasks = `{"tasks": [` +
	`{"id": "tab1", "prio": 4, "status": "open", "title": "Read /home/user/notes.txt", "topic": "demo", "owner": null, "line": "[tab1] p4 open: x  (demo)"},` +
	`{"id": "tab2", "prio": 3, "status": "claimed", "title": "Gone work", "topic": "demo", "owner": "demo", "line": "[tab2] p3 claimed: Gone work  (demo)  <- demo [gone]"},` +
	`{"id": "tab3", "prio": 3, "status": "claimed", "title": "Handed over", "topic": "demo", "owner": "next", "line": "[tab3] p3 claimed: x  (demo)  <- next [handoff, waiting for --as next]"},` +
	`{"id": "tab4", "prio": 2, "status": "claimed", "title": "Live work", "topic": "demo", "owner": "other", "line": "[tab4] p2 claimed: Live work  (demo)  <- other"},` +
	`{"id": "tab5", "prio": 2, "status": "claimed", "title": "Leased", "topic": "demo", "owner": "sub", "line": "[tab5] p2 claimed: x  (demo)  <- sub [lease]"},` +
	`{"id": "tab6", "prio": 2, "status": "done", "title": "Finished", "topic": "demo", "owner": null, "line": "[tab6] p2 done: Finished  (demo)"}` +
	`], "api": 1}`

func (k *writeFakeKmem) run(prog string, args ...string) (string, error) {
	a := strings.Join(args, " ")
	k.mu.Lock()
	k.calls = append(k.calls, a)
	tasks, fail, block, in := k.tasks, k.fail, k.block, k.in
	k.mu.Unlock()
	verb := kmemCmdVerb(args)
	if fail != "" && verb == fail {
		return "task is claimed by someone (alive); ask them", errors.New("exit status 1")
	}
	switch {
	case a == "version --json":
		return `{"version": "test", "api": 1}`, nil
	case a == "task list --all --json":
		return tasks, nil
	case a == "trash list":
		if in != nil {
			select {
			case in <- struct{}{}:
			default:
			}
		}
		if block != nil {
			<-block
		}
		return "NOTICE (once): rules changed\n[f1a2b3c] fact, 2.5 days in bin, by demo: wrong path /home/user/x\n    (demo/notes) mail user@example.com\n", nil
	case a == "show tab1":
		return "[tab1] p4 open: Read /home/user/notes.txt  (demo)\n  2026-11-22T10:00:00-08:00 task exampleuser\n", nil
	case a == "show tab404":
		return "no fact or task tab404", errors.New("exit status 1")
	case verb == "claim":
		return "CLAIMED [x] ...  token 2", nil
	case verb == "note":
		return "noted " + args[4], nil
	case verb == "release":
		return "released " + args[4], nil
	case verb == "done":
		return "done " + args[4] + " (success, user: from Quill Android)", nil
	case verb == "restore":
		return "restored " + args[4], nil
	}
	return "", nil
}

func (k *writeFakeKmem) since(n int) []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.calls[n:]...)
}

func (k *writeFakeKmem) n() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.calls)
}

// writes: the kmem calls among calls that change kmem.
func kmemWritesOf(calls []string) []string {
	var out []string
	for _, c := range calls {
		if strings.Contains(c, " task claim ") || strings.Contains(c, " task note ") || strings.Contains(c, " task release ") ||
			strings.Contains(c, " task done ") || strings.Contains(c, "trash restore") || strings.Contains(c, "empty") {
			out = append(out, c)
		}
	}
	return out
}

func TestPhoneKmemWrites(t *testing.T) {
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	oldH, oldU, oldTTL := kmemScrubHome, kmemScrubUser, kmemConfirmTTL
	kmemScrubHome = func() string { return "/home/user" }
	kmemScrubUser = func() string { return "exampleuser" }
	defer func() { kmemScrubHome, kmemScrubUser, kmemConfirmTTL = oldH, oldU, oldTTL }()

	h := newHarness(t)
	k := &writeFakeKmem{tasks: writeFakeTasks}
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
	base, key := "https://127.0.0.1:"+q.Get("p"), q.Get("k")
	client := &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	a := unlockedClient(h, &apiClient{t: t, base: base, key: key, c: client})
	if code, m, _ := a.do("POST", "/phone/hello", "hello-kmemw-0000001", map[string]string{"name": "Test Pixel", "app": "1.0"}); code != 200 {
		t.Fatalf("hello: %d %v", code, m)
	}
	n := 0
	post := func(path string, body any) (int, map[string]any) {
		t.Helper()
		n++
		time.Sleep(110 * time.Millisecond) // under the 10 writes/s limit
		code, m, _ := a.do("POST", path, "kmem-write-"+strconv.Itoa(1000000+n), body)
		js, _ := json.Marshal(m)
		for _, bad := range []string{"/home/user", "exampleuser", "user@example.com"} {
			if strings.Contains(string(js), bad) {
				t.Fatalf("%s leaks %q: %s", path, bad, js)
			}
		}
		return code, m
	}
	prepare := func(path string, body any) (string, map[string]any) {
		t.Helper()
		code, m := post(path, body)
		c, _ := m["confirm"].(string)
		if code != 200 || m["v"] != float64(1) || c == "" || m["expiresS"] != float64(60) {
			t.Fatalf("prepare %s: %d %v", path, code, m)
		}
		return c, m
	}

	// Control off: every write is refused, nothing runs.
	h.ui(func() { h.d.app.Preferences().SetBool("phoneControl", false) })
	start := k.n()
	for _, p := range []string{"/phone/kmem/task/tab2/release/prepare", "/phone/kmem/task/tab2/release/commit", "/phone/kmem/bin/f1a2b3c/restore/prepare"} {
		if code, m := post(p, map[string]any{"confirm": "c0123456789abcdef"}); code != 403 || errCode(m) != "control_off" {
			t.Fatalf("%s with control off: %d %v", p, code, m)
		}
	}
	if w := kmemWritesOf(k.since(start)); len(w) != 0 {
		t.Fatalf("writes with control off: %v", w)
	}
	h.ui(func() { h.d.app.Preferences().SetBool("phoneControl", true) })

	// A locked phone (no unlock token) is refused before anything runs.
	locked := &apiClient{t: t, base: base, key: key, c: client}
	if code, m, _ := locked.do("POST", "/phone/kmem/task/tab2/release/prepare", "kmem-write-locked-001", map[string]any{}); code != 401 || errCode(m) != "locked" {
		t.Fatalf("locked phone: %d %v", code, m)
	}
	// No Idempotency-Key: refused.
	if code, m, _ := a.do("POST", "/phone/kmem/task/tab2/release/prepare", "", map[string]any{}); code != 400 || errCode(m) != "idempotency_key_missing" {
		t.Fatalf("no idempotency key: %d %v", code, m)
	}

	// No route empties or purges the bin; unknown shapes are 404.
	start = k.n()
	for _, p := range []string{"/phone/kmem/bin/empty", "/phone/kmem/bin/empty/prepare", "/phone/kmem/bin/f1a2b3c/empty/prepare",
		"/phone/kmem/bin/f1a2b3c/empty/commit", "/phone/kmem/bin/f1a2b3c/purge/prepare", "/phone/kmem/trash/empty", "/phone/kmem/bin",
		"/phone/kmem/task/tab2/claim/prepare", "/phone/kmem/task/tab2/release", "/phone/kmem/commit",
		"/phone/kmem/task/tab2/release/prepare/x", "/phone/kmem/bin/f1a2b3c/restore/run"} {
		if code, _ := post(p, map[string]any{}); code != 404 {
			t.Fatalf("%s: %d, want 404", p, code)
		}
	}
	if calls := k.since(start); len(calls) != 0 {
		t.Fatalf("404 paths ran kmem: %v", calls)
	}

	// Release of a gone claim: prepare names the exact commands.
	start = k.n()
	cid, m := prepare("/phone/kmem/task/tab2/release/prepare", map[string]any{})
	cmds, _ := json.Marshal(m["commands"])
	if string(cmds) != `["kmem --as demo task claim tab2","kmem --as demo task note tab2 -- \"released by the user from Quill Android (Test Pixel)\"","kmem --as demo task release tab2"]` ||
		!strings.Contains(m["summary"].(string), "Release task [tab2]") {
		t.Fatalf("release prepare: %v", m)
	}
	if w := kmemWritesOf(k.since(start)); len(w) != 0 {
		t.Fatalf("prepare wrote: %v", w)
	}
	// The wrong action spends it; it can't be used again.
	if code, m := post("/phone/kmem/task/tab2/done/commit", map[string]any{"confirm": cid}); code != 409 || errCode(m) != "confirm_invalid" {
		t.Fatalf("wrong action: %d %v", code, m)
	}
	if code, m := post("/phone/kmem/task/tab2/release/commit", map[string]any{"confirm": cid}); code != 409 || errCode(m) != "confirm_invalid" {
		t.Fatalf("spent id: %d %v", code, m)
	}
	if w := kmemWritesOf(k.since(start)); len(w) != 0 {
		t.Fatalf("refused commits wrote: %v", w)
	}
	cid, _ = prepare("/phone/kmem/task/tab2/release/prepare", map[string]any{})
	if code, m := post("/phone/kmem/task/tab3/release/commit", map[string]any{"confirm": cid}); code != 409 || errCode(m) != "confirm_invalid" {
		t.Fatalf("wrong task: %d %v", code, m)
	}
	cid, _ = prepare("/phone/kmem/task/tab2/release/prepare", map[string]any{})
	start = k.n()
	code, m := post("/phone/kmem/task/tab2/release/commit", map[string]any{"confirm": cid})
	if code != 200 || m["action"] != "release" || m["id"] != "tab2" || !strings.Contains(m["output"].(string), "released tab2") {
		t.Fatalf("release commit: %d %v", code, m)
	}
	if w := strings.Join(kmemWritesOf(k.since(start)), "\n"); w != "--as demo task claim tab2\n"+
		"--as demo task note tab2 -- released by the user from Quill Android (Test Pixel)\n--as demo task release tab2" {
		t.Fatalf("release ran:\n%s", w)
	}
	if code, m := post("/phone/kmem/task/tab2/release/commit", map[string]any{"confirm": cid}); code != 409 || errCode(m) != "confirm_invalid" {
		t.Fatalf("reuse after commit: %d %v", code, m)
	}

	// Handoff claim: done with the user's result under the owner's name.
	if code, m := post("/phone/kmem/task/tab3/done/prepare", map[string]any{}); code != 400 {
		t.Fatalf("done without result: %d %v", code, m)
	}
	if code, m := post("/phone/kmem/task/tab3/done/prepare", map[string]any{"result": " \n\t\x01 "}); code != 400 {
		t.Fatalf("done with blank result: %d %v", code, m)
	}
	if code, m := post("/phone/kmem/task/tab3/done/prepare", map[string]any{"result": strings.Repeat("é", 301)}); code != 400 {
		t.Fatalf("done with 301 chars: %d %v", code, m)
	}
	if code, m := post("/phone/kmem/task/tab3/done/prepare", map[string]any{"result": "--help"}); code != 400 {
		t.Fatalf("done with --help: %d %v", code, m)
	}
	cid, m = prepare("/phone/kmem/task/tab3/done/prepare", map[string]any{"result": "Checked\non the\x07 phone " + strings.Repeat("x", 270)})
	start = k.n()
	if code, m := post("/phone/kmem/task/tab3/done/commit", map[string]any{"confirm": cid}); code != 200 || m["action"] != "done" {
		t.Fatalf("done commit: %d %v", code, m)
	}
	if w := strings.Join(kmemWritesOf(k.since(start)), "\n"); w != "--as next task claim tab3\n"+
		"--as next task done tab3 --verified-by user: from Quill Android (Test Pixel) -- Checked on the phone "+strings.Repeat("x", 270) {
		t.Fatalf("done ran:\n%s", w)
	}

	// Open task: done as Quill Android, no claim; release is not offered.
	cid, _ = prepare("/phone/kmem/task/tab1/done/prepare", map[string]any{"result": "done on the phone"})
	start = k.n()
	if code, m := post("/phone/kmem/task/tab1/done/commit", map[string]any{"confirm": cid}); code != 200 {
		t.Fatalf("open done: %d %v", code, m)
	}
	if w := strings.Join(kmemWritesOf(k.since(start)), "\n"); w != "--as Quill Android task done tab1 --verified-by user: from Quill Android (Test Pixel) -- done on the phone" {
		t.Fatalf("open done ran: %s", w)
	}

	// Refused: live and leased claims, release of an open task, done tasks, unknown tasks.
	start = k.n()
	for p, want := range map[string]int{
		"/phone/kmem/task/tab4/release/prepare": 409, "/phone/kmem/task/tab4/done/prepare": 409,
		"/phone/kmem/task/tab5/release/prepare": 409, "/phone/kmem/task/tab1/release/prepare": 409,
		"/phone/kmem/task/tab6/done/prepare": 409, "/phone/kmem/task/tab99/done/prepare": 404,
	} {
		code, m := post(p, map[string]any{"result": "x"})
		if code != want || (want == 409 && errCode(m) != "stale") {
			t.Fatalf("%s: %d %v", p, code, m)
		}
		if p == "/phone/kmem/task/tab4/release/prepare" && !strings.Contains(m["error"].(map[string]any)["message"].(string), "never taken") {
			t.Fatalf("live claim message: %v", m)
		}
	}
	if w := kmemWritesOf(k.since(start)); len(w) != 0 {
		t.Fatalf("refused actions wrote: %v", w)
	}

	// Stale: a live session claims the task between prepare and commit.
	cid, _ = prepare("/phone/kmem/task/tab2/release/prepare", map[string]any{})
	k.mu.Lock()
	k.tasks = strings.Replace(writeFakeTasks, `<- demo [gone]`, `<- demo`, 1)
	k.mu.Unlock()
	start = k.n()
	if code, m := post("/phone/kmem/task/tab2/release/commit", map[string]any{"confirm": cid}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("live since prepare: %d %v", code, m)
	}
	// Same claim state but another owner: also stale.
	k.mu.Lock()
	k.tasks = writeFakeTasks
	k.mu.Unlock()
	cid, _ = prepare("/phone/kmem/task/tab2/release/prepare", map[string]any{})
	k.mu.Lock()
	k.tasks = strings.Replace(writeFakeTasks, `"owner": "demo", "line": "[tab2] p3 claimed: Gone work  (demo)  <- demo [gone]"`,
		`"owner": "demo2", "line": "[tab2] p3 claimed: Gone work  (demo)  <- demo2 [gone]"`, 1)
	k.mu.Unlock()
	if code, m := post("/phone/kmem/task/tab2/release/commit", map[string]any{"confirm": cid}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("other owner since prepare: %d %v", code, m)
	}
	if w := kmemWritesOf(k.since(start)); len(w) != 0 {
		t.Fatalf("stale commits wrote: %v", w)
	}
	k.mu.Lock()
	k.tasks = writeFakeTasks
	k.mu.Unlock()

	// kmem refuses (a race kmem itself catches): 409 kmem_refused with its message.
	cid, _ = prepare("/phone/kmem/task/tab2/release/prepare", map[string]any{})
	k.mu.Lock()
	k.fail = "claim"
	k.mu.Unlock()
	start = k.n()
	if code, m := post("/phone/kmem/task/tab2/release/commit", map[string]any{"confirm": cid}); code != 409 || errCode(m) != "kmem_refused" ||
		!strings.Contains(m["error"].(map[string]any)["message"].(string), "alive") {
		t.Fatalf("kmem refused: %d %v", code, m)
	}
	if w := kmemWritesOf(k.since(start)); len(w) != 1 || !strings.Contains(w[0], "task claim") {
		t.Fatalf("after a refused claim nothing else runs: %v", w)
	}
	k.mu.Lock()
	k.fail = ""
	k.mu.Unlock()

	// Expiry.
	kmemConfirmTTL = 50 * time.Millisecond
	_, m = post("/phone/kmem/task/tab2/release/prepare", map[string]any{})
	kmemConfirmTTL = oldTTL
	if cid, _ = m["confirm"].(string); cid == "" || m["expiresS"] != float64(1) {
		t.Fatalf("short-lived prepare: %v", m)
	}
	if code, m := post("/phone/kmem/task/tab2/release/commit", map[string]any{"confirm": cid}); code != 409 || errCode(m) != "confirm_expired" {
		t.Fatalf("expired: %d %v", code, m)
	}
	if code, m := post("/phone/kmem/task/tab2/release/commit", map[string]any{"confirm": "not-an-id"}); code != 400 {
		t.Fatalf("bad confirm: %d %v", code, m)
	}

	// One kmem process per phone: a second call while one runs is 409 kmem_busy.
	time.Sleep(1100 * time.Millisecond) // a fresh 10 writes/s window
	k.mu.Lock()
	k.block, k.in = make(chan struct{}), make(chan struct{}, 1)
	block, in := k.block, k.in
	k.mu.Unlock()
	done := make(chan int, 1)
	go func() {
		code, m, _ := a.do("POST", "/phone/kmem/bin/f1a2b3c/restore/prepare", "kmem-write-busy-0000001", map[string]any{})
		if code != 200 {
			t.Errorf("the first call: %d %v", code, m)
		}
		done <- code
	}()
	select {
	case <-in:
	case <-done:
		t.Fatal("the first call ended before kmem ran")
	}
	time.Sleep(110 * time.Millisecond)
	if code, m, _ := a.do("POST", "/phone/kmem/task/tab1/done/prepare", "kmem-write-busy-0000002", map[string]any{"result": "x"}); code != 409 || errCode(m) != "kmem_busy" {
		t.Fatalf("busy: %d %v", code, m)
	}
	k.mu.Lock()
	k.block, k.in = nil, nil
	k.mu.Unlock()
	close(block)
	if code := <-done; code != 200 {
		t.Fatalf("the first call: %d", code)
	}

	// Task detail and the recycle bin.
	code, m, _ = a.do("GET", "/phone/kmem/task/tab1", "", nil)
	if js, _ := json.Marshal(m); code != 200 || m["id"] != "tab1" || !strings.Contains(m["text"].(string), "~/notes.txt") || strings.Contains(string(js), "exampleuser") {
		t.Fatalf("task detail: %d %v", code, m)
	}
	if code, _, _ := a.do("GET", "/phone/kmem/task/tab404", "", nil); code != 404 {
		t.Fatalf("unknown task detail: %d", code)
	}
	code, m, _ = a.do("GET", "/phone/kmem/bin", "", nil)
	if js, _ := json.Marshal(m); code != 200 || string(js) != `{"items":[{"by":"demo","days":2.5,"id":"f1a2b3c","kind":"fact","reason":"wrong path ~/x","title":"mail (e-mail)","topic":"demo/notes"}],"truncated":false,"v":1}` {
		t.Fatalf("bin: %d %s", code, js)
	}
	cid, m = prepare("/phone/kmem/bin/f1a2b3c/restore/prepare", map[string]any{})
	if cmds, _ := json.Marshal(m["commands"]); string(cmds) != `["kmem --as \"Quill Android\" trash restore f1a2b3c"]` {
		t.Fatalf("restore prepare: %v", m)
	}
	if code, m := post("/phone/kmem/task/tab2/release/commit", map[string]any{"confirm": cid}); code != 409 || errCode(m) != "confirm_invalid" {
		t.Fatalf("restore id used for a task: %d %v", code, m)
	}
	cid, _ = prepare("/phone/kmem/bin/f1a2b3c/restore/prepare", map[string]any{})
	start = k.n()
	if code, m := post("/phone/kmem/bin/f1a2b3c/restore/commit", map[string]any{"confirm": cid}); code != 200 || m["output"] != "restored f1a2b3c" {
		t.Fatalf("restore: %d %v", code, m)
	}
	if w := kmemWritesOf(k.since(start)); len(w) != 1 || w[0] != "--as Quill Android trash restore f1a2b3c" {
		t.Fatalf("restore ran: %v", w)
	}
	if code, m := post("/phone/kmem/bin/f9e8d7c/restore/prepare", map[string]any{}); code != 409 || errCode(m) != "stale" {
		t.Fatalf("restore of an item not in the bin: %d %v", code, m)
	}

	// Never --force, never empty, from any route above.
	for _, c := range k.since(0) {
		if strings.Contains(c, "--force") || strings.Contains(c, "empty") {
			t.Fatalf("forbidden kmem call: %s", c)
		}
	}

	// kmem off: 503.
	h.ui(func() { h.d.app.Preferences().SetString(memoryModeKey, memoryModeClaude) })
	if code, m := post("/phone/kmem/task/tab2/release/prepare", map[string]any{}); code != 503 || errCode(m) != "kmem_unavailable" {
		t.Fatalf("kmem off: %d %v", code, m)
	}
}
