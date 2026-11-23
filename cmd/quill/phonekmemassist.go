package main

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/folders"
)

// kmem on Quill Android, phase 2 (docs/design/kmem-android.md §4.3, §5;
// kmem task tc6efac, user decisions fd77947): the kmem assistant's chat and
// its Start / Stop / Restart / Save now buttons and the save-timeout question
// on the phone. The assistant stays hidden from /phone/sessions and the
// /phone/session/{id}/... routes; these routes call the same handlers with
// the assistant's session (items, send, answer, interrupt, queue) and the kmem
// window's flows (kmemassist.go) for the buttons. Every write needs control
// mode; the phone server adds pairing, unlock, Idempotency-Key and the
// 10 writes/s limit. A plan approved here goes through answer like a
// session's plan card, so planscope.go's rules apply unchanged.

// kmemAssistPhoneID: the id items answers carry for the assistant (its
// Claude session id is not sent; a new session shows as a new epoch).
const kmemAssistPhoneID = "kmem-assistant"

// kmemAssistActions: what the phone may do with the assistant in its current
// state (UI thread); the write routes refuse the others with 409 stale.
// start, stop, restart and save are Quill Desktop's header buttons; send is
// the input; choose answers the save-timeout question.
func (d *desk) kmemAssistActions(a *kmemAssist) []string {
	s := a.s
	running := s != nil && s.running() && !s.stopping
	live := a.state == asLive || a.state == asStarting
	out := []string{}
	if (a.state == asClosed || a.state == asPanels) && !running {
		out = append(out, "start")
	}
	if live && s != nil && !s.running() || d.kmemAssistModelChanged(a) {
		out = append(out, "restart")
	}
	if live && running {
		out = append(out, "stop", "send")
		if s.kmemSave == nil && d.org.Kmem[s.id] != "" && d.kmemAvailable() {
			out = append(out, "save")
		}
	} else if live && s != nil {
		out = append(out, "send") // FAILED: a message starts it again, as on Quill Desktop
	}
	if a.state == asSaving && a.choice != nil {
		out = append(out, "choose")
	}
	return out
}

// kmemAssistQuestion: the save-timeout question while it is open, nil else.
func (d *desk) kmemAssistQuestion(a *kmemAssist) map[string]any {
	if a.state != asSaving || a.choice == nil {
		return nil
	}
	return map[string]any{"text": kmemScrubber()(d.kmemAssistAskText(a)), "choices": kmemAssistChoices}
}

// kmemAssistItems serves GET /phone/kmem/assistant/items: the same query and
// item JSON as /phone/session/{id}/items, plus the assistant's state.
func (a *phoneAPI) kmemAssistItems(w http.ResponseWriter, r *http.Request) {
	a.assistItems(w, r, kmemAssistPhoneID, "assistant", func() *assistant { return a.d.assist() },
		func() map[string]any { return a.kmemAssistSummary(true) })
}

// assistItems serves an assistant's items read (kmem and Quill operator
// share it): the session's items body plus the summary under key.
func (a *phoneAPI) assistItems(w http.ResponseWriter, r *http.Request, id, key string, as func() *assistant, summary func() map[string]any) {
	iq, ok := parseItemsQuery(w, r)
	if !ok {
		return
	}
	var body map[string]any
	a.ui(func() {
		if s := as().s; s != nil {
			body = a.itemsBody(s, id, iq)
		} else {
			body = map[string]any{"v": 1, "id": id, "epoch": 0, "rev": 0, "reset": iq.hasEpoch && iq.epoch != 0,
				"hasMore": false, "items": []map[string]any{}, "queue": []map[string]any{}}
		}
		body[key] = summary()
	})
	folders.WriteJSON(w, http.StatusOK, body)
}

// assistWaiting: what the assistant's session waits for: "plan", "question",
// "permission" or "" (a pending request; the plan wins, then a question).
func assistWaiting(s *deskSession) string {
	waiting := ""
	if s != nil && s.running() {
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
	return waiting
}

// assistStateName: the assistant's state as phones see it.
func assistStateName(as *assistant) string {
	switch as.state {
	case asProbing, asStarting:
		return "starting"
	case asPanels:
		return "panels"
	case asElsewhere:
		return "elsewhere"
	case asLive:
		if s := as.s; s == nil || !s.running() {
			return "failed"
		}
		return "live"
	case asSaving:
		return "saving"
	case asStopping:
		return "stopping"
	}
	return "off"
}

var kmemAssistOps = []string{"send", "answer", "interrupt", "queue/delete", "queue/send-now", "start", "stop", "restart", "save", "choose"}

// kmemAssistWrite serves POST /phone/kmem/assistant/{op}.
func (a *phoneAPI) kmemAssistWrite(w http.ResponseWriter, r *http.Request, op string) {
	a.assistWrite(w, r, op, kmemAssistOps, "assistant", a.kmemAssistDo, func() map[string]any { return a.kmemAssistSummary(true) })
}

// assistWrite serves one write of an assistant's route (kmem and Quill
// operator share it): control mode, the body, the op, then the summary under
// key in the answer.
func (a *phoneAPI) assistWrite(w http.ResponseWriter, r *http.Request, op string, ops []string, key string,
	do func(op string, in writeBody) (map[string]any, *apiErr), summary func() map[string]any) {
	if !slices.Contains(ops, op) {
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
		out, fail = do(op, in)
		if fail == nil {
			if out == nil {
				out = map[string]any{}
			}
			out[key] = summary()
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

// assistSessionWrite: the ops every assistant shares (send, answer,
// interrupt, queue) on its session; what is the name for the errors (UI thread).
func (a *phoneAPI) assistSessionWrite(as *assistant, what, op string, in writeBody, offered func(string) *apiErr) (map[string]any, *apiErr) {
	s := as.s
	if s == nil {
		return nil, &apiErr{http.StatusConflict, "not_running", "the " + what + " has not started"}
	}
	if op == "send" {
		if fail := offered("send"); fail != nil {
			return nil, fail
		}
	}
	action, sub := op, ""
	if op == "queue/delete" || op == "queue/send-now" {
		action, sub = "queue", op[len("queue/"):]
	}
	out, fail := a.sessionWrite(s, action, sub, in)
	if fail == nil && op == "send" {
		a.d.assistRefresh(as)
	}
	return out, fail
}

// kmemAssistDo runs one assistant write (UI thread).
func (a *phoneAPI) kmemAssistDo(op string, in writeBody) (map[string]any, *apiErr) {
	d := a.d
	as := d.assist()
	can := d.kmemAssistActions(as)
	offered := func(action string) *apiErr {
		if !slices.Contains(can, action) {
			return stale("the kmem assistant's state doesn't offer " + action + " now")
		}
		return nil
	}
	switch op {
	case "start":
		if !d.kmemOn() {
			return nil, &apiErr{http.StatusServiceUnavailable, "kmem_unavailable", "kmem memory is off or kmem is not installed on Quill Desktop"}
		}
		if fail := offered("start"); fail != nil {
			return nil, fail
		}
		as.userStopped = false
		d.kmemAssistProbe(as) // as the window's "Start assistant"
		return nil, nil
	case "stop":
		if fail := offered("stop"); fail != nil {
			return nil, fail
		}
		d.kmemAssistStopNow(as)
		return nil, nil
	case "restart":
		if fail := offered("restart"); fail != nil {
			return nil, fail
		}
		if d.kmemAssistModelChanged(as) {
			d.kmemAssistReboot(as)
		} else if !d.kmemAssistRestartNow(as) {
			return nil, stale("the kmem assistant's state doesn't offer restart now")
		}
		return nil, nil
	case "save":
		if fail := offered("save"); fail != nil {
			return nil, fail
		}
		d.saveHandoffNow(as.s)
		return nil, nil
	case "choose":
		if in.Choice == nil || *in.Choice < 0 || *in.Choice >= len(kmemAssistChoices) {
			return nil, badReq("choice is 0, 1 or 2")
		}
		// First answer wins: once Quill Desktop's dialog (or another phone)
		// answered, the question is gone and this one is stale.
		if fail := offered("choose"); fail != nil {
			return nil, stale("the save-timeout question was already answered")
		}
		d.kmemAssistChoose(as, *in.Choice)
		return nil, nil
	}
	return a.assistSessionWrite(as, "kmem assistant", op, in, offered)
}
