package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/folders"
)

// kmem on Quill Android, phase 1 (docs/design/kmem-android.md §4, §5; kmem
// task tc6efac, user decisions fd77947): read-only /phone/kmem/overview,
// /phone/kmem/tasks and /phone/kmem/topics, the "kmem" entry in hello and the
// "kmem" stream change that drives the kmem button's bubble and needs-you
// mark (t90b538). Phase 3 (phonekmemmemory.go) adds find, get and fact. The reads use the kmem window's loader
// (readKmemPanelData), cached for kmemPhoneCacheFor and shared by all phones;
// each phone runs at most one kmem read at a time (409 kmem_busy). Text that
// leaves Quill Desktop has the home folder replaced by ~, the user name by
// "user" and e-mail addresses removed; the maintenance report is a file name.
// No route here writes to kmem; phase 4's task and bin writes are in
// phonekmemwrite.go.

// kmemPhoneAPI is the level in hello's "kmem" entry; a breaking change raises it.
const kmemPhoneAPI = 1

const (
	kmemPhoneMax      = 64 << 10 // bytes of text per response (§5.5)
	kmemPhoneBriefMax = 32 << 10
	kmemPhoneTitleMax = 500
)

var kmemPhoneCacheFor = 10 * time.Second // tests shorten it

type phoneKmem struct {
	mu      sync.Mutex
	data    *kmemPanelData
	ok      bool
	at      time.Time
	loading chan struct{}
	busy    map[string]bool // phone id -> a kmem read of this phone runs
	find    kmemFindLimit   // phase 3: find at most once a second per phone
	// phase 4 (phonekmemwrite.go): open confirm ids of task and bin writes
	confirms map[string]*kmemConfirm
}

// begin marks a kmem read for phone; false when one already runs (409).
func (k *phoneKmem) begin(phone string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.busy == nil {
		k.busy = map[string]bool{}
	}
	if k.busy[phone] {
		return false
	}
	k.busy[phone] = true
	return true
}

func (k *phoneKmem) end(phone string) {
	k.mu.Lock()
	delete(k.busy, phone)
	k.mu.Unlock()
}

// kmemOn: what hello's kmem.on reports (kmem memory in use and kmem here).
func (d *desk) kmemOn() bool {
	return d.kmemAvailable() && d.kmemMemory()
}

// kmemData returns the cached panel data, reading kmem when the cache is
// older than kmemPhoneCacheFor. A read already running for another phone is
// shared; a second read of the same phone answers 409 kmem_busy. On failure
// it wrote the error and returns nil.
func (a *phoneAPI) kmemData(ctx context.Context, w http.ResponseWriter, phone string) *kmemPanelData {
	var on bool
	a.ui(func() { on = a.d.kmemOn() })
	if !on {
		folders.WriteError(w, http.StatusServiceUnavailable, "kmem_unavailable", "kmem memory is off or kmem is not installed on Quill Desktop")
		return nil
	}
	k := &a.kmem
	k.mu.Lock()
	if k.data != nil && k.ok && time.Since(k.at) < kmemPhoneCacheFor {
		data := k.data
		k.mu.Unlock()
		return data
	}
	k.mu.Unlock()
	if !k.begin(phone) {
		folders.WriteError(w, http.StatusConflict, "kmem_busy", "a kmem read for this phone is already running")
		return nil
	}
	defer k.end(phone)
	k.mu.Lock()
	ch := k.loading
	if ch == nil {
		ch = make(chan struct{})
		k.loading = ch
		go func() {
			data, ok := a.d.readKmemPanelData(false)
			if ok {
				a.d.do(func() { a.d.setKmemNeeds(data.needs) }) // the PC's bubble follows too
			}
			k.mu.Lock()
			k.data, k.ok, k.at, k.loading = data, ok, time.Now(), nil
			k.mu.Unlock()
			close(ch)
		}()
	}
	k.mu.Unlock()
	select {
	case <-ch:
	case <-ctx.Done():
		return nil
	}
	k.mu.Lock()
	data, ok := k.data, k.ok
	k.mu.Unlock()
	if !ok {
		folders.WriteError(w, http.StatusServiceUnavailable, "kmem_unavailable", "kmem didn't answer")
		return nil
	}
	return data
}

// kmemRoute serves GET /phone/kmem/*.
func (a *phoneAPI) kmemRoute(w http.ResponseWriter, r *http.Request, path string) {
	phone := folders.PhoneID(r)
	switch path {
	case "kmem/overview":
		data := a.kmemData(r.Context(), w, phone)
		if data == nil {
			return
		}
		var as map[string]any
		a.ui(func() { as = a.kmemAssistSummary(true) })
		folders.WriteJSON(w, http.StatusOK, kmemOverviewBody(a.boot, data, as, kmemScrubber()))
	case "kmem/tasks":
		q := r.URL.Query()
		if q.Get("all") == "1" {
			a.kmemAllTasks(w, r, phone, q.Get("topic"))
			return
		}
		data := a.kmemData(r.Context(), w, phone)
		if data == nil {
			return
		}
		folders.WriteJSON(w, http.StatusOK, kmemTasksBody(data.rows, q.Get("topic"), kmemScrubber()))
	case "kmem/topics":
		data := a.kmemData(r.Context(), w, phone)
		if data == nil {
			return
		}
		folders.WriteJSON(w, http.StatusOK, kmemTopicsBody(data.topics, kmemScrubber()))
	case "kmem/find":
		a.kmemFind(w, r, phone)
	case "kmem/get":
		a.kmemGet(w, r, phone)
	case "kmem/fact":
		a.kmemFact(w, r, phone)
	case "kmem/bin":
		a.kmemBin(w, phone)
	default:
		if id, ok := strings.CutPrefix(path, "kmem/task/"); ok {
			a.kmemTaskShow(w, phone, id)
			return
		}
		folders.WriteError(w, http.StatusNotFound, "not_found", "unknown path")
	}
}

// kmemAllTasks: tasks?all=1 also lists done tasks (`kmem task list --all`),
// not cached; one at a time per phone like the cached reads.
func (a *phoneAPI) kmemAllTasks(w http.ResponseWriter, r *http.Request, phone, topic string) {
	var on bool
	a.ui(func() { on = a.d.kmemOn() })
	if !on {
		folders.WriteError(w, http.StatusServiceUnavailable, "kmem_unavailable", "kmem memory is off or kmem is not installed on Quill Desktop")
		return
	}
	if !a.kmem.begin(phone) {
		folders.WriteError(w, http.StatusConflict, "kmem_busy", "a kmem read for this phone is already running")
		return
	}
	defer a.kmem.end(phone)
	var tr struct {
		Tasks []topicTask `json:"tasks"`
	}
	if !a.d.kmemJSON(&tr, "task", "list", "--all") {
		folders.WriteError(w, http.StatusServiceUnavailable, "kmem_unavailable", "kmem didn't answer")
		return
	}
	folders.WriteJSON(w, http.StatusOK, kmemTasksBody(tr.Tasks, topic, kmemScrubber()))
}

// --- response bodies ---

type kmemPhoneTask struct {
	ID        string   `json:"id"`
	Prio      int      `json:"prio"`
	Status    string   `json:"status"`
	Title     string   `json:"title"`
	Topic     string   `json:"topic"`
	Owner     string   `json:"owner,omitempty"`
	Claim     string   `json:"claim,omitempty"`
	NeedsUser bool     `json:"needsUser"`
	Issue     bool     `json:"issue"`
	Actions   []string `json:"actions"`
}

// kmemClaim reads the claim state from kmem's task line ("<- owner [gone]"):
// live, gone, lease, leaseExpired or handoff; "" for unclaimed tasks.
func kmemClaim(t kmemTask) string {
	if t.Status != "claimed" {
		return ""
	}
	i := strings.LastIndex(t.Line, "  <- ")
	if i < 0 {
		return "live"
	}
	rest := t.Line[i:]
	switch {
	case strings.Contains(rest, "[gone]"):
		return "gone"
	case strings.Contains(rest, "[lease expired]"):
		return "leaseExpired"
	case strings.Contains(rest, "[lease]"):
		return "lease"
	case strings.Contains(rest, "[handoff"):
		return "handoff"
	}
	return "live"
}

func kmemPhoneTaskOf(t kmemTask, sc func(string) string) kmemPhoneTask {
	title, _ := capText(sc(t.Title), kmemPhoneTitleMax)
	out := kmemPhoneTask{ID: t.ID, Prio: t.Prio, Status: t.Status, Title: title, Topic: sc(t.Topic), Claim: kmemClaim(t),
		NeedsUser: userTask(t), Issue: kmemIssue(t), Actions: kmemTaskActions(t)} // phase 4: what the write routes accept
	if t.Owner != nil {
		out.Owner = sc(*t.Owner)
	}
	return out
}

// kmemTopicMatch: kmem's topic filter (the topic itself or below it).
func kmemTopicMatch(topic, want string) bool {
	return want == "" || topic == want || strings.HasPrefix(topic, want+"/")
}

func kmemTasksBody(rows []topicTask, topic string, sc func(string) string) map[string]any {
	tasks := []kmemPhoneTask{}
	tops := map[string]bool{}
	size, truncated := 0, false
	sorted := append([]topicTask(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Prio < sorted[j].Prio })
	for _, t := range sorted {
		if t.Topic != "" {
			tops[sc(topLevel(t.Topic))] = true
		}
		if !kmemTopicMatch(t.Topic, topic) {
			continue
		}
		pt := kmemPhoneTaskOf(t.kmemTask, sc)
		if size += len(pt.Title) + len(pt.ID) + len(pt.Topic) + len(pt.Owner) + 120; size > kmemPhoneMax {
			truncated = true
			break
		}
		tasks = append(tasks, pt)
	}
	topics := []string{}
	for t := range tops {
		topics = append(topics, t)
	}
	sort.Strings(topics)
	return map[string]any{"v": 1, "tasks": tasks, "topics": topics, "truncated": truncated}
}

func kmemTopicsBody(ts []kmemTopic, sc func(string) string) map[string]any {
	out := []map[string]any{}
	for _, t := range ts {
		out = append(out, map[string]any{"name": sc(t.Name), "facts": t.Facts, "kb": t.KB})
	}
	return map[string]any{"v": 1, "topics": out}
}

func kmemNeedsBody(n kmemNeeds, sc func(string) string) map[string]any {
	list := func(ts []kmemTask) []map[string]any {
		out := []map[string]any{}
		for _, t := range ts {
			title, _ := capText(sc(t.Title), kmemPhoneTitleMax)
			out = append(out, map[string]any{"id": t.ID, "prio": t.Prio, "title": title, "topic": sc(t.Topic)})
		}
		return out
	}
	report := ""
	if n.MaintReport != "" {
		report = sc(filepath.Base(n.MaintReport))
	}
	return map[string]any{"count": n.Count(), "bin": n.Bin, "audit": sc(n.Audit), "maint": n.Maint, "maintReport": report,
		"rolloutWait": sc(n.RolloutWait), "issues": list(n.Issues), "userTasks": list(n.UserTasks)}
}

func kmemOverviewBody(boot string, data *kmemPanelData, assistant map[string]any, sc func(string) string) map[string]any {
	text := strings.TrimSpace(data.brief)
	if r := data.needs.MaintReport; r != "" {
		text = strings.ReplaceAll(text, r, filepath.Base(r)) // the report as a file name only
	}
	brief, _ := capText(sc(text), kmemPhoneBriefMax)
	big := []map[string]any{}
	for _, t := range data.topics {
		if t.KB > 8 {
			big = append(big, map[string]any{"name": sc(t.Name), "kb": t.KB})
		}
	}
	return map[string]any{"v": 1, "boot": boot, "needs": kmemNeedsBody(data.needs, sc), "assistant": assistant,
		"brief": brief, "bigTopics": big,
		"counts": map[string]any{"topics": len(data.topics), "handoffs": len(data.handoffs), "openTasks": len(data.tasks)},
		"readMs": data.read.UnixMilli()}
}

// --- the assistant's state (UI thread) ---

// kmemAssistSummary: the kmem assistant as phones see it (§4.2). full adds
// the model, the save countdown and the reason line (overview); the stream
// change carries only state, waiting, epoch and rev.
func (a *phoneAPI) kmemAssistSummary(full bool) map[string]any {
	d := a.d
	as := d.assist()
	s := as.s
	state := "off"
	switch as.state {
	case asProbing, asStarting:
		state = "starting"
	case asPanels:
		state = "panels"
	case asElsewhere:
		state = "elsewhere"
	case asLive:
		state = "live"
		if s == nil || !s.running() {
			state = "failed"
		}
	case asSaving:
		state = "saving"
	case asStopping:
		state = "stopping"
	}
	waiting := ""
	if as.state == asSaving && as.choice != nil {
		waiting = "saveTimeout"
	} else if s != nil && s.running() {
		for _, it := range s.conv.PendingPermissions() {
			switch {
			case it.Kind == chat.Plan:
				waiting = "plan"
			case isQuestion(it):
				if waiting != "plan" {
					waiting = "question"
				}
			default:
				if waiting == "" {
					waiting = "permission"
				}
			}
		}
	}
	out := map[string]any{"state": state, "waiting": waiting, "epoch": 0, "rev": 0}
	if s != nil {
		if t := a.tracks[s.id]; t != nil {
			out["epoch"], out["rev"] = t.epoch, t.rev
		}
	}
	if !full {
		return out
	}
	sc := kmemScrubber()
	model := d.kmemAssistModel()
	if s != nil && s.model != "" {
		model = s.model
	}
	src := "kmem assistant setting"
	if d.app.Preferences().String(prefKmemAssistModel) == "" {
		src = "New sessions"
	}
	out["model"], out["modelSource"] = model, src
	out["actions"] = d.kmemAssistActions(as) // phase 2: what /phone/kmem/assistant/* accepts now
	if q := d.kmemAssistQuestion(as); q != nil {
		out["question"] = q
	}
	if as.state == asSaving && !as.untilSaved && as.choice == nil {
		if left := time.Until(as.saveBy); left > 0 {
			out["saveLeftS"] = int((left + time.Second - 1) / time.Second)
		}
	}
	if state == "failed" && s != nil && s.failed != "" {
		out["reason"] = sc(oneLine(s.failed, 200))
	}
	if as.reason != "" && state != "live" && state != "saving" && state != "stopping" {
		out["reason"] = sc(as.reason)
		if as.button != "" {
			out["button"] = as.button
		}
	} else if state == "off" && as.userStopped {
		out["reason"] = "kmem-lock-audit is stopped; its handoff keeps its work for the next start."
		out["button"] = "Start assistant"
	}
	return out
}

// kmemAssistTrackID: the assistant's session id while a tracker is kept for
// it (scan keeps that one hidden tracker; "" when there is no assistant).
func (a *phoneAPI) kmemAssistTrackID() string {
	if s := a.d.assist().s; s != nil {
		return s.id
	}
	return ""
}

// kmemChange: the "kmem" stream change (UI thread), or nil when nothing a
// phone shows changed since the last one. It reads only Quill Desktop's
// state (kmemNeedsNow, the assistant), never kmem, and no files: it runs on
// every 500 ms scan.
func (a *phoneAPI) kmemChange() map[string]any {
	body := map[string]any{"type": "kmem", "needs": map[string]any{"count": a.d.kmemNeedsNow.Count()}, "assistant": a.kmemAssistSummary(false)}
	js, _ := json.Marshal(body)
	if string(js) == a.lastKmem {
		return nil
	}
	a.lastKmem = string(js)
	return body
}

// --- personal data ---

var (
	kmemScrubHome = homeDir // tests: a placeholder home
	kmemScrubUser = func() string {
		if u, err := user.Current(); err == nil {
			return u.Username
		}
		return ""
	}
	kmemEmail = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
)

// kmemScrubber replaces the home folder by ~, the user name (as a whole
// word) by "user" and e-mail addresses by "(e-mail)" (§5.5).
func kmemScrubber() func(string) string {
	home := strings.TrimRight(kmemScrubHome(), "/")
	name := kmemScrubUser()
	var nameRe *regexp.Regexp
	if name != "" && name != "user" {
		nameRe = regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	}
	return func(s string) string {
		if s == "" {
			return s
		}
		if home != "" && home != "/" {
			s = strings.ReplaceAll(s, home, "~")
		}
		s = kmemEmail.ReplaceAllString(s, "(e-mail)")
		if nameRe != nil {
			s = nameRe.ReplaceAllString(s, "user")
		}
		return s
	}
}
