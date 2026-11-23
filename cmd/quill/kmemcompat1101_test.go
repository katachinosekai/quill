package main

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

// fakeKmem simulates kmem 1.9.3 (no features) or 1.10.1 (with topic-scope).
type fakeKmem struct {
	mu    sync.Mutex
	scope bool
	calls []string
}

func (f *fakeKmem) run(_ string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(args, " "))
	switch args[0] {
	case "version":
		if f.scope {
			return `{"kmem": "1.10.1", "platform": "linux", "api": 1, "features": ["topic-context","end-check","sessions","topic-scope"]}`, nil
		}
		return `{"kmem": "1.9.3", "platform": "linux", "api": 1}`, nil
	case "recall", "find":
		return "alpha/core: [f1] fact: zebra\n", nil
	case "scope":
		if args[1] == "plain" {
			return `{"scope": {"topic": "plain", "topics": ["plain"]}, "api": 1}`, nil
		}
		return `{"scope": {"topic": "alpha", "topics": ["alpha", "alpha-extra"]}, "api": 1}`, nil
	}
	return "", errors.New("unexpected")
}

func compatDesk(t *testing.T, scope bool) (*desk, *fakeKmem) {
	h := newHarness(t)
	f := &fakeKmem{scope: scope}
	h.d.kmemOff = false
	h.d.kmemRun = f.run
	h.d.kmemVer.reset()
	return h.d, f
}

func (f *fakeKmem) has(sub string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func TestRecallPutsDashDashBeforeUserText(t *testing.T) {
	d, f := compatDesk(t, false)
	d.contentHits("-x zebra", []topicInfo{{Slug: "alpha", Scope: []string{"alpha"}}}, nil, none)
	if !f.has("recall -n " + recallHitLimit + " -- -x zebra") {
		t.Fatalf("calls: %v", f.calls)
	}
}

func TestExitCodeOf(t *testing.T) {
	for _, c := range []struct {
		err  error
		want int
	}{{nil, -1}, {errors.New("exit status 2"), 2}, {errors.New("exit status 1"), 1}, {errors.New("context deadline exceeded"), -1}} {
		if got := exitCodeOf(c.err); got != c.want {
			t.Errorf("%v: %d want %d", c.err, got, c.want)
		}
	}
}

func TestEndCheckExitCodeMapping(t *testing.T) {
	d, f := compatDesk(t, true)
	reply := func(out string, err error) {
		d.kmemRun = func(prog string, args ...string) (string, error) {
			if args[0] == "--as" {
				return out, err
			}
			return f.run(prog, args...)
		}
	}
	// exit 2 is a kmem error even when the output parses as a result
	reply(ecOK.out, errors.New("exit status 2"))
	if _, e := d.endCheck("alpha", "s", 0); e == "" {
		t.Fatal("exit 2 must be an error")
	}
	reply(ecBroken.out, ecBroken.err)
	if _, e := d.endCheck("alpha", "s", 0); e == "" {
		t.Fatal("broken output is an error")
	}
	// exit 1 with blockers is a result
	reply(ecBlocked.out, ecBlocked.err)
	if r, e := d.endCheck("alpha", "s", 0); e != "" || r.OK || len(r.Blockers) != 3 {
		t.Fatalf("blockers: %+v %q", r, e)
	}
	reply(ecOK.out, nil)
	if r, e := d.endCheck("alpha", "s", 0); e != "" || !r.OK {
		t.Fatalf("ok: %+v %q", r, e)
	}
}

func TestEndCheckMessageTellsHowToClearBlockers(t *testing.T) {
	m := endCheckMessage(endForEnd, "alpha", []endBlocker{{Kind: "job", PID: 42}, {Kind: "files", Repo: "/p/repo"}})
	for _, w := range []string{"pid 42", "repo name", "uncommitted", `"pid N"`} {
		if !strings.Contains(m, w) {
			t.Errorf("missing %q in %s", w, m)
		}
	}
}

func TestTopicScopeFeature(t *testing.T) {
	for _, on := range []bool{false, true} {
		d, f := compatDesk(t, on)
		list := []topicInfo{{Slug: "alpha", Scope: []string{"alpha"}}}
		d.applyKmemScopes(list)
		if on {
			if len(list[0].Scope) != 2 || !f.has("scope alpha --json") {
				t.Fatalf("scope from kmem: %+v %v", list[0], f.calls)
			}
		} else if len(list[0].Scope) != 1 || f.has("scope") {
			t.Fatalf("1.9.3 keeps today's behaviour: %+v %v", list[0], f.calls)
		}
	}
}

func TestScopeReadOnly(t *testing.T) {
	if !readOnlyKmemWords([]string{"scope", "quill", "--json"}) || readOnlyKmemWords([]string{"scope", "quill", "a,b"}) {
		t.Fatal("scope T reads; scope T list writes")
	}
}

func TestKmemVersionField(t *testing.T) {
	for _, c := range []struct {
		scope bool
		want  string
	}{{false, "1.9.3"}, {true, "1.10.1"}} {
		d, _ := compatDesk(t, c.scope)
		if got := d.kmemInstalledVersion(); got != c.want {
			t.Errorf("version %q want %q", got, c.want)
		}
	}
}

func TestScopeCountsFromKmem(t *testing.T) {
	d, _ := compatDesk(t, true)
	list := []topicInfo{{Slug: "alpha", Scope: []string{"alpha"}}, {Slug: "plain", Scope: []string{"plain"}}}
	tasks := []kmemTask{
		{ID: "t1", Status: "claimed", Topic: "alpha"},
		{ID: "t2", Status: "open", Topic: "alpha-extra/x"},
		{ID: "t3", Status: "claimed", Topic: "alpha-extra"},
		{ID: "t4", Status: "open", Topic: "plain"},
	}
	countTasks(list, tasks)
	if list[0].Claimed != 1 || list[0].Open != 0 {
		t.Fatalf("own scope only: %+v", list[0])
	}
	if !d.applyKmemScopes(list) {
		t.Fatal("scopes applied")
	}
	countTasks(list, tasks)
	if list[0].Claimed != 2 || list[0].Open != 1 || !list[0].Labeled {
		t.Fatalf("kmem's scope: %+v", list[0])
	}
	if list[1].Open != 1 || list[1].Labeled || len(list[1].Scope) != 1 {
		t.Fatalf("no stored scope: %+v", list[1])
	}
}

func TestStatusLineNewest(t *testing.T) {
	out := "[f2c3bfa] decision: older status\n[f427744] fact: middle\n[fa99658] fact: newest status\n"
	if got := statusLine(out); got != "newest status" {
		t.Fatalf("status line %q", got)
	}
}
