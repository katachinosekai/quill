package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/folders"
	"github.com/katachinosekai/quill/internal/transcript"
)

const (
	apiScanEvery  = 500 * time.Millisecond
	apiLiveFor    = 2 * time.Minute
	apiRing       = 1000
	apiScanTail   = 400
	apiWindow     = 200
	apiWindowMax  = 500
	apiResultMax  = 2 << 10
	apiDiffMax    = 400
	apiStreamWait = 25
	apiTextMax    = 32 << 10
)

type phoneAPI struct {
	d      *desk
	server *folders.Server
	boot   string

	mu      sync.Mutex
	next    int64
	ring    []apiChange
	wake    chan struct{}
	live    time.Time
	streams map[string]chan struct{}
	stop    chan struct{}

	tracks     map[string]*itemTrack
	lastSess   map[string]string
	lastIDs    string
	lastUse    string
	lastCtl    map[string]bool
	lastSet    string
	lastTopics string // the topics list last sent as a "topics" change

	lan *lanMem // home-network unlock (phoneunlock.go)

	kmem     phoneKmem // /phone/kmem/* reads (phonekmem.go)
	lastKmem string    // the last "kmem" stream change

	lastQuill string // the last "quill" stream change (phonequillop.go)

	files     *fileGuard      // /phone/session/{id}/file (phonefiles.go)
	lastFiles map[string]bool // per phone: the last "files" capability sent
}

type apiChange struct {
	cursor int64
	body   map[string]any
}

func newPhoneAPI(d *desk, s *folders.Server) *phoneAPI {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	a := &phoneAPI{d: d, server: s, boot: hex.EncodeToString(b), next: 1, wake: make(chan struct{}), stop: make(chan struct{}), streams: map[string]chan struct{}{},
		tracks: map[string]*itemTrack{}, lastSess: map[string]string{}, lastCtl: map[string]bool{}, lan: newLanMem(),
		files: newFileGuard(), lastFiles: map[string]bool{}}
	go a.loop()
	return a
}

func (a *phoneAPI) loop() {
	t := time.NewTicker(apiScanEvery)
	defer t.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-t.C:
		}
		a.mu.Lock()
		live := time.Since(a.live) < apiLiveFor
		a.mu.Unlock()
		if live {
			a.d.do(a.scan)
		}
	}
}

func (a *phoneAPI) close() {
	close(a.stop)
}

func (a *phoneAPI) touch() {
	a.mu.Lock()
	a.live = time.Now()
	a.mu.Unlock()
}

func (a *phoneAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.touch()
	path := strings.TrimPrefix(r.URL.Path, "/phone/")
	parts := strings.Split(path, "/")
	switch {
	case r.Method == http.MethodPost && path == "hello":
		a.hello(w, r)
	case r.Method == http.MethodGet && path == "sessions":
		a.sessions(w, folders.PhoneID(r))
	case r.Method == http.MethodGet && path == "folders":
		a.folders(w, r)
	case r.Method == http.MethodGet && path == "topics":
		a.topicsView(w, r)
	case r.Method == http.MethodGet && path == "topics/search":
		a.topicSearch(w, r)
	case r.Method == http.MethodGet && path == "topics/slug":
		a.topicSlug(w, r)
	case r.Method == http.MethodGet && path == "handoffs":
		a.handoffs(w)
	case r.Method == http.MethodGet && path == "handoffs/slug":
		a.handoffSlug(w, r)
	case r.Method == http.MethodGet && path == "kmem/assistant/items":
		a.kmemAssistItems(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "kmem/assistant/"):
		a.kmemAssistWrite(w, r, strings.TrimPrefix(path, "kmem/assistant/"))
	case r.Method == http.MethodPost && strings.HasPrefix(path, "kmem/"):
		a.kmemWrite(w, r, path)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "kmem/"):
		a.kmemRoute(w, r, path)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "quill"):
		a.quillRoute(w, r, path)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "quill/"):
		a.quillWrite(w, r, path)
	case r.Method == http.MethodGet && path == "stream":
		a.stream(w, r)
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "session" && parts[2] == "items":
		a.items(w, r, parts[1])
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "session" && parts[2] == "file":
		a.file(w, r, parts[1])
	case r.Method == http.MethodGet && path == "settings/questions":
		a.featureQuestionsRoute(w)
	case r.Method == http.MethodPost && path == "settings/answer":
		a.featureAnswer(w, r)
	case r.Method == http.MethodPost && path == "settings":
		a.setSettings(w, r)
	case r.Method == http.MethodPost && path == "unlock":
		a.unlockRoute(w, r, folders.PhoneID(r))
	case r.Method == http.MethodPost && path == "unlock/touch":
		a.unlockAPI(w, r, path, folders.PhoneID(r))
	case r.Method == http.MethodPost && path == "unlock/challenge":
		a.unlockChallenge(w, r, folders.PhoneID(r))
	case r.Method == http.MethodPost && path == "unlock/key":
		a.unlockKey(w, r, folders.PhoneID(r))
	case r.Method == http.MethodPost:
		a.write(w, r, path, parts)
	default:
		folders.WriteError(w, http.StatusNotFound, "not_found", "unknown path")
	}
}

func (a *phoneAPI) ui(f func()) {
	a.d.doWait(f)
}

func (a *phoneAPI) control(phone string) bool {
	if !a.d.app.Preferences().Bool("phoneControl") {
		return false
	}
	for _, p := range a.d.phones() {
		if p.ID == phone {
			return p.Control
		}
	}
	return false
}

func (a *phoneAPI) hello(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		App  string `json:"app"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", "body must be JSON {name, app}")
		return
	}
	phone := folders.PhoneID(r)
	conn, snap := lanConnOf(r), lanSnapshot()
	var ctl, unlock, lan, kmemOn, files, endCheck, topics, quillOn bool
	a.ui(func() {
		list := a.d.phones()
		changed := false
		for i := range list {
			if list[i].ID != phone {
				continue
			}
			if name := strings.TrimSpace(in.Name); name != "" && list[i].Name != name {
				list[i].Name = clip(name, 60)
				changed = true
			}
			// The first hello decides the kind; later ones can't change it,
			// so a PC's key can't pass itself off as a phone.
			if list[i].Kind == "" {
				list[i].Kind = "phone"
				if strings.HasPrefix(in.App, "quill-desktop") {
					list[i].Kind = "pc"
				}
				changed = true
			}
			unlock = list[i].NeedsUnlock()
			files = filesOn(list[i])
		}
		if changed {
			a.d.app.Preferences().SetString("phones", folders.PhonesJSON(list))
			a.d.updatePhoneList()
		}
		ctl = a.control(phone)
		lan = a.lanHello(phone, conn, snap)
		kmemOn = a.d.kmemOn()
		endCheck = kmemOn && a.d.kmemFeatureCached(endCheckFeature)
		topics = a.d.topicMode()
		quillOn = a.d.quillOpOn()
	})
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "phoneId": phone, "control": ctl, "boot": a.boot, "quill": version, "unlock": unlock, "lanUnlock": lan,
		"kmem": map[string]any{"api": kmemPhoneAPI, "on": kmemOn}, "files": map[string]any{"api": filesPhoneAPI, "on": files},
		"topics":        map[string]any{"api": topicsPhoneAPI, "on": topics, "endCheck": endCheck},
		"quillOperator": map[string]any{"api": quillOpPhoneAPI, "on": quillOn}})
}

type apiSession struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Folder     string     `json:"folder"`
	FolderName string     `json:"folderName"`
	Group      string     `json:"group"`
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
	Busy       bool       `json:"busy"`
	Kmem       string     `json:"kmem,omitempty"`
	Model      string     `json:"model"`
	Mode       string     `json:"mode"`
	Backend    string     `json:"backend"`
	Context    apiContext `json:"context"`
	Pending    apiPending `json:"pending"`
	Queue      int        `json:"queue"`
	Background int        `json:"background"`
	Epoch      int        `json:"epoch"`
	Rev        int        `json:"rev"`
	UpdatedMs  int64      `json:"updatedMs"`
	Focused    bool       `json:"focused"`
	Tokens     int64      `json:"tokens"`
	Project    string     `json:"project"`
	Renamed    bool       `json:"renamed,omitempty"`
	// Tab: 0-based position among the open tabs the phone can see (Quill
	// Desktop's tab bar, left to right), -1 when the session is not an open
	// tab. Quill Android's portrait list follows it.
	Tab int `json:"tab"`
	// Suggestion: the prompt suggestion Quill Desktop shows as ghost text in
	// the session's empty message box (shownSuggestion, incl. the /clear
	// rule); left out when there is none.
	Suggestion string `json:"suggestion,omitempty"`
	// Topic fields (phone API topics level 1, docs/phone-api.md): the topic
	// the session coordinates (its kmem name), its title, the old kmem name
	// of a session that predates topic tabs (to merge), the subagents
	// (the strip's list) with their summed tokens, and the end-of-session
	// check while the session is ending.
	Topic       string      `json:"topic,omitempty"`
	TopicTitle  string      `json:"topicTitle,omitempty"`
	LegacyName  string      `json:"legacyName,omitempty"`
	Agents      []apiAgent  `json:"agents,omitempty"`
	AgentTokens int64       `json:"agentTokens,omitempty"`
	EndWait     *apiEndWait `json:"endWait,omitempty"`
	// AutoApprove: the session's auto-approve is on (no plan needed until
	// the user turns it off; autoapprove.go).
	AutoApprove bool `json:"autoApprove,omitempty"`
}

type apiContext struct {
	Used int `json:"used"`
	Max  int `json:"max"`
}

type apiPending struct {
	Permissions int `json:"permissions"`
	Questions   int `json:"questions"`
	Plans       int `json:"plans"`
}

var apiStatus = map[status]string{stNone: "stopped", stRunning: "running", stWaiting: "waiting", stIdle: "idle", stError: "error", stTerminal: "terminal", stRestart: "running"}

func (a *phoneAPI) sessionIDs() ([]string, map[string]string) {
	d := a.d
	group := map[string]string{}
	var ids []string
	// Topic mode (kmem memory on): only live sessions, grouped by topic.
	topicMode := d.topicMode()
	live := func(id string) bool {
		if !topicMode {
			return true
		}
		if s := d.sessions[id]; s != nil && s.running() {
			return true
		}
		return contains(d.tabs, id)
	}
	title := func(id, g string) string {
		if t, ok := cachedTopic(d.topicOf(id)); topicMode && ok {
			return t.Title
		}
		return g
	}
	for _, g := range d.groups {
		if g.key == placeArchive {
			continue
		}
		for _, id := range g.ids {
			if strings.HasPrefix(id, remotePrefix) || d.hiddenID(id, "") || !live(id) {
				continue
			}
			if _, ok := group[id]; !ok {
				group[id] = title(id, g.title)
				ids = append(ids, id)
			}
		}
	}
	for _, id := range d.tabs {
		if d.staleTab(id) || strings.HasPrefix(id, remotePrefix) || d.hiddenID(id, "") {
			continue
		}
		if _, ok := group[id]; !ok {
			group[id] = title(id, "Open")
			ids = append(ids, id)
		}
	}
	return ids, group
}

// tabIndex is id's position in d.tabs counted over the tabs sessionIDs lists,
// or -1 when id is not one of them.
func (a *phoneAPI) tabIndex(id string) int {
	d := a.d
	n := 0
	for _, t := range d.tabs {
		if d.staleTab(t) || strings.HasPrefix(t, remotePrefix) || d.hiddenID(t, "") {
			continue
		}
		if t == id {
			return n
		}
		n++
	}
	return -1
}

func (a *phoneAPI) session(id, group string) apiSession {
	d := a.d
	title, _, st := d.rowText(id)
	info := d.infos[id]
	out := apiSession{ID: id, Tab: a.tabIndex(id), Title: title, Group: group, Status: apiStatus[st], Folder: info.Cwd, Model: "", Mode: "default", Backend: "online", Kmem: d.org.Kmem[id], Tokens: int64(info.Usage.Total())}
	if !info.Last.IsZero() {
		out.UpdatedMs = info.Last.UnixMilli()
	}
	if s := d.sessions[id]; s != nil {
		out.Folder = firstNonEmpty(s.cwd, out.Folder)
		out.Model, out.Mode = s.model, s.mode
		if isLocal(s.model) {
			out.Backend = "local"
		}
		if st == stError {
			out.Error = s.failed
		}
		out.Busy = s.running() && (s.conv.Busy || s.proc.Busy())
		out.Context = apiContext{s.conv.Stats.ContextUsed, s.conv.Stats.ContextMax}
		for _, it := range s.conv.PendingPermissions() {
			switch {
			case it.Kind == chat.Plan:
				out.Pending.Plans++
			case isQuestion(it):
				out.Pending.Questions++
			default:
				out.Pending.Permissions++
			}
		}
		out.Queue = len(s.queue)
		out.AutoApprove = d.autoApproveShown(s)
		out.Suggestion = d.shownSuggestion(s)
		if s.running() {
			out.Background = s.conv.Background()
		}
		if t := a.tracks[id]; t != nil {
			out.Epoch, out.Rev = t.epoch, t.rev
		}
		out.Focused = d.current == s
		if !s.created.IsZero() && out.UpdatedMs == 0 {
			out.UpdatedMs = s.created.UnixMilli()
		}
	}
	_, out.Renamed = d.org.Titles[id]
	a.topicFields(&out)
	out.Project = d.projectOf(id)
	out.FolderName = filepath.Base(out.Folder)
	return out
}

func (a *phoneAPI) usage() map[string]any {
	d := a.d
	u := map[string]any{"paused": d.usagePaused, "pausedUntilMs": int64(0)}
	credits := creditsText(d.rate)
	if credits == "not reported yet" {
		credits = "not reported"
	}
	u["credits"] = credits
	u["todayTokens"], u["weekTokens"] = d.dayWeekTokens()
	if d.usagePaused && !d.pauseUntil.IsZero() && d.pauseUntil.Unix() > 0 {
		u["pausedUntilMs"] = d.pauseUntil.UnixMilli()
	}
	if w, ok := d.rate.Window("five_hour"); ok {
		u["fiveHour"] = map[string]any{"pct": pct(w.Utilization), "resetsAtMs": w.ResetsAt * 1000}
	}
	if w, ok := d.rate.Window("seven_day"); ok {
		u["week"] = map[string]any{"pct": pct(w.Utilization), "resetsAtMs": w.ResetsAt * 1000}
	}
	return u
}

func (a *phoneAPI) sessions(w http.ResponseWriter, phone string) {
	var body map[string]any
	a.ui(func() {
		a.scan()
		ids, group := a.sessionIDs()
		list := []apiSession{}
		for _, id := range ids {
			list = append(list, a.session(id, group[id]))
		}
		a.mu.Lock()
		cursor := a.next
		a.mu.Unlock()
		topics := a.topicList()
		body = map[string]any{
			"v": 1, "boot": a.boot, "cursor": cursor, "usage": a.usage(), "sessions": list, "control": a.control(phone),
			"models":   map[string]any{"online": modelChoices, "local": a.d.localModelList()},
			"modes":    map[string]any{"online": modeChoices, "local": localModes},
			"projects": a.projects(), "actions": a.d.phoneActions(), "settings": a.d.phoneSettings(),
		}
		if topics != nil {
			body["topics"] = topics
		}
	})
	folders.WriteJSON(w, http.StatusOK, body)
}

func (a *phoneAPI) knownFolders() []map[string]string {
	seen := map[string]bool{}
	out := []map[string]string{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, map[string]string{"path": p, "name": filepath.Base(p)})
	}
	for _, s := range a.d.sessions {
		if !a.d.hiddenID(s.id, s.cwd) {
			add(s.cwd)
		}
	}
	for _, info := range a.d.order {
		if !a.d.hiddenID(info.ID, info.Cwd) {
			add(info.Cwd)
		}
	}
	return out
}

// folders: every folder Quill Desktop knows; with ?topic=SLUG in topic mode
// the topic's working folders (topicFolders).
func (a *phoneAPI) folders(w http.ResponseWriter, r *http.Request) {
	var list []map[string]string
	slug := r.URL.Query().Get("topic")
	var fail *apiErr
	a.ui(func() {
		if slug != "" && a.d.topicMode() {
			if _, ok := cachedTopic(slug); !ok {
				fail = notFound("no topic " + slug)
				return
			}
			list = a.topicFolders(slug)
			return
		}
		list = a.knownFolders()
	})
	if fail != nil {
		folders.WriteError(w, fail.status, fail.code, fail.msg)
		return
	}
	out := map[string]any{"v": 1, "folders": list}
	if slug != "" {
		out["topic"] = slug
	}
	folders.WriteJSON(w, http.StatusOK, out)
}

type itemTrack struct {
	conv     *chat.Conversation
	epoch    int
	rev      int
	lastSeq  int
	queueSig string
	ptr      map[int]*chat.Item
	sig      map[int]itemTag
	cheap    map[int]uint64 // itemCheapTag, to skip hashing items that didn't change
	irev     map[int]int
}

// newItemTrack starts a session's tracker with an epoch and a rev that no
// earlier tracker for it handed out, also one from before a Quill Desktop
// restart: Quill Android keeps the largest rev it has seen across a restart
// (rev = max(rev, new)) and fetches the open chat only when a session's rev
// grows past it or its epoch changes, so revs that started over at 0 left
// the open chat without live updates (user 2026-11-22). rev starts at the
// clock in ms (a tracker bumps it far less than once per ms); the epoch, a
// clock value in seconds that fits Android's int, makes the phone's next
// incremental fetch get the fresh window instead of everything since its
// old rev.
var trackClock = time.Now // tests: a restart some time later

func newItemTrack(now time.Time) *itemTrack {
	return &itemTrack{epoch: int(now.Unix() % 1_000_000_000), rev: int(now.UnixMilli())}
}

func (t *itemTrack) reset(c *chat.Conversation) {
	t.conv = c
	t.epoch++
	t.lastSeq = 0
	t.ptr, t.sig, t.irev, t.cheap = map[int]*chat.Item{}, map[int]itemTag{}, map[int]int{}, map[int]uint64{}
}

// sigHashRecent: the newest items are always hashed (a reply can be
// replaced by one of the same length while it is finishing).
const sigHashRecent = 20

func (t *itemTrack) update(s *deskSession) {
	c := s.conv
	if t.conv != c {
		t.reset(c)
	}
	items := c.Items
	last := 0
	if n := len(items); n > 0 {
		last = items[n-1].Seq
	}
	if last < t.lastSeq {
		t.reset(c)
	}
	start := max(0, len(items)-apiScanTail)
	if t.lastSeq == 0 {
		start = 0
	} else if j := sort.Search(len(items), func(i int) bool { return items[i].Seq > t.lastSeq }); j < start {
		start = j
	}
	for _, it := range items[start:] {
		if p, ok := t.ptr[it.Seq]; ok && p != it {
			t.reset(c)
			t.update(s)
			return
		}
	}
	recent := len(items) - sigHashRecent
	for i, it := range items[start:] {
		// Older items: only when length or flags changed is the text hashed
		// (hashing 400 long replies on every scan cost 1.5 ms per session).
		cheap := itemCheapTag(it)
		if start+i < recent && t.cheap[it.Seq] == cheap && t.sig[it.Seq] != (itemTag{}) {
			continue
		}
		t.cheap[it.Seq] = cheap
		sig := itemSigTag(it)
		if t.sig[it.Seq] != sig {
			t.rev++
			t.sig[it.Seq], t.irev[it.Seq], t.ptr[it.Seq] = sig, t.rev, it
		}
	}
	t.lastSeq = last
	if q := strings.Join(s.queue, "\x00"); q != t.queueSig {
		t.queueSig = q
		t.rev++
	}
}

func (a *phoneAPI) track(s *deskSession) *itemTrack {
	t := a.tracks[s.id]
	if t == nil {
		t = newItemTrack(trackClock())
		a.tracks[s.id] = t
	}
	t.update(s)
	return t
}

func (a *phoneAPI) scan() {
	d := a.d
	ids, group := a.sessionIDs()
	var changes []map[string]any
	now := map[string]bool{}
	for _, id := range ids {
		now[id] = true
		if s := d.sessions[id]; s != nil && s.loaded {
			a.track(s)
		}
		js, _ := json.Marshal(a.session(id, group[id]))
		if a.lastSess[id] != string(js) {
			a.lastSess[id] = string(js)
			changes = append(changes, map[string]any{"type": "session", "session": json.RawMessage(js)})
		}
	}
	// The kmem assistant is hidden but keeps its tracker: its rev and epoch
	// go into the "kmem" stream change (docs/design/kmem-android.md §4.3).
	assistID := a.kmemAssistTrackID()
	if s := d.sessions[assistID]; s != nil && s.loaded {
		a.track(s)
	}
	quillID := a.quillOpTrackID()
	if s := d.sessions[quillID]; s != nil && s.loaded {
		a.track(s)
	}
	for id := range a.tracks {
		if id == assistID || id == quillID {
			continue // kept while stopped too: its chat stays readable on the phone
		}
		if d.sessions[id] == nil || d.hiddenID(id, "") {
			delete(a.tracks, id)
		}
	}
	for id := range a.lastSess {
		if !now[id] {
			delete(a.lastSess, id)
			changes = append(changes, map[string]any{"type": "removed", "id": id})
		}
	}
	if j := strings.Join(ids, ","); j != a.lastIDs {
		a.lastIDs = j
		changes = append(changes, map[string]any{"type": "order", "ids": ids})
	}
	ub, _ := json.Marshal(a.usage())
	if string(ub) != a.lastUse {
		a.lastUse = string(ub)
		changes = append(changes, map[string]any{"type": "usage", "usage": json.RawMessage(ub)})
	}
	sb, _ := json.Marshal(d.phoneSettings())
	if string(sb) != a.lastSet {
		first := a.lastSet == ""
		a.lastSet = string(sb)
		if !first {
			changes = append(changes, map[string]any{"type": "settings", "settings": json.RawMessage(sb)})
		}
	}
	if tl := a.topicList(); tl != nil {
		tb, _ := json.Marshal(tl)
		if string(tb) != a.lastTopics {
			first := a.lastTopics == ""
			a.lastTopics = string(tb)
			if !first {
				changes = append(changes, map[string]any{"type": "topics", "topics": json.RawMessage(tb)})
			}
		}
	}
	if c := a.kmemChange(); c != nil {
		changes = append(changes, c)
	}
	if c := a.quillChange(); c != nil {
		changes = append(changes, c)
	}
	for _, p := range d.phones() {
		c := a.control(p.ID)
		if old, ok := a.lastCtl[p.ID]; !ok || old != c {
			a.lastCtl[p.ID] = c
			if ok {
				changes = append(changes, map[string]any{"type": "control", "control": c, "phone": p.ID})
			}
		}
		// The file switch (Quill Desktop → Devices) updates hello's "files"
		// capability live.
		f := filesOn(p)
		if old, ok := a.lastFiles[p.ID]; !ok || old != f {
			a.lastFiles[p.ID] = f
			if ok {
				changes = append(changes, map[string]any{"type": "files", "files": map[string]any{"api": filesPhoneAPI, "on": f}, "phone": p.ID})
			}
		}
	}
	if len(changes) == 0 {
		return
	}
	a.mu.Lock()
	for _, c := range changes {
		a.ring = append(a.ring, apiChange{a.next, c})
		a.next++
	}
	if len(a.ring) > apiRing {
		a.ring = append([]apiChange(nil), a.ring[len(a.ring)-apiRing:]...)
	}
	wake := a.wake
	a.wake = make(chan struct{})
	a.mu.Unlock()
	close(wake)
}

func (a *phoneAPI) since(cursor int64, phone string) ([]map[string]any, int64, bool, chan struct{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cursor > a.next || cursor < a.next-int64(len(a.ring)) {
		return nil, a.next, true, a.wake
	}
	out := []map[string]any{}
	for _, c := range a.ring {
		if c.cursor < cursor {
			continue
		}
		if p, ok := c.body["phone"]; ok {
			// A change for one phone only: sent to it, without the id.
			if p != phone {
				continue
			}
			body := map[string]any{}
			for k, v := range c.body {
				if k != "phone" {
					body[k] = v
				}
			}
			out = append(out, body)
			continue
		}
		out = append(out, c.body)
	}
	return out, a.next, false, a.wake
}

func (a *phoneAPI) stream(w http.ResponseWriter, r *http.Request) {
	phone := folders.PhoneID(r)
	q := r.URL.Query()
	cursor, err := strconv.ParseInt(q.Get("cursor"), 10, 64)
	if err != nil || cursor < 0 {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", "cursor must be a number from /phone/sessions")
		return
	}
	wait := apiStreamWait
	if v := q.Get("wait"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			wait = min(n, apiStreamWait)
		}
	}
	mine := make(chan struct{})
	a.mu.Lock()
	if old := a.streams[phone]; old != nil {
		close(old)
	}
	a.streams[phone] = mine
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.streams[phone] == mine {
			delete(a.streams, phone)
		}
		a.mu.Unlock()
	}()
	a.d.do(a.scan)
	changes, next, reset, wake := a.since(cursor, phone)
	if !reset && len(changes) == 0 && wait > 0 {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Duration(wait+5) * time.Second))
		t := time.NewTimer(time.Duration(wait) * time.Second)
		defer t.Stop()
		revoked := a.server.Revoked()
		select {
		case <-wake:
		case <-t.C:
		case <-mine:
		case <-revoked:
		case <-r.Context().Done():
			return
		}
		if !a.server.PhoneOK(phone) {
			folders.WriteError(w, http.StatusForbidden, "forbidden", "this phone was revoked")
			return
		}
		changes, next, reset, _ = a.since(cursor, phone)
	}
	if reset {
		changes = []map[string]any{}
	}
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "boot": a.boot, "cursor": next, "reset": reset, "changes": changes, "settings": a.d.phoneSettings()})
}

var apiKinds = map[chat.Kind]string{chat.User: "user", chat.Assistant: "assistant", chat.Tool: "tool", chat.Permission: "permission",
	chat.Notice: "notice", chat.Error: "error", chat.Thinking: "thinking", chat.Plan: "plan", chat.Rotate: "rotate"}

var apiStates = map[chat.PermState]string{chat.PermPending: "pending", chat.PermAllowed: "allowed", chat.PermAllowedAlways: "allowedAlways",
	chat.PermDenied: "denied", chat.PermCancelled: "cancelled", chat.PermAutoAllowed: "autoAllowed", chat.PermPlanAsked: "planAsked",
	chat.PermRetryAllowed: "retryAllowed", chat.PermRuleAllowed: "ruleAllowed"}

func capText(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// headText keeps at most n bytes of s, never ending inside a UTF-8 character.
func headText(s string, n int) string {
	h, _ := capText(s, n)
	return h
}

// tailText keeps at most n trailing bytes of s, never starting inside a UTF-8 character.
func tailText(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	cut := len(s) - n
	for cut < len(s) && !utf8Start(s[cut]) {
		cut++
	}
	return s[cut:]
}

func utf8Start(b byte) bool { return b&0xc0 != 0x80 }

func requestID(it *chat.Item) string {
	if it.RequestID != "" && it.Kind != chat.Rotate {
		return it.RequestID
	}
	return "seq:" + strconv.Itoa(it.Seq)
}

func (a *phoneAPI) item(s *deskSession, it *chat.Item, rev int, open *chat.Item) map[string]any {
	kind := apiKinds[it.Kind]
	if it.Kind == chat.Permission && isQuestion(it) {
		kind = "question"
	}
	m := map[string]any{"seq": it.Seq, "rev": rev, "kind": kind, "text": it.Text, "streaming": it.Streaming, "inPlan": it.InPlan}
	if !it.Time.IsZero() {
		m["timeMs"] = it.Time.UnixMilli() // when it was said (message times)
	}
	switch it.Kind {
	case chat.Tool:
		res, cut := capText(it.Result, apiResultMax)
		m["tool"] = map[string]any{"name": it.ToolName, "id": it.ToolID, "summary": it.Summary, "done": it.HasResult, "isError": it.ResultError, "result": res, "resultTruncated": cut}
		if len(it.Diff) > 0 {
			lines := []map[string]string{}
			for i, l := range it.Diff {
				if i >= apiDiffMax {
					break
				}
				lines = append(lines, map[string]string{"op": string([]byte{l.Op}), "text": l.Text})
			}
			m["diff"] = map[string]any{"path": it.DiffPath, "lines": lines, "truncated": len(it.Diff) > apiDiffMax}
		}
	case chat.Permission, chat.Plan, chat.Rotate:
		req := map[string]any{"id": requestID(it), "state": apiStates[it.PermState]}
		if it.Kind == chat.Plan {
			req["planState"] = planState(s.conv, it) // active|approved|rejected|replaced|moved_on
		}
		switch {
		case it.Kind == chat.Plan:
			text := s.conv.PlanText(it)
			m["text"] = text
			req["steps"] = planSteps(text)
			// The options the approval question offers, parsed here so every
			// client shows the same Approve buttons (planchoices.go).
			if qs := planItemQuestions(s, it); len(qs) > 0 {
				req["questions"] = qs
			}
			if opts, rec := planChoices(planQuestion(s.conv, it)); len(opts) > 0 {
				req["choices"] = opts
				if rec >= 0 {
					req["recommended"] = rec
				}
			}
			// The approval question's own options (planapproval.go): Quill
			// Desktop shows these instead of Approve / Don't run; answer with
			// approvePlan + approvalChoice (a stop option turns the plan down).
			if a := planItemApproval(s, it); a != nil {
				req["approval"] = a
			}
		case it.Kind == chat.Rotate:
		case isQuestion(it):
			req["tool"] = it.ToolName
			var in struct {
				Questions []struct {
					Question    string `json:"question"`
					Header      string `json:"header"`
					MultiSelect bool   `json:"multiSelect"`
					Options     []struct {
						Label       string `json:"label"`
						Description string `json:"description"`
					} `json:"options"`
				} `json:"questions"`
			}
			_ = json.Unmarshal(it.Input, &in)
			qs := []map[string]any{}
			for _, q := range in.Questions {
				opts := []map[string]string{}
				for _, o := range q.Options {
					opts = append(opts, map[string]string{"label": o.Label, "description": o.Description})
				}
				qs = append(qs, map[string]any{"question": q.Question, "header": q.Header, "multiSelect": q.MultiSelect, "options": opts})
			}
			req["questions"] = qs
		default:
			detail, _ := capText(firstNonEmpty(it.Summary, it.ToolName), apiResultMax)
			req["tool"] = it.ToolName
			req["detail"] = detail
			req["canAllowAlways"] = true
		}
		m["request"] = req
	case chat.Assistant:
		if !it.Streaming {
			text, _ := chat.SplitSources(it.Text)
			if _, opts, ok := parseChoices(text[strings.LastIndex(text, "\n")+1:]); ok {
				m["choices"] = opts
				m["choicesOpen"] = it == open
				if rec := choiceRecommended(text[strings.LastIndex(text, "\n")+1:], len(opts)); rec >= 0 {
					m["choicesRecommended"] = rec // Quill Android marks that button "(recommended)"
				}
			}
		}
	}
	return m
}

func openChoiceItem(c *chat.Conversation) *chat.Item {
	for i := len(c.Items) - 1; i >= 0; i-- {
		it := c.Items[i]
		if it.Kind == chat.User || it.Kind == chat.Plan {
			return nil
		}
		if it.Kind == chat.Assistant && strings.TrimSpace(it.Text) != "" && !it.Streaming && asksNewQuestion(it.Text) {
			return it // plain status replies after it don't supersede it
		}
	}
	return nil
}

func (a *phoneAPI) preload(id string) bool {
	found := true
	var path string
	var mod time.Time
	a.ui(func() {
		info, known := a.d.infos[id]
		s := a.d.sessions[id]
		if s != nil {
			s.touched = time.Now() // a phone read keeps it loaded (unload.go)
		}
		if !known && s == nil || a.d.hiddenID(id, "") {
			found = false // the kmem window's assistant is not on the phone
			return
		}
		if known && (s == nil || !s.loaded && !s.running()) {
			path, mod = info.Path, info.ModTime
		}
	})
	if !found || path == "" {
		return found
	}
	conv, _ := transcript.Load(path)
	a.ui(func() {
		s := a.d.session(id)
		if s == nil || conv == nil || s.loaded || s.running() || s.loading {
			return
		}
		s.conv, s.loaded, s.loadedMod = conv, true, mod
		s.touched = time.Now()
		a.d.noteWindow(s)
		if conv.Cwd != "" {
			s.cwd = conv.Cwd
		}
	})
	return true
}

// itemsQuery: the query of an items read (session/{id}/items and
// kmem/assistant/items).
type itemsQuery struct {
	limit, epoch, since, before int
	hasEpoch, hasBefore         bool
	have                        map[int]haveText
}

// parseItemsQuery reads limit, epoch+since, before and delta+have; on a bad
// query it wrote the error and returns false.
func parseItemsQuery(w http.ResponseWriter, r *http.Request) (itemsQuery, bool) {
	q := r.URL.Query()
	iq := itemsQuery{limit: apiWindow}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			folders.WriteError(w, http.StatusBadRequest, "bad_request", "limit must be a positive number")
			return iq, false
		}
		iq.limit = min(n, apiWindowMax)
	}
	num := func(k string) (int, bool, bool) {
		v := q.Get(k)
		if v == "" {
			return 0, false, true
		}
		n, err := strconv.Atoi(v)
		return n, true, err == nil && n >= 0
	}
	var hasSince, ok1, ok2, ok3 bool
	iq.epoch, iq.hasEpoch, ok1 = num("epoch")
	iq.since, hasSince, ok2 = num("since")
	iq.before, iq.hasBefore, ok3 = num("before")
	if !ok1 || !ok2 || !ok3 || iq.hasEpoch != hasSince {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", "epoch and since go together; numbers only")
		return iq, false
	}
	if q.Get("delta") == "1" {
		var err error
		if iq.have, err = parseHave(q.Get("have")); err != nil {
			folders.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
			return iq, false
		}
	}
	return iq, true
}

func (a *phoneAPI) items(w http.ResponseWriter, r *http.Request, id string) {
	iq, ok := parseItemsQuery(w, r)
	if !ok {
		return
	}
	var body map[string]any
	found := a.preload(id)
	if !found {
		folders.WriteError(w, http.StatusNotFound, "not_found", "no session "+id)
		return
	}
	a.ui(func() {
		s := a.d.session(id)
		if s == nil {
			found = false
			return
		}
		body = a.itemsBody(s, id, iq)
	})
	if !found {
		folders.WriteError(w, http.StatusNotFound, "not_found", "no session "+id)
		return
	}
	folders.WriteJSON(w, http.StatusOK, body)
}

// itemsBody: the items answer for s (UI thread); id is the id the phone
// knows it by.
func (a *phoneAPI) itemsBody(s *deskSession, id string, iq itemsQuery) map[string]any {
	t := a.track(s)
	items := s.conv.Items
	open := openChoiceItem(s.conv)
	reset := false
	incremental := false
	var pick []*chat.Item
	switch {
	case iq.hasEpoch && iq.epoch == t.epoch && iq.since <= t.rev:
		incremental = true
		for _, it := range items {
			if t.irev[it.Seq] > iq.since {
				pick = append(pick, it)
			}
		}
	case iq.hasBefore:
		end := len(items)
		for i, it := range items {
			if it.Seq >= iq.before {
				end = i
				break
			}
		}
		pick = items[max(0, end-iq.limit):end]
	default:
		reset = iq.hasEpoch
		pick = items[max(0, len(items)-iq.limit):]
	}
	out := []map[string]any{}
	for _, it := range pick {
		m := a.item(s, it, t.irev[it.Seq], open)
		if h, ok := iq.have[it.Seq]; ok && incremental {
			textDelta(m, it, h)
		}
		out = append(out, m)
	}
	hasMore := len(pick) > 0 && len(items) > 0 && pick[0] != items[0]
	queue := []map[string]any{}
	for i, txt := range s.queue {
		queue = append(queue, map[string]any{"index": i, "text": txt})
	}
	return map[string]any{"v": 1, "id": id, "epoch": t.epoch, "rev": t.rev, "reset": reset, "hasMore": hasMore, "items": out, "queue": queue}
}
