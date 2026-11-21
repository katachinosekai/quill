package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path/filepath"
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

	tracks   map[string]*itemTrack
	lastSess map[string]string
	lastIDs  string
	lastUse  string
	lastCtl  map[string]bool
}

type apiChange struct {
	cursor int64
	body   map[string]any
}

func newPhoneAPI(d *desk, s *folders.Server) *phoneAPI {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	a := &phoneAPI{d: d, server: s, boot: hex.EncodeToString(b), next: 1, wake: make(chan struct{}), stop: make(chan struct{}), streams: map[string]chan struct{}{},
		tracks: map[string]*itemTrack{}, lastSess: map[string]string{}, lastCtl: map[string]bool{}}
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
		a.sessions(w)
	case r.Method == http.MethodGet && path == "folders":
		a.folders(w)
	case r.Method == http.MethodGet && path == "stream":
		a.stream(w, r)
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "session" && parts[2] == "items":
		a.items(w, r, parts[1])
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
	var ctl bool
	a.ui(func() {
		if name := strings.TrimSpace(in.Name); name != "" {
			list := a.d.phones()
			for i := range list {
				if list[i].ID == phone && list[i].Name != name {
					list[i].Name = clip(name, 60)
					a.d.app.Preferences().SetString("phones", folders.PhonesJSON(list))
					a.d.updatePhoneList()
				}
			}
		}
		ctl = a.control(phone)
	})
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "phoneId": phone, "control": ctl, "boot": a.boot})
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
	for _, g := range d.groups {
		if g.key == placeArchive {
			continue
		}
		for _, id := range g.ids {
			if _, ok := group[id]; !ok {
				group[id] = g.title
				ids = append(ids, id)
			}
		}
	}
	for _, id := range d.tabs {
		if _, ok := group[id]; !ok {
			group[id] = "Open"
			ids = append(ids, id)
		}
	}
	return ids, group
}

func (a *phoneAPI) session(id, group string) apiSession {
	d := a.d
	title, _, st := d.rowText(id)
	info := d.infos[id]
	out := apiSession{ID: id, Title: title, Group: group, Status: apiStatus[st], Folder: info.Cwd, Model: "", Mode: "default", Backend: "online", Kmem: d.org.Kmem[id]}
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
	out.FolderName = filepath.Base(out.Folder)
	return out
}

func (a *phoneAPI) usage() map[string]any {
	d := a.d
	u := map[string]any{"paused": d.usagePaused, "pausedUntilMs": int64(0)}
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

func (a *phoneAPI) sessions(w http.ResponseWriter) {
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
		body = map[string]any{
			"v": 1, "boot": a.boot, "cursor": cursor, "usage": a.usage(), "sessions": list,
			"models": map[string]any{"online": modelChoices, "local": a.d.localModelList()},
			"modes":  map[string]any{"online": modeChoices, "local": localModes},
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
		add(s.cwd)
	}
	for _, info := range a.d.order {
		add(info.Cwd)
	}
	return out
}

func (a *phoneAPI) folders(w http.ResponseWriter) {
	var list []map[string]string
	a.ui(func() { list = a.knownFolders() })
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "folders": list})
}

type itemTrack struct {
	conv     *chat.Conversation
	epoch    int
	rev      int
	lastSeq  int
	queueSig string
	ptr      map[int]*chat.Item
	sig      map[int]string
	irev     map[int]int
}

func (t *itemTrack) reset(c *chat.Conversation) {
	t.conv = c
	t.epoch++
	t.lastSeq = 0
	t.ptr, t.sig, t.irev = map[int]*chat.Item{}, map[int]string{}, map[int]int{}
}

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
	}
	for _, it := range items[start:] {
		if p, ok := t.ptr[it.Seq]; ok && p != it {
			t.reset(c)
			t.update(s)
			return
		}
	}
	for _, it := range items[start:] {
		sig := itemSig(it)
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
		t = &itemTrack{}
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
	for _, p := range d.phones() {
		c := a.control(p.ID)
		if old, ok := a.lastCtl[p.ID]; !ok || old != c {
			a.lastCtl[p.ID] = c
			if ok {
				changes = append(changes, map[string]any{"type": "control", "control": c, "phone": p.ID})
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
			if p != phone {
				continue
			}
			body := map[string]any{"type": "control", "control": c.body["control"]}
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
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "boot": a.boot, "cursor": next, "reset": reset, "changes": changes})
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
		switch {
		case it.Kind == chat.Plan:
			text := s.conv.PlanText(it)
			m["text"] = text
			req["steps"] = planSteps(text)
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
			text := strings.TrimRight(it.Text, " \n")
			if _, opts, ok := parseChoices(text[strings.LastIndex(text, "\n")+1:]); ok {
				m["choices"] = opts
				m["choicesOpen"] = it == open
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
		if it.Kind == chat.Assistant && strings.TrimSpace(it.Text) != "" {
			return it
		}
	}
	return nil
}

func (a *phoneAPI) items(w http.ResponseWriter, r *http.Request, id string) {
	q := r.URL.Query()
	limit := apiWindow
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			folders.WriteError(w, http.StatusBadRequest, "bad_request", "limit must be a positive number")
			return
		}
		limit = min(n, apiWindowMax)
	}
	num := func(k string) (int, bool, bool) {
		v := q.Get(k)
		if v == "" {
			return 0, false, true
		}
		n, err := strconv.Atoi(v)
		return n, true, err == nil && n >= 0
	}
	epoch, hasEpoch, ok1 := num("epoch")
	since, hasSince, ok2 := num("since")
	before, hasBefore, ok3 := num("before")
	if !ok1 || !ok2 || !ok3 || hasEpoch != hasSince {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", "epoch and since go together; numbers only")
		return
	}
	var body map[string]any
	found := true
	var path string
	var mod time.Time
	a.ui(func() {
		info, known := a.d.infos[id]
		s := a.d.sessions[id]
		if !known && s == nil {
			found = false
			return
		}
		if known && (s == nil || !s.loaded && !s.running()) {
			path, mod = info.Path, info.ModTime
		}
	})
	if !found {
		folders.WriteError(w, http.StatusNotFound, "not_found", "no session "+id)
		return
	}
	var conv *chat.Conversation
	if path != "" {
		conv, _ = transcript.Load(path)
	}
	a.ui(func() {
		d := a.d
		s := d.session(id)
		if s == nil {
			found = false
			return
		}
		if conv != nil && !s.loaded && !s.running() && !s.loading {
			s.conv, s.loaded, s.loadedMod = conv, true, mod
			if conv.Cwd != "" {
				s.cwd = conv.Cwd
			}
		}
		t := a.track(s)
		items := s.conv.Items
		open := openChoiceItem(s.conv)
		reset := false
		var pick []*chat.Item
		switch {
		case hasEpoch && epoch == t.epoch && since <= t.rev:
			for _, it := range items {
				if t.irev[it.Seq] > since {
					pick = append(pick, it)
				}
			}
		case hasBefore:
			end := len(items)
			for i, it := range items {
				if it.Seq >= before {
					end = i
					break
				}
			}
			pick = items[max(0, end-limit):end]
		default:
			reset = hasEpoch
			pick = items[max(0, len(items)-limit):]
		}
		out := []map[string]any{}
		for _, it := range pick {
			out = append(out, a.item(s, it, t.irev[it.Seq], open))
		}
		hasMore := len(pick) > 0 && len(items) > 0 && pick[0] != items[0]
		queue := []map[string]any{}
		for i, txt := range s.queue {
			queue = append(queue, map[string]any{"index": i, "text": txt})
		}
		body = map[string]any{"v": 1, "id": id, "epoch": t.epoch, "rev": t.rev, "reset": reset, "hasMore": hasMore, "items": out, "queue": queue}
	})
	if !found {
		folders.WriteError(w, http.StatusNotFound, "not_found", "no session "+id)
		return
	}
	folders.WriteJSON(w, http.StatusOK, body)
}
