package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/katachinosekai/quill/internal/folders"
)

// Quill Android and the Quill operator (docs/design/quill-operator.md §4,
// §7 row G; user answers 2026-11-23: older Quill Android hides the operator
// until it updates; bottom sheets with chat, status and a Changes list with
// Undo). The operator is hidden from /phone/sessions and the
// /phone/session/{id}/... routes; these routes call the same handlers with
// its session, as the kmem assistant's do (phonekmemassist.go). Every write
// needs control mode; the phone server adds pairing, unlock, Idempotency-Key
// and the write limit.
//
// The phone acts only as the user's hands: it chats, answers the operator's
// plan cards and undoes a settings change. It can NOT change a setting: there
// is no "set" route; the operator changes settings itself with `quill
// settings set`, covered by the approved plan, and the phone shows and undoes
// the result.

// quillOpPhoneID: the id items answers carry for the operator.
const quillOpPhoneID = "quill-operator"

// quillOpPhoneAPI is the level in hello's "quillOperator" entry; a breaking
// change raises it.
const quillOpPhoneAPI = 1

// quillOpOps: the writes of /phone/quill/operator/{op}. No "set".
var quillOpOps = []string{"send", "answer", "interrupt", "queue/delete", "queue/send-now", "start", "stop", "restart"}

// phoneActions: the "actions" of /phone/sessions: the session menu, plus
// "quillOperator" while the operator can run here, so Quill Android knows
// it may show the Quill button (older versions ignore the entry and hide it).
func (d *desk) phoneActions() []string {
	out := slices.Clone(menuActions)
	if d.phoneQuillOperatorOn() {
		out = append(out, "quillOperator")
	}
	return out
}

// quillOpActions: what the phone may do with the operator in its current
// state (UI thread); the write routes refuse the others with 409 stale.
func (d *desk) quillOpActions(a *assistant) []string {
	s := a.s
	running := s != nil && s.running() && !s.stopping
	live := a.state == asLive || a.state == asStarting
	out := []string{}
	if (a.state == asClosed || a.state == asPanels) && !running && d.phoneQuillOperatorOn() {
		out = append(out, "start")
	}
	if live && s != nil && !s.running() || d.assistModelChanged(a) {
		out = append(out, "restart")
	}
	if live && running {
		out = append(out, "stop", "send")
	} else if live && s != nil {
		out = append(out, "send") // FAILED: a message starts it again, as on Quill Desktop
	}
	return out
}

// quillOpSummary: the operator as phones see it (UI thread). full adds the
// status line data, the actions, the model and the reason line; the stream
// change carries only state, waiting, epoch and rev.
func (a *phoneAPI) quillOpSummary(full bool) map[string]any {
	d := a.d
	as := d.quillAssist()
	s := as.s
	state := assistStateName(as)
	waiting := assistWaiting(s)
	out := map[string]any{"state": state, "waiting": waiting, "epoch": 0, "rev": 0}
	if s != nil {
		if t := a.tracks[s.id]; t != nil {
			out["epoch"], out["rev"] = t.epoch, t.rev
		}
	}
	if !full {
		return out
	}
	status := "off"
	switch {
	case !d.assistRunning(as):
	case waiting != "":
		status = "waiting"
	case d.assistWorking(as):
		status = "working"
	default:
		status = "idle"
	}
	sc := kmemScrubber()
	out["status"] = status
	out["dot"] = d.assistRunning(as)
	out["label"] = quillRole.label
	out["model"] = d.quillOpModel()
	out["actions"] = d.quillOpActions(as)
	out["unread"] = d.assistUnread(as)
	if state == "failed" && s != nil && s.failed != "" {
		out["reason"] = sc(oneLine(s.failed, 200))
	}
	if as.reason != "" && state != "live" && state != "saving" && state != "stopping" {
		out["reason"] = sc(as.reason)
		if as.button != "" {
			out["button"] = as.button
		}
	}
	return out
}

// quillNeeds: the operator's badge items as phones see them: count is what
// needs the user, unread what the Quill button's bubble shows.
func (a *phoneAPI) quillNeeds() map[string]any {
	d := a.d
	as := d.quillAssist()
	items := []map[string]any{}
	for _, it := range d.assistNeeds(as) {
		items = append(items, map[string]any{"key": it.Key, "title": kmemScrubber()(it.Sig)})
	}
	return map[string]any{"count": len(items), "unread": d.assistUnread(as), "items": items}
}

// apiSettingChange: one entry of the Changes list.
type apiSettingChange struct {
	N       int    `json:"n"`
	ID      string `json:"id"`
	Label   string `json:"label"`
	Old     string `json:"old"`
	New     string `json:"new"`
	TimeMs  int64  `json:"timeMs"`
	Why     string `json:"why,omitempty"`
	By      string `json:"by"`
	Undone  bool   `json:"undone"`
	CanUndo bool   `json:"canUndo"` // not undone, and the setting still has the value this change set
}

// quillChanges: the settings change log, newest first, at most
// maxSettingsChanges (UI thread).
func (a *phoneAPI) quillChanges() []apiSettingChange {
	d := a.d
	sc := kmemScrubber()
	out := []apiSettingChange{}
	list := d.settingsChanges()
	for i := len(list) - 1; i >= 0; i-- {
		c := list[i]
		can := false
		if !c.Undone {
			if s, ok := settingByID(c.ID); ok && !s.security && s.get(d) == c.New && d.settingUnavailable(s) == "" {
				can = true
			}
		}
		out = append(out, apiSettingChange{N: c.N, ID: c.ID, Label: c.Label, Old: sc(c.Old), New: sc(c.New), TimeMs: c.Time * 1000,
			Why: sc(c.Why), By: c.By, Undone: c.Undone, CanUndo: can})
	}
	if len(out) > maxSettingsChanges {
		out = out[:maxSettingsChanges]
	}
	return out
}

func (a *phoneAPI) lastChangeN() int {
	if list := a.d.settingsChanges(); len(list) > 0 {
		return list[len(list)-1].N
	}
	return 0
}

// quillChange: the "quill" stream change (UI thread), or nil when nothing a
// phone shows changed since the last one. It reads only Quill Desktop's
// state, no files: it runs on every 500 ms scan. The changes list itself is
// read with GET /phone/quill/settings/changes when "lastChange" moves.
func (a *phoneAPI) quillChange() map[string]any {
	as := a.d.quillAssist()
	body := map[string]any{"type": "quill", "needs": map[string]any{"count": len(a.d.assistNeeds(as)), "unread": a.d.assistUnread(as)},
		"operator": a.quillOpSummary(false), "lastChange": a.lastChangeN()}
	js, _ := json.Marshal(body)
	if string(js) == a.lastQuill {
		return nil
	}
	a.lastQuill = string(js)
	return body
}

// quillOpTrackID: the operator's session id while a tracker is kept for it.
func (a *phoneAPI) quillOpTrackID() string {
	if s := a.d.quillAssist().s; s != nil {
		return s.id
	}
	return ""
}

// quillRoute serves GET /phone/quill...
func (a *phoneAPI) quillRoute(w http.ResponseWriter, r *http.Request, path string) {
	if !a.quillOn(w) {
		return
	}
	switch path {
	case "quill":
		var body map[string]any
		a.ui(func() {
			op := a.quillOpSummary(true)
			body = map[string]any{"v": 1, "api": quillOpPhoneAPI, "state": op["state"], "status": op["status"], "waiting": op["waiting"],
				"needs": a.quillNeeds(), "actions": op["actions"], "operator": op, "changes": a.quillChanges()}
		})
		folders.WriteJSON(w, http.StatusOK, body)
	case "quill/operator/items":
		a.assistItems(w, r, quillOpPhoneID, "operator", func() *assistant { return a.d.quillAssist() },
			func() map[string]any { return a.quillOpSummary(true) })
	case "quill/settings/changes":
		var list []apiSettingChange
		a.ui(func() { list = a.quillChanges() })
		folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "changes": list})
	default:
		folders.WriteError(w, http.StatusNotFound, "not_found", "unknown path")
	}
}

// quillOn: the Quill operator exists on this Quill Desktop (it can run
// here); otherwise it wrote the 503.
func (a *phoneAPI) quillOn(w http.ResponseWriter) bool {
	on := false
	a.ui(func() { on = a.d.phoneQuillOperatorOn() })
	if !on {
		folders.WriteError(w, http.StatusServiceUnavailable, "quill_unavailable", "the Quill operator is off, or kmem memory is off or kmem is not installed on Quill Desktop")
	}
	return on
}

// quillWrite serves POST /phone/quill/...: the operator's ops and the undo.
// Nothing here changes a setting: "set" is not a route.
func (a *phoneAPI) quillWrite(w http.ResponseWriter, r *http.Request, path string) {
	switch op := strings.TrimPrefix(path, "quill/operator/"); {
	case op != path && slices.Contains(quillOpOps, op):
		if !a.quillOn(w) {
			return
		}
		a.assistWrite(w, r, op, quillOpOps, "operator", a.quillOpDo, func() map[string]any { return a.quillOpSummary(true) })
	case path == "quill/settings/undo":
		if !a.quillOn(w) {
			return
		}
		a.assistWrite(w, r, "undo", []string{"undo"}, "operator", a.quillUndo, func() map[string]any { return a.quillOpSummary(false) })
	default:
		folders.WriteError(w, http.StatusNotFound, "not_found", "unknown path")
	}
}

// quillUndo: POST /phone/quill/settings/undo {change}: the user's Undo
// button on a Changes entry (UI thread, control mode checked).
func (a *phoneAPI) quillUndo(_ string, in writeBody) (map[string]any, *apiErr) {
	if in.Change == nil || *in.Change <= 0 {
		return nil, badReq("change is the number of an entry of the Changes list")
	}
	d := a.d
	c, err := d.undoSettingChange(d.quillAssist().s, *in.Change)
	if err != nil {
		if strings.Contains(err.Error(), "no change ") {
			return nil, notFound(err.Error())
		}
		return nil, stale(kmemScrubber()(err.Error()))
	}
	return map[string]any{"undone": c.N, "changes": a.quillChanges()}, nil
}

// quillOpDo runs one operator write (UI thread).
func (a *phoneAPI) quillOpDo(op string, in writeBody) (map[string]any, *apiErr) {
	d := a.d
	as := d.quillAssist()
	can := d.quillOpActions(as)
	offered := func(action string) *apiErr {
		if !slices.Contains(can, action) {
			return stale("the Quill operator's state doesn't offer " + action + " now")
		}
		return nil
	}
	switch op {
	case "start":
		if fail := offered("start"); fail != nil {
			return nil, fail
		}
		as.userStopped = false
		d.assistProbe(as) // as the Quill button
		return nil, nil
	case "stop":
		if fail := offered("stop"); fail != nil {
			return nil, fail
		}
		as.userStopped = true
		d.assistSaveStop(as, false, nil) // the operator's end-of-session check
		return nil, nil
	case "restart":
		if fail := offered("restart"); fail != nil {
			return nil, fail
		}
		if d.assistModelChanged(as) {
			d.assistReboot(as)
		} else if !d.assistRestartNow(as) {
			return nil, stale("the Quill operator's state doesn't offer restart now")
		}
		return nil, nil
	}
	return a.assistSessionWrite(as, "Quill operator", op, in, offered)
}

// The phone operator workflow depends on a shared topic. The desktop
// operator can also run on its own.
func (d *desk) phoneQuillOperatorOn() bool { return d.kmemMemory() && d.quillOpOn() }
