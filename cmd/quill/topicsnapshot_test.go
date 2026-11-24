package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// Fixtures in kmem 1.13.0's shapes (`handoff get T --json`, `handoff list
// --json`, `scope T --json`, `end-check --json`). Placeholder data only.
const (
	snapVersion113 = `{"kmem": "1.13.0", "platform": "linux", "api": 1, "features": ["topic-context", "end-check", "sessions", "topic-scope", "howto", "relate", "topic-snapshot", "topic-title", "handoff-convert"]}`
	snapHandoffs   = `{"handoffs": [` +
		`{"name": "alpha", "items": 4, "saved": "2026-11-23T10:00:00-08:00"},` +
		`{"name": "old-work", "items": 3, "saved": "2026-11-21T06:00:00-08:00", "converted_to": "alpha"},` +
		`{"name": "not-a-topic", "items": 1, "saved": "2026-11-20T06:00:00-08:00"}], "api": 1}`
	snapAlpha = `{"name": "alpha", "renamed_from": [], "items": [], "text": "", "snapshot": {"name": "alpha", "saved": "2026-11-23T10:00:00-08:00", "by": "alpha", "age_h": 2.5, "old": false,
 "questions": [{"qid": "q1", "text": "Which layout?", "options": ["List", "Grid"], "recommended": "Grid", "asked": "2026-11-23T09:00:00-08:00", "answered": null, "id": "f0000a1"},
  {"qid": "q2", "text": "Ship on Friday?", "options": [], "recommended": null, "asked": "2026-11-23T09:30:00-08:00", "answered": null, "id": "f0000a2"}],
 "jobs": [{"what": "build", "pid": 4242, "log": "/tmp/example.log", "running": true, "id": "f0000b1"},
  {"what": "old test run", "pid": 4343, "log": null, "running": false, "id": "f0000b2"},
  {"what": "worktree example-wt", "pid": null, "log": null, "running": null, "id": "f0000b3"}],
 "hypotheses": ["X likely causes Y, unverified"], "states": ["half done; docs left"], "wip": null, "lock": null, "pointers": [], "answered": []}, "api": 1}`
	snapEndCheck = `{"ok": false, "name": "alpha", "session": "s", "since": "2026-11-23T10:00:00Z",
 "blockers": [{"kind": "snapshot", "name": "alpha", "saved": "2026-11-23T09:00:00Z", "need": "save this topic's snapshot (kmem --as alpha handoff save alpha --state \"...\")"},
  {"kind": "claim", "id": "t1", "title": "Ship it", "need": "claim (kmem task claim t1), then checkpoint or release"}], "notes": [], "api": 1}`
)

func TestSnapshotBlockerWording(t *testing.T) {
	r, perr := parseEndCheck(snapEndCheck)
	if perr != "" || len(r.Blockers) != 2 {
		t.Fatalf("parse: %+v %q", r, perr)
	}
	s := r.Blockers[0]
	if s.Kind != "snapshot" || s.Saved == "" {
		t.Fatalf("snapshot blocker: %+v", s)
	}
	txt := blockerText(s)
	for _, w := range []string{"the topic snapshot of alpha", "not saved in this session; last saved", "handoff save alpha"} {
		if !strings.Contains(txt, w) {
			t.Errorf("blocker text misses %q: %s", w, txt)
		}
	}
	if txt := blockerText(endBlocker{Kind: "snapshot", Name: "alpha", Need: "save it"}); !strings.Contains(txt, "(never saved): save it") {
		t.Errorf("never saved: %s", txt)
	}
	if txt := blockerText(endBlocker{Kind: "future-kind", Need: "do the thing"}); txt != "future-kind: do the thing" {
		t.Errorf("unknown kinds stay generic: %s", txt)
	}

	m := endCheckMessage(endForEnd, "alpha", r.Blockers)
	for _, w := range []string{
		"`kmem --as alpha handoff save alpha --state \"...\"`",
		"--question", "--job \"what | pid N | log\"",
		"`kmem --as alpha handoff save alpha --same` if nothing changed",
		"needs `kmem task claim ID` first",
		"and release your shared lock turn",
	} {
		if !strings.Contains(m, w) {
			t.Errorf("message misses %q: %s", w, m)
		}
	}
	// Never saved: no --same.
	if m := endCheckMessage(endForEnd, "alpha", []endBlocker{{Kind: "snapshot", Name: "alpha"}}); strings.Contains(m, "--same") || !strings.Contains(m, "handoff save alpha --state") {
		t.Errorf("never-saved snapshot: %s", m)
	}
	// No snapshot blocker (older kmem): no snapshot wording.
	m = endCheckMessage(endForEnd, "alpha", []endBlocker{{Kind: "claim", ID: "t1"}})
	if strings.Contains(m, "handoff save") || !strings.Contains(m, "kmem task claim ID") || !strings.Contains(m, "and release your shared lock turn") {
		t.Errorf("claim only: %s", m)
	}

	if c := checkpointMessage("alpha", true); !strings.Contains(c, "handoff save alpha --state") || !strings.Contains(c, "--same") || !strings.Contains(c, "kmem task claim ID") {
		t.Errorf("checkpoint with snapshot: %s", c)
	}
	if c := checkpointMessage("alpha", false); strings.Contains(c, "handoff save") || !strings.HasPrefix(c, "Checkpoint now: run `kmem task checkpoint`") {
		t.Errorf("checkpoint without snapshot: %s", c)
	}
}

func fixtureJS(replies map[string]string) (func(any, ...string) bool, *[]string) {
	var calls []string
	return func(out any, args ...string) bool {
		a := strings.Join(args, " ")
		calls = append(calls, a)
		r, ok := replies[a]
		return ok && json.Unmarshal([]byte(r), out) == nil
	}, &calls
}

func TestReadSnapshots(t *testing.T) {
	var hs struct {
		Handoffs []kmemHandoffRow `json:"handoffs"`
	}
	if err := json.Unmarshal([]byte(snapHandoffs), &hs); err != nil || hs.Handoffs[1].ConvertedTo != "alpha" {
		t.Fatalf("handoff list: %v %+v", err, hs)
	}
	js, calls := fixtureJS(map[string]string{"handoff get alpha": snapAlpha})
	got := readSnapshots(js, hs.Handoffs, map[string]bool{"alpha": true, "old-work": true, "beta": true})
	if len(*calls) != 1 || (*calls)[0] != "handoff get alpha" {
		t.Fatalf("reads only same-named, not converted handoffs: %v", *calls)
	}
	s := got["alpha"]
	if s == nil || len(s.Questions) != 2 || len(s.Jobs) != 3 || s.By != "alpha" || s.Questions[1].Recommended != "" || s.Jobs[2].Running != nil || s.Jobs[2].PID != 0 {
		t.Fatalf("snapshot: %+v", s)
	}
}

func TestSnapshotLines(t *testing.T) {
	var r struct {
		Snapshot *topicSnapshot `json:"snapshot"`
	}
	if err := json.Unmarshal([]byte(snapAlpha), &r); err != nil {
		t.Fatal(err)
	}
	s := r.Snapshot
	now := s.savedAt().Add(3 * time.Hour)
	got := strings.Join(snapshotLines(s, now), "\n")
	for _, w := range []string{
		"Snapshot saved 3h ago by alpha",
		"Open question q1: Which layout? (options: List; Grid; recommended: Grid)",
		"Open question q2: Ship on Friday?",
		"Job: build (pid 4242, running, log /tmp/example.log)",
		"Job: old test run (pid 4343, ended)",
		"Job: worktree example-wt (no pid: kept until the next snapshot)",
		"State: half done; docs left",
		"Hypothesis: X likely causes Y, unverified",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("lines miss %q:\n%s", w, got)
		}
	}
	if strings.Contains(got, "old (over 14 days)") {
		t.Errorf("a fresh snapshot is not old:\n%s", got)
	}
	// Past the fixed 14 days by time, or by kmem's flag.
	if h := snapshotHead(s, s.savedAt().Add(15*24*time.Hour)); !strings.Contains(h, "15d ago") || !strings.Contains(h, "old (over 14 days)") {
		t.Errorf("old by time: %s", h)
	}
	s.Old = true
	if h := snapshotHead(s, now); !strings.Contains(h, "old (over 14 days)") {
		t.Errorf("old by kmem's flag: %s", h)
	}
}

func TestKmemTopicTitle(t *testing.T) {
	for _, titles := range []bool{false, true} {
		h := newHarness(t)
		resetTopicCache()
		t.Cleanup(resetTopicCache)
		h.d.kmemOff = false
		h.d.kmemRun = func(_ string, args ...string) (string, error) {
			switch args[0] {
			case "version":
				if titles {
					return snapVersion113, nil
				}
				return `{"kmem": "1.12.0", "platform": "linux", "api": 1, "features": ["topic-context", "end-check", "sessions", "topic-scope"]}`, nil
			case "scope":
				return `{"scope": {"topic": "alpha", "topics": ["alpha"], "title": "Alpha Garden"}, "api": 1}`, nil
			}
			return "", errors.New("unexpected")
		}
		h.d.kmemVer.reset()
		seedSavedTopicTitles(map[string]string{"alpha": "Typed Title"})
		list := []topicInfo{{Slug: "alpha", Title: "Alpha", Scope: []string{"alpha"}}}
		h.d.applyKmemScopes(list)
		applySavedTitles(list, nil)
		if titles && (list[0].Title != "Alpha Garden" || list[0].KmemTitle != "Alpha Garden") {
			t.Fatalf("kmem's title wins: %+v", list[0])
		}
		if !titles && (list[0].Title != "Typed Title" || list[0].KmemTitle != "") {
			t.Fatalf("without topic-title: today's title: %+v", list[0])
		}
	}
}

func TestTopicsTabShowsSnapshot(t *testing.T) {
	h := newHarness(t)
	resetTopicCache()
	t.Cleanup(resetTopicCache)
	topicCache.mu.Lock()
	topicCache.list, topicCache.loaded = []topicInfo{{Slug: "alpha", Title: "Alpha Garden", KmemTitle: "Alpha Garden", Scope: []string{"alpha"}}}, true
	topicCache.mu.Unlock()
	var hs struct {
		Handoffs []kmemHandoffRow `json:"handoffs"`
	}
	_ = json.Unmarshal([]byte(snapHandoffs), &hs)
	js, _ := fixtureJS(map[string]string{"handoff get alpha": snapAlpha})
	data := &kmemPanelData{topics: []kmemTopic{{"alpha/core", 2, 1}, {"beta/open", 1, 1}}, handoffs: hs.Handoffs, status: map[string]string{"alpha": "half done"}}
	data.snapshots = readSnapshots(js, data.handoffs, panelTopicNames(data))
	h.ui(func() {
		p := &kmemPanel{d: h.d}
		got := labelsIn(p.topicsRows(data))
		for _, w := range []string{"Alpha Garden (alpha)", "Snapshot saved", "by alpha", "Open question q1: Which layout? (options: List; Grid; recommended: Grid)", "Job: build (pid 4242, running", "State: half done; docs left", "beta"} {
			if !strings.Contains(got, w) {
				t.Errorf("Topics tab misses %q:\n%s", w, got)
			}
		}
	})
	if m := handoffMeta("2d ago", hs.Handoffs[1]); !strings.Contains(m, "converted to topic alpha") {
		t.Errorf("history marks converted handoffs: %s", m)
	}
}

func TestPhoneTopicsSnapshot(t *testing.T) {
	h, a := topicPhone(t, true)
	prev := h.d.kmemRun
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "version --json":
			return snapVersion113, nil
		case "handoff list --json":
			return snapHandoffs, nil
		case "handoff get alpha --json":
			return snapAlpha, nil
		case "scope alpha --json":
			return `{"scope": {"topic": "alpha", "topics": ["alpha"], "title": null}, "api": 1}`, nil
		case "scope beta --json":
			return `{"scope": {"topic": "beta", "topics": ["beta"], "title": "Beta Example"}, "api": 1}`, nil
		}
		return prev(prog, args...)
	}
	h.d.kmemVer.reset()
	h.d.kmemAPILevel()
	h.d.loadTopics()
	code, m, _ := a.do("GET", "/phone/topics", "", nil)
	if code != 200 {
		t.Fatalf("topics: %d %v", code, m)
	}
	al := topicByslug(m, "alpha")
	if al == nil || al["status"] != "alpha is half done" || al["snapshotBy"] != "alpha" || al["snapshotOld"] != nil {
		t.Fatalf("alpha keeps its fields and gains the snapshot: %v", al)
	}
	want, _ := time.Parse(time.RFC3339, "2026-11-23T10:00:00-08:00")
	if al["snapshotAt"] != float64(want.UnixMilli()) {
		t.Fatalf("snapshotAt: %v", al["snapshotAt"])
	}
	qs, _ := al["questions"].([]any)
	if len(qs) != 2 {
		t.Fatalf("questions: %v", al["questions"])
	}
	q1, q2 := qs[0].(map[string]any), qs[1].(map[string]any)
	if q1["qid"] != "q1" || q1["text"] != "Which layout?" || q1["recommended"] != "Grid" || len(q1["options"].([]any)) != 2 || q1["askedMs"].(float64) <= 0 {
		t.Fatalf("q1: %v", q1)
	}
	if q2["recommended"] != nil || len(q2["options"].([]any)) != 0 {
		t.Fatalf("q2: %v", q2)
	}
	be := topicByslug(m, "beta")
	if be == nil || be["title"] != "Beta Example" {
		t.Fatalf("beta takes kmem's title: %v", be)
	}
	for _, k := range []string{"snapshotAt", "snapshotBy", "snapshotOld", "questions"} {
		if _, ok := be[k]; ok {
			t.Fatalf("no snapshot: %s left out: %v", k, be)
		}
	}
	hist, _ := m["history"].([]any)
	var conv string
	for _, x := range hist {
		if e := x.(map[string]any); e["name"] == "old-work" {
			conv, _ = e["convertedTo"].(string)
		}
	}
	if conv != "alpha" {
		t.Fatalf("history convertedTo: %v", m["history"])
	}
}
