package main

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/folders"
)

// Feature questions. "Updates keep settings; only new, changed or removed
// features that need the user's input ask, once each" (user 2026-11-22).
//
//   - featureQuestions is the registry, oldest first. Every unanswered,
//     unretired entry is asked in ONE dialog after the Welcome (also after
//     skipped versions), recommended choices preselected. Answering records
//     "<id>Asked"; "Later" applies nothing and asks again next start.
//   - New users answer the "New" entries in the Welcome; the Welcome marks
//     every entry answered, so they are never asked again.
//   - "Changed"/"Removed" entries say what changes and what happens to the
//     current setting; changes that need no input aren't registered and carry
//     over silently. "Notice" entries (e.g. security changes) only have OK.
//   - Changed defaults: a pref the user set carries "<pref>Set" (setUserPref);
//     prefBool reads the code's default for prefs the user never set.
//   - Downgrades: unknown "<id>Asked" prefs are never cleared.
//   - Never asked while the window is hidden (tray start), during a restore
//     (hot reload, restart into a new build), over another dialog, or while
//     the user typed in the last few seconds (featureAskReady).
//   - Quill Android answers them too: GET /phone/settings/questions, POST
//     /phone/settings/answer (docs/phone-api.md).
//   - Info-only "What's new" is the update line's, never this dialog's.

type featureKind string

const (
	featureNew     featureKind = "New"
	featureChanged featureKind = "Changed"
	featureRemoved featureKind = "Removed"
	featureNotice  featureKind = "Notice"
)

type featureChoice struct {
	Label string
	Value any // written to the entry's Pref: bool or string; nil leaves it as it is
}

type featureQuestion struct {
	ID          string // "<ID>Asked" records the answer
	Kind        featureKind
	Title       string
	Question    string
	Explain     string // changed/removed: what changes or goes away, and what happens to the current setting
	Note        string // optional short second line
	Choices     []featureChoice
	Recommended int // index into Choices, preselected
	Pref        string
	Since       string // the release that added the entry
	Retired     bool   // superseded or removed: never asked again
}

func (q featureQuestion) askedPref() string { return q.ID + "Asked" }

const (
	prefCoordinatorAsked = "coordinatorAsked"
	coordinatorTitle     = "Coordinate with subagents"
	coordinatorExplain   = "Claude splits your requests into tasks and runs each in its own background subagent while it stays free to talk with you. Each task costs about the same tokens as running it in a session of its own, plus a little for briefing and review. Because tasks run side by side, more work gets done per hour, and token use grows at the same rate as the work, not faster."
	coordinatorOnline    = "Online sessions only (Claude models); local models work alone."
	welcomeCoordinate    = "Coordinate with subagents (recommended)"
	welcomeOneSession    = "One session does everything"
)

// The explanation texts are user-approved: keep them word for word.
var featureQuestions = []featureQuestion{{
	ID: "coordinator", Kind: featureNew, Title: coordinatorTitle,
	Question: "How should Claude work?", Explain: coordinatorExplain, Note: coordinatorOnline,
	Choices: []featureChoice{{welcomeCoordinate, true}, {welcomeOneSession, false}},
	Pref:    prefCoordinator, Since: "v0.1.25",
}}

// prefDefaults: the current default of each tracked bool pref.
var prefDefaults = map[string]bool{prefCoordinator: true}

// setUserPref writes a pref the user chose and marks it as theirs.
func setUserPref(p fyne.Preferences, pref string, v any) {
	switch v := v.(type) {
	case bool:
		p.SetBool(pref, v)
	case string:
		p.SetString(pref, v)
	default:
		return
	}
	p.SetBool(pref+"Set", true)
}

// prefBool: the user's value when they set it, else today's default. A value
// stored before the markers existed counts as the user's (updates keep it).
func prefBool(p fyne.Preferences, pref string) bool {
	def := prefDefaults[pref]
	if p.Bool(pref+"Set") || p.BoolWithFallback(pref, true) == p.BoolWithFallback(pref, false) {
		return p.BoolWithFallback(pref, def)
	}
	return def
}

func (d *desk) dueFeatureQuestions() []featureQuestion {
	p := d.app.Preferences()
	if !p.Bool(prefWelcomeSeen) {
		return nil
	}
	var due []featureQuestion
	for _, q := range featureQuestions {
		if !q.Retired && !p.Bool(q.askedPref()) {
			due = append(due, q)
		}
	}
	return due
}

// featureQuestionForm: the entry's widgets under its title; r is nil for a notice.
func featureQuestionForm(q featureQuestion) (r *widget.RadioGroup, objs []fyne.CanvasObject) {
	if q.Question != "" {
		objs = append(objs, widget.NewLabel(q.Question))
	}
	if len(q.Choices) > 0 {
		var labels []string
		for _, c := range q.Choices {
			labels = append(labels, c.Label)
		}
		r = widget.NewRadioGroup(labels, nil)
		r.Required = true
		r.SetSelected(labels[q.Recommended])
		objs = append(objs, r)
	}
	objs = append(objs, caption(q.Explain))
	if q.Note != "" {
		objs = append(objs, caption(q.Note))
	}
	return r, objs
}

// answerFeatureQuestion writes choice i (ignored for notices) and records
// the answer; false when the entry is unknown, retired or i is out of range.
func (d *desk) answerFeatureQuestion(id string, i int) bool {
	p := d.app.Preferences()
	for _, q := range featureQuestions {
		if q.ID != id || q.Retired {
			continue
		}
		if len(q.Choices) > 0 {
			if i < 0 || i >= len(q.Choices) {
				return false
			}
			setUserPref(p, q.Pref, q.Choices[i].Value)
		}
		p.SetBool(q.askedPref(), true)
		return true
	}
	return false
}

func (d *desk) markFeatureQuestionsAnswered() {
	for _, q := range featureQuestions {
		d.app.Preferences().SetBool(q.askedPref(), true)
	}
}

var featureIdle = 5 * time.Second // no typing for this long before asking

func (d *desk) featureAskReady() bool {
	return !d.hidden && d.restore == nil && time.Since(d.lastTyped) >= featureIdle && d.win.Canvas().Overlays().Top() == nil
}

// askFeatureQuestionsWhenReady waits off the UI thread until featureAskReady.
func (d *desk) askFeatureQuestionsWhenReady() {
	go func() {
		for {
			ready, due := false, false
			d.doWait(func() { due = len(d.dueFeatureQuestions()) > 0; ready = due && d.featureAskReady() })
			if !due {
				return
			}
			if ready {
				d.do(d.askFeatureQuestions)
				return
			}
			time.Sleep(2 * time.Second)
		}
	}()
}

// askFeatureQuestions shows every due entry in one dialog.
func (d *desk) askFeatureQuestions() {
	due := d.dueFeatureQuestions()
	if len(due) == 0 || d.featureDlg != nil {
		return
	}
	body := container.NewVBox(caption("Quill Desktop kept all your settings. These need your choice; each is asked only once."))
	radios := make([]*widget.RadioGroup, len(due))
	notices := true
	for i, q := range due {
		var objs []fyne.CanvasObject
		radios[i], objs = featureQuestionForm(q)
		notices = notices && radios[i] == nil
		body.Add(widget.NewSeparator())
		body.Add(boldLabel(string(q.Kind) + ": " + q.Title))
		for _, o := range objs {
			body.Add(o)
		}
	}
	answer := func() {
		for i, q := range due {
			if d.app.Preferences().Bool(q.askedPref()) {
				continue // answered on the phone meanwhile
			}
			sel := 0
			if r := radios[i]; r != nil {
				sel = indexOf(r.Options, r.Selected)
			}
			d.answerFeatureQuestion(q.ID, sel)
		}
		d.settingsChanged()
	}
	title := "Quill Desktop: your choice"
	if len(due) == 1 {
		title = due[0].Title
	}
	scroll := container.NewVScroll(body)
	if notices {
		body.Add(caption(""))
		dlg := dialog.NewCustom(title, "OK", scroll, d.win)
		dlg.SetOnClosed(func() {
			if d.featureDlg == dlg { // not closed for a phone answer
				d.featureDlg = nil
				answer()
			}
		})
		d.featureDlg = dlg
	} else {
		body.Add(caption("You can change these any time in Settings."))
		d.featureDlg = dialog.NewCustomConfirm(title, "Save", "Later", scroll, func(ok bool) {
			d.featureDlg = nil
			if ok {
				answer()
			}
		}, d.win)
	}
	d.featureDlg.Resize(fyne.NewSize(640, 440))
	d.featureDlg.Show()
}

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

// Phone API: GET /phone/settings/questions, POST /phone/settings/answer.

func (a *phoneAPI) featureQuestionsRoute(w http.ResponseWriter) {
	var out []map[string]any
	a.ui(func() {
		for _, q := range a.d.dueFeatureQuestions() {
			choices := []string{}
			for _, c := range q.Choices {
				choices = append(choices, c.Label)
			}
			text := q.Explain
			if q.Note != "" {
				text += "\n" + q.Note
			}
			out = append(out, map[string]any{"id": q.ID, "kind": string(q.Kind), "title": q.Title, "question": q.Question,
				"text": text, "choices": choices, "recommended": q.Recommended})
		}
	})
	if out == nil {
		out = []map[string]any{}
	}
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "questions": out})
}

func (a *phoneAPI) featureAnswer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID     string `json:"id"`
		Choice *int   `json:"choice"`
	}
	body, _ := io.ReadAll(r.Body)
	if json.Unmarshal(body, &in) != nil || in.ID == "" {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", `body must be {"id": "...", "choice": <index>}`)
		return
	}
	phone := folders.PhoneID(r)
	var fail *apiErr
	a.ui(func() {
		if !a.control(phone) {
			fail = &apiErr{http.StatusForbidden, "control_off", "Let paired devices control sessions is off"}
			return
		}
		due := false
		for _, q := range a.d.dueFeatureQuestions() {
			due = due || q.ID == in.ID && (len(q.Choices) == 0 || in.Choice != nil)
		}
		c := 0
		if in.Choice != nil {
			c = *in.Choice
		}
		if !due || !a.d.answerFeatureQuestion(in.ID, c) {
			fail = &apiErr{http.StatusNotFound, "not_found", "no such open question or choice"}
			return
		}
		a.d.settingsChanged()
		if dl := a.d.featureDlg; dl != nil { // answered here: the desktop dialog shows what is left
			a.d.featureDlg = nil
			dl.Hide()
			a.d.askFeatureQuestions()
		}
	})
	if fail != nil {
		folders.WriteError(w, fail.status, fail.code, fail.msg)
		return
	}
	a.featureQuestionsRoute(w)
}
