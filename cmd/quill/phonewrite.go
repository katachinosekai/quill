package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/folders"
	"github.com/katachinosekai/quill/internal/protocol"
)

type apiErr struct {
	status int
	code   string
	msg    string
}

func (e *apiErr) Error() string { return e.msg }

func badReq(msg string) *apiErr   { return &apiErr{http.StatusBadRequest, "bad_request", msg} }
func notFound(msg string) *apiErr { return &apiErr{http.StatusNotFound, "not_found", msg} }
func stale(msg string) *apiErr    { return &apiErr{http.StatusConflict, "stale", msg} }

type writeBody struct {
	Text      string              `json:"text"`
	RequestID string              `json:"requestId"`
	Seq       int                 `json:"seq"`
	Action    string              `json:"action"`
	Answers   map[string][]string `json:"answers"`
	Index     *int                `json:"index"`
	Folder    string              `json:"folder"`
	Backend   string              `json:"backend"`
	Model     string              `json:"model"`
	Mode      string              `json:"mode"`
}

func (a *phoneAPI) write(w http.ResponseWriter, r *http.Request, path string, parts []string) {
	known := path == "usage/resume" || path == "sessions/new" ||
		len(parts) == 3 && parts[0] == "session" && slices.Contains([]string{"send", "answer", "interrupt"}, parts[2]) ||
		len(parts) == 4 && parts[0] == "session" && parts[2] == "queue" && (parts[3] == "delete" || parts[3] == "send-now")
	if !known {
		folders.WriteError(w, http.StatusNotFound, "not_found", "unknown path")
		return
	}
	var in writeBody
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", "body must be a JSON object")
		return
	}
	if len(in.Text) > apiTextMax {
		folders.WriteError(w, http.StatusRequestEntityTooLarge, "too_large", "text over 32 KiB")
		return
	}
	phone := folders.PhoneID(r)
	var out map[string]any
	var fail *apiErr
	a.ui(func() {
		if !a.control(phone) {
			fail = &apiErr{http.StatusForbidden, "control_off", "Let paired devices control sessions is off"}
			return
		}
		switch {
		case path == "usage/resume":
			a.d.resumeFromPause()
			out = map[string]any{"usage": a.usage()}
		case path == "sessions/new":
			out, fail = a.newSession(in)
		default:
			s := a.d.sessions[parts[1]]
			if s == nil {
				if _, ok := a.d.infos[parts[1]]; ok {
					s = a.d.session(parts[1])
				}
			}
			if s == nil {
				fail = notFound("no session " + parts[1])
				return
			}
			switch parts[2] {
			case "send":
				out, fail = a.send(s, in.Text)
			case "answer":
				out, fail = a.answer(s, in)
			case "interrupt":
				if !s.running() {
					fail = &apiErr{http.StatusConflict, "not_running", "no Claude process"}
					return
				}
				busy := s.conv.Busy || s.proc.Busy()
				a.d.interrupt(s)
				out = map[string]any{"wasBusy": busy}
			case "queue":
				out, fail = a.queueAction(s, parts[3], in)
			}
		}
	})
	if fail != nil {
		folders.WriteError(w, fail.status, fail.code, fail.msg)
		return
	}
	a.d.do(a.scan)
	out["v"] = 1
	folders.WriteJSON(w, http.StatusOK, out)
}

func (a *phoneAPI) queueJSON(s *deskSession) []map[string]any {
	q := []map[string]any{}
	for i, t := range s.queue {
		q = append(q, map[string]any{"index": i, "text": t})
	}
	return q
}

func (a *phoneAPI) send(s *deskSession, text string) (map[string]any, *apiErr) {
	d := a.d
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, badReq("text is empty")
	}
	if d.runLocalCommandFor(s, text) {
		return map[string]any{"queued": false}, nil
	}
	if s.takeover || s.loading || s.terminal != nil && !s.running() {
		return nil, &apiErr{http.StatusConflict, "blocked", "the session is loading, being taken over or open in a terminal"}
	}
	if d.usagePaused && !strings.HasPrefix(text, "/") {
		text = pausedNote + text
	}
	if s.pickPending || s.compacting || s.running() && (s.conv.Busy || s.proc.Busy()) {
		d.enqueue(s, text)
		return map[string]any{"queued": true, "queue": a.queueJSON(s)}, nil
	}
	d.sendTo(s, text)
	return map[string]any{"queued": false}, nil
}

func findItem(s *deskSession, requestID string, seq int) *chat.Item {
	if n, ok := strings.CutPrefix(requestID, "seq:"); ok {
		seq, _ = strconv.Atoi(n)
		requestID = ""
	}
	if requestID != "" {
		if it := s.conv.Permission(requestID); it != nil {
			return it
		}
		for _, it := range s.conv.Items {
			if it.RequestID == requestID {
				return it
			}
		}
		return nil
	}
	for _, it := range s.conv.Items {
		if it.Seq == seq {
			return it
		}
	}
	return nil
}

func (a *phoneAPI) answer(s *deskSession, in writeBody) (map[string]any, *apiErr) {
	d := a.d
	it := findItem(s, in.RequestID, in.Seq)
	if it == nil {
		return nil, notFound("no such request in this session")
	}
	pending := it.PermState == chat.PermPending
	question := it.Kind == chat.Permission && isQuestion(it)
	switch in.Action {
	case "allow", "allowAlways", "deny":
		if it.Kind != chat.Permission || question && in.Action != "deny" {
			return nil, badReq(in.Action + " does not fit this item")
		}
		if !pending {
			return nil, stale("the request is no longer pending")
		}
		dec := map[string]protocol.Decision{"allow": protocol.Allow, "allowAlways": protocol.AllowAlways, "deny": protocol.Deny}[in.Action]
		d.answer(s, it, dec)
		if it.PermState == chat.PermPending {
			return nil, &apiErr{http.StatusConflict, "not_running", "the session's Claude process is not running"}
		}
		if in.Action == "deny" && strings.TrimSpace(in.Text) != "" {
			a.send(s, in.Text)
		}
	case "approvePlan", "rejectPlan":
		if it.Kind != chat.Plan {
			return nil, badReq(in.Action + " needs a plan item")
		}
		if !pending {
			return nil, stale("the plan is no longer pending")
		}
		d.answerPlan(s, it, in.Action == "approvePlan")
		if in.Action == "rejectPlan" && strings.TrimSpace(in.Text) != "" {
			a.send(s, in.Text)
		}
	case "answer":
		if !question {
			return nil, badReq("answer needs a question item")
		}
		if !pending {
			return nil, stale("the question is no longer pending")
		}
		answers := map[string]string{}
		for _, q := range protocol.ParseQuestions(it.Input) {
			labels := in.Answers[q.Question]
			if len(labels) == 0 {
				return nil, badReq("every question needs an answer: " + q.Question)
			}
			answers[q.Question] = strings.Join(labels, ", ")
		}
		d.answerQuestions(s, it, answers)
		if it.PermState == chat.PermPending {
			return nil, &apiErr{http.StatusConflict, "not_running", "the session's Claude process is not running"}
		}
	case "choice":
		if it.Kind != chat.Assistant || in.Index == nil {
			return nil, badReq("choice needs an assistant item seq and an index")
		}
		text := strings.TrimRight(it.Text, " \n")
		_, opts, ok := parseChoices(text[strings.LastIndex(text, "\n")+1:])
		if !ok {
			return nil, badReq("this message has no choices")
		}
		if it != openChoiceItem(s.conv) {
			return nil, stale("the choices are no longer open")
		}
		if *in.Index < 0 || *in.Index >= len(opts) {
			return nil, badReq("index out of range")
		}
		d.pickChoice(s, it, opts[*in.Index])
	case "rotate", "dismiss":
		if it.Kind != chat.Rotate {
			return nil, badReq(in.Action + " needs a rotate item")
		}
		if !pending {
			return nil, stale("the rotate card is no longer pending")
		}
		d.answerRotate(s, it, in.Action == "rotate")
	default:
		return nil, badReq("unknown action " + in.Action)
	}
	t := a.track(s)
	return map[string]any{"item": a.item(s, it, t.irev[it.Seq], openChoiceItem(s.conv))}, nil
}

func (a *phoneAPI) queueAction(s *deskSession, op string, in writeBody) (map[string]any, *apiErr) {
	if in.Index == nil {
		return nil, badReq("index and text are required")
	}
	i := *in.Index
	if i < 0 || i >= len(s.queue) || s.queue[i] != in.Text {
		return nil, stale("the queue changed")
	}
	if op == "delete" {
		s.queue = append(s.queue[:i:i], s.queue[i+1:]...)
		s.editRemoved(i)
		a.d.syncQueue(s)
	} else {
		a.d.sendQueuedNow(s, i)
	}
	return map[string]any{"queue": a.queueJSON(s)}, nil
}

func (a *phoneAPI) newSession(in writeBody) (map[string]any, *apiErr) {
	d := a.d
	ok := false
	for _, f := range a.knownFolders() {
		ok = ok || f["path"] == in.Folder
	}
	if !ok {
		return nil, badReq("folder must be one of /phone/folders")
	}
	models, modes := modelChoices, modeChoices
	switch in.Backend {
	case "", "online":
	case "local":
		models, modes = d.localModelList(), localModes
	default:
		return nil, badReq("backend is online or local")
	}
	model, mode := in.Model, firstNonEmpty(in.Mode, "default")
	if model == "" {
		model = models[0]
	}
	if !slices.Contains(models, model) {
		return nil, badReq("unknown model " + model)
	}
	if !slices.Contains(modes, mode) {
		return nil, badReq("unknown mode " + mode)
	}
	ns := d.createSession(in.Folder, model, mode)
	_, group := a.sessionIDs()
	return map[string]any{"session": a.session(ns.id, firstNonEmpty(group[ns.id], "Open"))}, nil
}
