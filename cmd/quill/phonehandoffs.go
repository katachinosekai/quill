package main

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/folders"
)

// topicsPhoneAPI is the level in hello's "topics" entry (docs/phone-api.md,
// "Topics"); a breaking change raises it.
const topicsPhoneAPI = 1

// updateAndroid is what an older Quill Android gets when it uses the
// handoff start of the new-session sheet while topics are on.
const updateAndroid = "Update Quill Android: this Quill Desktop opens topics now. Use New session and pick a topic (openTopic); resuming handoffs and free names are gone."

// topicMigratedKey is the marker the topic migration sets (sub F,
// topicmigrate.go: topicMigration=1); absent means topic mode is off.
const topicMigratedKey = "topicMigration"

// topicMode: topic tabs are really on: kmem memory in use, kmem has the
// end-of-session check (1.10.0+) and the topic migration ran. Until then the
// phone API behaves as before. UI thread.
func (d *desk) topicMode() bool {
	return d.kmemOn() && d.kmemFeatureCached(endCheckFeature) && d.app.Preferences().Int(topicMigratedKey) >= 1
}

func updateRequired() *apiErr {
	return &apiErr{http.StatusUpgradeRequired, "update_required", updateAndroid}
}

type apiStart struct {
	Kmem  string `json:"kmem"`
	Name  string `json:"name"`
	Title string `json:"title"`
}

type apiOpenIn struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type apiHandoff struct {
	Name    string     `json:"name"`
	SavedMs int64      `json:"savedMs"`
	OpenIn  *apiOpenIn `json:"openIn"`
}

func (a *phoneAPI) handoffs(w http.ResponseWriter) {
	var hs []kmemHandoff
	avail, topics := false, false
	a.ui(func() { avail, topics = a.d.kmemAvailable(), a.d.topicMode() })
	if topics { // read-only and nothing to resume: the Topics view has the history
		folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "handoffs": []apiHandoff{}, "readOnly": true})
		return
	}
	if avail {
		hs = a.d.kmemHandoffs()
	}
	out := []apiHandoff{}
	a.ui(func() {
		for _, h := range hs {
			e := apiHandoff{Name: h.Name, SavedMs: h.Saved.UnixMilli()}
			if owner := a.d.handoffOwner(h.Name, &deskSession{}); owner != nil {
				title, _, _ := a.d.rowText(owner.id)
				e.OpenIn = &apiOpenIn{ID: owner.id, Title: title}
			}
			out = append(out, e)
		}
	})
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "handoffs": out})
}

func (a *phoneAPI) handoffSlug(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.URL.Query().Get("title"))
	slug := ""
	a.ui(func() {
		if title == "" {
			return
		}
		a.d.hsMu.Lock()
		known := a.d.lastHandoffs
		a.d.hsMu.Unlock()
		slug = kmemSlug(title, a.d.kmemTaken(known))
	})
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "slug": slug})
}

// apiAgent is one subagent in a session's "agents" (the strip's list).
type apiAgent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type,omitempty"`
	Tokens    int    `json:"tokens"`
	StartedMs int64  `json:"startedMs"`
	Done      bool   `json:"done"`
	DoneMs    int64  `json:"doneMs,omitempty"`
	Status    string `json:"status"`
}

// apiBlocker is one end-check blocker; text is the sentence Quill Desktop
// shows, the other fields are end-check's own (kmem 1.10.0).
type apiBlocker struct {
	Kind  string   `json:"kind"`
	ID    string   `json:"id,omitempty"`
	Title string   `json:"title,omitempty"`
	Name  string   `json:"name,omitempty"`
	PID   int      `json:"pid,omitempty"`
	What  string   `json:"what,omitempty"`
	Repo  string   `json:"repo,omitempty"`
	Paths []string `json:"paths,omitempty"`
	Need  string   `json:"need,omitempty"`
	Text  string   `json:"text"`
}

// apiEndWait is a session's end-of-session check while it runs: why
// (end, rotate, quit, checkpoint), checking (kmem is being asked), asked
// (the session was sent the blockers and must resolve them) and the blockers.
type apiEndWait struct {
	Why      string       `json:"why"`
	Checking bool         `json:"checking"`
	Asked    bool         `json:"asked"`
	Blockers []apiBlocker `json:"blockers"`
}

var endWhyName = map[endWhy]string{endForEnd: "end", endForRotate: "rotate", endForQuit: "quit", endForCheckpoint: "checkpoint"}

// topicFields fills the topic fields of an apiSession (UI thread).
func (a *phoneAPI) topicFields(out *apiSession) {
	d := a.d
	id := out.ID
	if slug := d.topicOf(id); slug != "" {
		out.Topic = slug
		if t, ok := cachedTopic(slug); ok {
			out.TopicTitle = t.Title
		}
	} else if k := d.org.Kmem[id]; k != "" && topicsLoaded() && !isHiddenSession(d.sessions[id]) {
		out.LegacyName = k // predates topic tabs: merge it into a topic
	}
	s := d.sessions[id]
	if s == nil {
		return
	}
	if s.conv != nil {
		var list []chat.AgentUse
		if d.agentStrip != nil {
			list = d.agentStrip.listed(s)
		} else {
			list = s.conv.Agents()
		}
		for _, g := range list {
			ag := apiAgent{ID: g.ID, Name: oneLine(strings.TrimSpace(g.Name), 80), Type: g.Type, Tokens: g.Tokens, StartedMs: g.Started.UnixMilli(), Done: !g.Running(), Status: g.Status}
			if !g.Running() {
				ag.DoneMs = g.Done.UnixMilli()
			}
			out.Agents = append(out.Agents, ag)
			out.AgentTokens += int64(g.Tokens)
		}
	}
	if ew := s.endWait; ew != nil {
		e := &apiEndWait{Why: endWhyName[ew.why], Checking: ew.checking, Asked: ew.asked, Blockers: []apiBlocker{}}
		for _, b := range ew.blockers {
			e.Blockers = append(e.Blockers, apiBlocker{Kind: b.Kind, ID: b.ID, Title: b.Title, Name: b.Name, PID: b.PID, What: b.What, Repo: b.Repo, Paths: b.Paths, Need: b.Need, Text: blockerText(b)})
		}
		out.EndWait = e
	}
}

// apiTopic is one entry of /phone/sessions "topics" and /phone/topics.
type apiTopic struct {
	Slug    string   `json:"slug"`
	Title   string   `json:"title"`
	Scope   []string `json:"scope"`
	Live    string   `json:"live"` // the live session's id, "" when none
	Status  string   `json:"status"`
	Claimed int      `json:"claimed"`
	Open    int      `json:"open"`
}

func (a *phoneAPI) topicEntry(t topicInfo) apiTopic {
	live := ""
	if owner := a.d.topicOwner(t.Slug, &deskSession{}); owner != nil && !isHiddenSession(owner) {
		live = owner.id
	}
	return apiTopic{Slug: t.Slug, Title: t.Title, Scope: firstNonEmptyList(t.Scope, []string{t.Slug}), Live: live,
		Status: kmemScrubber()(t.Status), Claimed: t.Claimed, Open: t.Open}
}

// topicList: the topics for the sessions body, from the cache only (UI
// thread); nil when topics are off. A stale cache is reloaded in the
// background.
func (a *phoneAPI) topicList() []apiTopic {
	if !a.d.topicMode() {
		return nil
	}
	a.d.refreshTopics()
	topicCache.mu.Lock()
	list := append([]topicInfo(nil), topicCache.list...)
	topicCache.mu.Unlock()
	out := []apiTopic{}
	for _, t := range list {
		out = append(out, a.topicEntry(t))
	}
	return out
}

// ensureTopics loads the topic cache when it is missing or older than a
// minute. Not on the UI thread.
func (a *phoneAPI) ensureTopics() {
	topicCache.mu.Lock()
	fresh := topicCache.loaded && time.Since(topicCache.at) < time.Minute
	topicCache.mu.Unlock()
	if !fresh {
		a.d.loadTopics()
	}
}

type apiTopicTask struct {
	ID         string `json:"id"`
	Prio       int    `json:"prio"`
	Title      string `json:"title"`
	Owner      string `json:"owner,omitempty"`
	Claim      string `json:"claim,omitempty"`
	Checkpoint string `json:"checkpoint,omitempty"`
}

type apiTopicView struct {
	apiTopic
	Tasks []apiTopicTask `json:"tasks"` // the claimed tasks, with their checkpoints
}

const topicViewTasks = 50

// topicsView is GET /phone/topics: the read-only Topics view (status,
// claimed tasks with checkpoints, open count) and the handoff history.
func (a *phoneAPI) topicsView(w http.ResponseWriter, r *http.Request) {
	phone := folders.PhoneID(r)
	on := false
	a.ui(func() { on = a.d.topicMode() })
	if !on {
		folders.WriteError(w, http.StatusServiceUnavailable, "kmem_unavailable", "kmem memory is off or kmem is not installed on Quill Desktop")
		return
	}
	a.ensureTopics()
	data := a.kmemData(r.Context(), w, phone)
	if data == nil {
		return
	}
	sc := kmemScrubber()
	var list []topicInfo
	topicCache.mu.Lock()
	list = append(list, topicCache.list...)
	topicCache.mu.Unlock()
	views := []apiTopicView{}
	hist := []map[string]any{}
	a.ui(func() {
		for _, t := range list {
			v := apiTopicView{apiTopic: a.topicEntry(t), Tasks: []apiTopicTask{}}
			for _, task := range data.rows {
				if task.Status != "claimed" || !contains(t.Scope, topLevel(task.Topic)) || len(v.Tasks) >= topicViewTasks {
					continue
				}
				title, _ := capText(sc(task.Title), kmemPhoneTitleMax)
				cp, _ := capText(sc(task.Checkpoint), kmemPhoneTitleMax)
				at := apiTopicTask{ID: task.ID, Prio: task.Prio, Title: title, Claim: kmemClaim(task.kmemTask), Checkpoint: cp}
				if task.Owner != nil {
					at.Owner = sc(*task.Owner)
				}
				v.Tasks = append(v.Tasks, at)
			}
			views = append(views, v)
		}
	})
	for _, h := range data.handoffs {
		e := map[string]any{"name": sc(h.Name), "items": h.Items}
		if t, err := time.Parse(time.RFC3339, h.Saved); err == nil {
			e["savedMs"] = t.UnixMilli()
		}
		hist = append(hist, e)
	}
	sort.SliceStable(hist, func(i, j int) bool {
		x, _ := hist[i]["savedMs"].(int64)
		y, _ := hist[j]["savedMs"].(int64)
		return x > y
	})
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "topics": views, "history": hist, "readMs": data.read.UnixMilli()})
}

// topicSlug is GET /phone/topics/slug?title=: what "New topic" with this
// title would open: its slug, and the existing topic (and its live session)
// when there is one.
func (a *phoneAPI) topicSlug(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.URL.Query().Get("title"))
	var fail *apiErr
	out := map[string]any{"v": 1, "slug": "", "exists": false, "live": ""}
	a.ui(func() {
		if !a.d.topicMode() {
			fail = &apiErr{http.StatusServiceUnavailable, "kmem_unavailable", "kmem memory is off or kmem is not installed on Quill Desktop"}
			return
		}
		if title == "" {
			return
		}
		slug := kmemSlug(title, func(string) bool { return false })
		if slug == "kmem" {
			fail = &apiErr{http.StatusUnprocessableEntity, "bad_request", "kmem is the kmem window's topic, not a tab"}
			return
		}
		out["slug"] = slug
		if t, ok := cachedTopic(slug); ok {
			out["exists"], out["title"], out["live"] = true, t.Title, a.topicEntry(t).Live
		}
	})
	if fail != nil {
		folders.WriteError(w, fail.status, fail.code, fail.msg)
		return
	}
	folders.WriteJSON(w, http.StatusOK, out)
}

// topicFolders: a topic's working folders: its coordinator's folder (live or
// last used) and the extra folders of its project (Quill Desktop's project
// folders). UI thread.
func (a *phoneAPI) topicFolders(slug string) []map[string]string {
	d := a.d
	out := []map[string]string{}
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, map[string]string{"path": p, "name": filepath.Base(p)})
		}
	}
	project := d.org.Projects[slug]
	if owner := d.topicOwner(slug, &deskSession{}); owner != nil && !isHiddenSession(owner) {
		add(firstNonEmpty(owner.cwd, d.infos[owner.id].Cwd))
		project = firstNonEmpty(project, d.projectOf(owner.id))
	}
	if t, ok := cachedTopic(slug); ok {
		project = firstNonEmpty(project, t.Title)
	}
	for _, dir := range d.org.Dirs[project] {
		add(dir)
	}
	return out
}

// openTopicWrite: POST /phone/sessions/new with openTopic {topic} or {new}
// (UI thread). A live topic's session is switched to; otherwise a fresh
// coordinator starts (topic-tabs §3.2). Optional folder: one of the topic's
// folders or a known one; default the home folder.
func (a *phoneAPI) openTopicWrite(in writeBody) (map[string]any, *apiErr) {
	d := a.d
	ot := in.OpenTopic
	if !d.topicMode() {
		return nil, &apiErr{http.StatusServiceUnavailable, "kmem_unavailable", "kmem memory is off or kmem is not installed on Quill Desktop"}
	}
	var t topicInfo
	isNew := false
	switch title := strings.TrimSpace(ot.New); {
	case (ot.Topic != "") == (title != ""):
		return nil, &apiErr{http.StatusUnprocessableEntity, "bad_request", "openTopic needs exactly one of topic and new"}
	case ot.Topic != "":
		if ot.Topic == "kmem" {
			return nil, &apiErr{http.StatusUnprocessableEntity, "bad_request", "kmem is the kmem window's topic, not a tab"}
		}
		var ok bool
		if t, ok = cachedTopic(ot.Topic); !ok {
			return nil, notFound("no topic " + ot.Topic)
		}
	default:
		if utf8.RuneCountInString(title) > apiTitleMax {
			return nil, badReq("new is longer than 200 characters")
		}
		slug := kmemSlug(title, func(string) bool { return false })
		if slug == "kmem" {
			return nil, &apiErr{http.StatusUnprocessableEntity, "bad_request", "kmem is the kmem window's topic, not a tab"}
		}
		var ok bool
		if t, ok = cachedTopic(slug); !ok { // a free name that is a topic opens that topic
			t = topicInfo{Slug: slug, Title: title, Scope: []string{slug}}
			isNew = true
		}
	}
	cwd := homeDir()
	if in.Folder != "" {
		okFolder := false
		for _, f := range append(a.topicFolders(t.Slug), a.knownFolders()...) {
			okFolder = okFolder || f["path"] == in.Folder
		}
		if !okFolder {
			return nil, badReq("folder must be one of /phone/folders?topic=" + t.Slug)
		}
		cwd = in.Folder
	}
	owner := d.topicOwner(t.Slug, &deskSession{})
	if isNew && owner == nil {
		rememberNewTopic(t)
	}
	s := d.openTopicInfo(t, isNew, cwd)
	_, group := a.sessionIDs()
	return map[string]any{"session": a.session(s.id, firstNonEmpty(group[s.id], t.Title)), "switched": owner != nil, "topic": map[string]string{"slug": t.Slug, "title": t.Title}}, nil
}

func (a *phoneAPI) checkStart(st *apiStart) *apiErr {
	switch st.Kmem {
	case "resume":
		if strings.TrimSpace(st.Name) == "" {
			return &apiErr{http.StatusUnprocessableEntity, "bad_request", "start.name is required to resume"}
		}
		if owner := a.d.handoffOwner(st.Name, &deskSession{}); owner != nil {
			return &apiErr{http.StatusConflict, "blocked", "that handoff is open in another session"}
		}
	case "new":
	default:
		return &apiErr{http.StatusUnprocessableEntity, "bad_request", "start.kmem is resume or new"}
	}
	return nil
}

func (a *phoneAPI) applyStart(s *deskSession, st *apiStart) {
	s.pickPending = true
	if st.Kmem == "resume" {
		a.d.pickResume(s, strings.TrimSpace(st.Name))
		return
	}
	a.d.pickNew(s, st.Title)
}
