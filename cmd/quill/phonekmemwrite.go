package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/katachinosekai/quill/internal/folders"
)

// kmem on Quill Android, phase 4 (docs/design/kmem-android.md §4.6, §5.2;
// kmem task tc6efac, user decisions fd77947 and f2b8d3a): task Done and
// Release, the task detail, the recycle bin list and Restore. Every write
// takes two steps: POST .../prepare checks the state, answers the exact kmem
// commands it will run and a confirm id; POST .../commit {confirm} runs them
// once. Confirm ids are single-use, live kmemConfirmTTL and are bound to the
// phone, the action, the target and the state seen at prepare (a changed
// state answers 409 stale). Every POST needs control mode; the phone server
// adds pairing, unlock, Idempotency-Key and the 10 writes/s limit. Each phone
// runs one kmem process at a time (409 kmem_busy, shared with the reads).
//
// Claim rules (kmem rule 19: a live session's claim is never taken; no
// --force anywhere):
//   - open task: Done, run as `kmem --as "Quill Android" task done`.
//   - claimed task whose claim is gone, leaseExpired or handoff (and not
//     blocked): Done and Release. kmem lets only the claim's owner release
//     or finish it, and lets the same name take over such a claim at once,
//     so Quill Desktop first runs `kmem --as <owner> task claim ID` (kmem
//     itself refuses a live or leased claim), then note + release or done
//     under that name. Claims by Quill Desktop's own ended sessions are gone
//     claims and follow the same rule.
//   - live or lease (liveness unknown, lease running): nothing.
//
// No route empties or purges the recycle bin (kmem rule 26: only the user,
// on the PC).

const (
	kmemPhoneName      = "Quill Android" // kmem name for phone writes that need no owner
	kmemResultMax      = 300             // runes of a Done result
	kmemConfirmPerPh   = 8
	kmemShowMax        = 32 << 10
	kmemVerifiedPrefix = "user: from Quill Android"
)

var kmemConfirmTTL = 60 * time.Second // tests shorten it

type kmemConfirm struct {
	phone, action, target string
	state                 string     // the task / bin item as prepare saw it
	cmds                  [][]string // kmem argument lists, run in order
	expires               time.Time
}

var (
	kmemTaskIDRe = regexp.MustCompile(`^t[a-z0-9]{2,16}$`)
	kmemOwnerRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._/@:+-]{0,79}$`)
	kmemConfIDRe = regexp.MustCompile(`^c[0-9a-f]{16}$`)
)

// kmemTaskActions: what the phone may do with a task (§4.2 actions); the
// write routes refuse everything else with 409 stale.
func kmemTaskActions(t kmemTask) []string {
	switch t.Status {
	case "open":
		return []string{"done"}
	case "claimed":
		if t.Owner == nil || !kmemOwnerRe.MatchString(*t.Owner) || strings.Contains(t.Line, "  [blocked by ") {
			return []string{} // kmem refuses to claim a blocked task
		}
		switch kmemClaim(t) {
		case "gone", "leaseExpired", "handoff":
			return []string{"done", "release"}
		}
	}
	return []string{}
}

// kmemTaskRefusal: why an action isn't offered for t (409 stale message).
func kmemTaskRefusal(t kmemTask, action string) string {
	switch {
	case t.Status == "done":
		return "task " + t.ID + " is already done"
	case t.Status == "open" && action == "release":
		return "task " + t.ID + " is not claimed"
	case t.Status == "claimed" && (kmemClaim(t) == "live" || kmemClaim(t) == "lease"):
		return "task " + t.ID + " is claimed by a running session; a live session's claim is never taken (kmem rule 19)"
	}
	return "task " + t.ID + " doesn't offer " + action + " now"
}

// kmemTaskState: the fingerprint commit compares with prepare's.
func kmemTaskState(t kmemTask) string {
	owner := ""
	if t.Owner != nil {
		owner = *t.Owner
	}
	return t.Status + "|" + owner + "|" + kmemClaim(t) + "|" + strconv.FormatBool(strings.Contains(t.Line, "  [blocked by "))
}

// kmemCleanResult: the Done result as kmem gets it: control characters
// become spaces, runs of spaces one; "" when nothing is left.
func kmemCleanResult(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// kmemTaskCmds: the kmem commands for action on t.
func kmemTaskCmds(t kmemTask, action, result, phoneName string) [][]string {
	verified := kmemVerifiedPrefix
	if phoneName != "" {
		verified += " (" + phoneName + ")"
	}
	if t.Status == "open" { // action is done
		return [][]string{{"--as", kmemPhoneName, "task", "done", t.ID, "--verified-by", verified, "--", result}}
	}
	as := *t.Owner
	cmds := [][]string{{"--as", as, "task", "claim", t.ID}}
	if action == "release" {
		return append(cmds,
			[]string{"--as", as, "task", "note", t.ID, "--", "released by the user from Quill Android (" + firstNonEmpty(phoneName, "phone") + ")"},
			[]string{"--as", as, "task", "release", t.ID})
	}
	return append(cmds, []string{"--as", as, "task", "done", t.ID, "--verified-by", verified, "--", result})
}

// kmemCmdLine shows one argument list as the command a user could type.
func kmemCmdLine(args []string) string {
	parts := []string{"kmem"}
	for _, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"'\\$`;&|<>()*?![]{}#~") {
			a = strconv.Quote(a)
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// kmemCmdOK: kmem's answer of a successful write (kmem prints its own
// notices around it).
var kmemCmdOK = map[string]string{"claim": "CLAIMED ", "note": "noted ", "release": "released ", "done": "done ", "restore": "restored "}

func kmemCmdVerb(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--as" {
			i++
			continue
		}
		if args[i] == "task" || args[i] == "trash" {
			return args[i+1]
		}
	}
	return ""
}

func kmemCmdSucceeded(args []string, out string) bool {
	want := kmemCmdOK[kmemCmdVerb(args)]
	for _, l := range strings.Split(out, "\n") {
		if want != "" && strings.HasPrefix(strings.TrimSpace(l), want) {
			return true
		}
	}
	return false
}

// --- the confirm store ---

func newConfirmID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "c" + hex.EncodeToString(b)
}

// putConfirm stores c and returns its id; expired ids are dropped and a
// phone keeps at most kmemConfirmPerPh open ids (the oldest goes).
func (k *phoneKmem) putConfirm(c *kmemConfirm, now time.Time) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.confirms == nil {
		k.confirms = map[string]*kmemConfirm{}
	}
	n, oldest := 0, ""
	for id, x := range k.confirms {
		if !now.Before(x.expires) {
			delete(k.confirms, id)
			continue
		}
		if x.phone == c.phone {
			n++
			if oldest == "" || x.expires.Before(k.confirms[oldest].expires) {
				oldest = id
			}
		}
	}
	if n >= kmemConfirmPerPh {
		delete(k.confirms, oldest)
	}
	id := newConfirmID()
	k.confirms[id] = c
	return id
}

// takeConfirm hands out a confirm id once. Another phone's id counts as
// unknown and stays; any other use by its phone spends it.
func (k *phoneKmem) takeConfirm(id, phone, action, target string, now time.Time) (*kmemConfirm, *apiErr) {
	k.mu.Lock()
	defer k.mu.Unlock()
	c := k.confirms[id]
	if c == nil || c.phone != phone {
		return nil, &apiErr{http.StatusConflict, "confirm_invalid", "unknown confirm id; prepare the action again"}
	}
	delete(k.confirms, id)
	if !now.Before(c.expires) {
		return nil, &apiErr{http.StatusConflict, "confirm_expired", "the confirmation expired; prepare the action again"}
	}
	if c.action != action || c.target != target {
		return nil, &apiErr{http.StatusConflict, "confirm_invalid", "this confirm id is for another action; prepare the action again"}
	}
	return c, nil
}

// --- routes ---

// kmemWrite serves POST /phone/kmem/{task|bin}/{id}/{action}/{prepare|commit}.
// Nothing else under /phone/kmem/ takes a POST (the assistant has its own
// routes): no route empties or purges the recycle bin.
func (a *phoneAPI) kmemWrite(w http.ResponseWriter, r *http.Request, path string) {
	p := strings.Split(strings.TrimPrefix(path, "kmem/"), "/")
	if len(p) != 4 || (p[3] != "prepare" && p[3] != "commit") ||
		!(p[0] == "task" && (p[2] == "done" || p[2] == "release") && kmemTaskIDRe.MatchString(p[1]) ||
			p[0] == "bin" && p[2] == "restore" && validBinID(p[1])) {
		folders.WriteError(w, http.StatusNotFound, "not_found", "unknown path")
		return
	}
	kind, id, action, step := p[0], p[1], p[2], p[3]
	var in struct {
		Result  string `json:"result"`
		Confirm string `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", "body must be a JSON object")
		return
	}
	phone := folders.PhoneID(r)
	var ctl, on bool
	phoneName := ""
	a.ui(func() {
		ctl, on = a.control(phone), a.d.kmemOn()
		for _, ph := range a.d.phones() {
			if ph.ID == phone {
				phoneName = oneLine(kmemCleanResult(ph.Name), 40)
			}
		}
	})
	if !ctl {
		folders.WriteError(w, http.StatusForbidden, "control_off", "Let paired devices control sessions is off")
		return
	}
	result := ""
	if step == "prepare" && action == "done" {
		result = kmemCleanResult(in.Result)
		switch {
		case result == "":
			folders.WriteError(w, http.StatusBadRequest, "bad_request", "Done needs a short result text")
			return
		case utf8.RuneCountInString(result) > kmemResultMax:
			folders.WriteError(w, http.StatusBadRequest, "bad_request", "the result is over 300 characters")
			return
		case result == "-h" || result == "--help":
			folders.WriteError(w, http.StatusBadRequest, "bad_request", "kmem would read this result as --help")
			return
		}
	}
	if step == "commit" && !kmemConfIDRe.MatchString(in.Confirm) {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", "commit needs the confirm id from prepare")
		return
	}
	if !on {
		folders.WriteError(w, http.StatusServiceUnavailable, "kmem_unavailable", "kmem memory is off or kmem is not installed on Quill Desktop")
		return
	}
	if !a.kmem.begin(phone) {
		folders.WriteError(w, http.StatusConflict, "kmem_busy", "a kmem call for this phone is already running")
		return
	}
	defer a.kmem.end(phone)
	sc := kmemScrubber()
	fail := func(e *apiErr) { folders.WriteError(w, e.status, e.code, sc(e.msg)) }

	var conf *kmemConfirm
	if step == "commit" {
		c, e := a.kmem.takeConfirm(in.Confirm, phone, action, kind+"/"+id, time.Now())
		if e != nil {
			fail(e)
			return
		}
		conf = c
	}
	// Read the current state (never the cache): prepare decides on it,
	// commit compares it with prepare's.
	var state, summary string
	var cmds [][]string
	if kind == "task" {
		t, e := a.kmemFindTask(id)
		if e != nil {
			fail(e)
			return
		}
		if !contains(kmemTaskActions(*t), action) {
			fail(stale(kmemTaskRefusal(*t, action)))
			return
		}
		title := oneLine(t.Title, 120)
		state = kmemTaskState(*t)
		if action == "done" {
			summary = "Mark task [" + t.ID + "] done: " + title + ". Result: " + result
		} else {
			summary = "Release task [" + t.ID + "]: " + title + " (claimed by " + *t.Owner + ", " + kmemClaim(*t) + ")"
		}
		if conf == nil {
			cmds = kmemTaskCmds(*t, action, result, phoneName)
		}
	} else {
		out, err := a.d.runKmem("kmem", "trash", "list")
		if err != nil {
			fail(&apiErr{http.StatusServiceUnavailable, "kmem_unavailable", "kmem didn't answer"})
			return
		}
		var it *binItem
		for _, x := range parseBin(out) {
			if x.ID == id {
				x := x
				it = &x
			}
		}
		if it == nil {
			fail(stale("[" + id + "] is not in the recycle bin"))
			return
		}
		state = it.Head
		summary = "Restore [" + id + "] (" + it.binMeta() + ") from the recycle bin: " + oneLine(it.Preview, 120)
		cmds = [][]string{append([]string{"--as", kmemPhoneName},
			binRestoreArgs(*it, "the user confirmed Restore for ["+id+"] on their phone (Quill for Android)")...)}
	}

	if conf == nil { // prepare
		c := &kmemConfirm{phone: phone, action: action, target: kind + "/" + id, state: state, cmds: cmds,
			expires: time.Now().Add(kmemConfirmTTL)}
		cid := a.kmem.putConfirm(c, time.Now())
		lines := []string{}
		for _, args := range cmds {
			lines = append(lines, sc(kmemCmdLine(args)))
		}
		folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "confirm": cid, "action": action, "id": id,
			"summary": sc(summary), "commands": lines, "expiresS": int((kmemConfirmTTL + time.Second - 1) / time.Second)})
		return
	}
	if conf.state != state {
		fail(stale("the " + kind + " changed since the confirmation; check it and prepare again"))
		return
	}
	var outs []string
	for i, args := range conf.cmds {
		out, err := a.d.runKmem("kmem", args...)
		if err != nil || !kmemCmdSucceeded(args, out) {
			msg := "kmem refused " + kmemCmdLine(args) + ": " + oneLine(firstNonEmpty(kmemOutText(out), firstNonEmpty(errText(err), "no answer")), 300)
			if i > 0 {
				msg += " (after: " + strings.Join(outs, "; ") + ")"
			}
			a.kmemWritten(kind)
			fail(&apiErr{http.StatusConflict, "kmem_refused", msg})
			return
		}
		outs = append(outs, oneLine(kmemOutText(out), 200))
	}
	a.kmemWritten(kind)
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "action": action, "id": id, "output": sc(strings.Join(outs, "\n"))})
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// kmemOutText: kmem's output without its notices.
func kmemOutText(out string) string {
	var keep []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if !kmemNoise(l) && strings.TrimSpace(l) != "" {
			keep = append(keep, strings.TrimSpace(l))
		}
	}
	return strings.Join(keep, " ")
}

// kmemFindTask reads the open and claimed tasks fresh and returns id's.
func (a *phoneAPI) kmemFindTask(id string) (*kmemTask, *apiErr) {
	var tr struct {
		Tasks []kmemTask `json:"tasks"`
	}
	if !a.d.kmemJSON(&tr, "task", "list", "--all") {
		return nil, &apiErr{http.StatusServiceUnavailable, "kmem_unavailable", "kmem didn't answer"}
	}
	for i := range tr.Tasks {
		if tr.Tasks[i].ID == id {
			return &tr.Tasks[i], nil
		}
	}
	return nil, notFound("no open task " + id + " (done tasks are listed with all=1; binned ones are in the recycle bin)")
}

// kmemWritten: after a phone write, the overview cache, the kmem button's
// bubble (and with it the "kmem" stream change), an open kmem window and
// the bin dialog follow.
func (a *phoneAPI) kmemWritten(kind string) {
	a.kmem.mu.Lock()
	a.kmem.ok, a.kmem.at = false, time.Time{}
	a.kmem.mu.Unlock()
	a.d.kmemCo.invalidate()
	a.d.do(func() {
		a.d.refreshKmemNeeds()
		if p := a.d.kmemPanelNow; p != nil {
			p.reload()
		}
		if b := a.d.kmemBinNow; b != nil && kind == "bin" {
			b.reload()
		}
		a.scan()
	})
}

// --- reads ---

var kmemBinMeta = regexp.MustCompile(`^(\w+), ([0-9.]+) days in bin, by (.*)$`)

// kmemBinBody: GET /phone/kmem/bin, from `kmem trash list`.
func kmemBinBody(items []binItem, sc func(string) string) map[string]any {
	out := []map[string]any{}
	size, truncated := 0, false
	for _, it := range items {
		kind, by, days := it.binMeta(), "", 0.0
		if m := kmemBinMeta.FindStringSubmatch(it.binMeta()); m != nil {
			kind, by = m[1], m[3]
			days, _ = strconv.ParseFloat(m[2], 64)
		}
		topic, title := "", it.Preview
		if strings.HasPrefix(title, "(") {
			if i := strings.Index(title, ") "); i > 0 {
				topic, title = title[1:i], title[i+2:]
			}
		}
		e := map[string]any{"id": it.ID, "kind": kind, "days": days, "by": sc(by), "reason": sc(oneLine(it.binReason(), 500)),
			"topic": sc(topic), "title": sc(oneLine(title, 300))}
		if size += len(it.Head) + len(it.Preview) + 80; size > kmemPhoneMax {
			truncated = true
			break
		}
		out = append(out, e)
	}
	return map[string]any{"v": 1, "items": out, "truncated": truncated}
}

func (a *phoneAPI) kmemBin(w http.ResponseWriter, phone string) {
	out, err, ok := a.kmemRead(w, phone, false, "trash", "list")
	if !ok {
		return
	}
	if err != nil {
		folders.WriteError(w, http.StatusServiceUnavailable, "kmem_unavailable", "kmem didn't answer")
		return
	}
	folders.WriteJSON(w, http.StatusOK, kmemBinBody(parseBin(out), kmemScrubber()))
}

// kmemTaskShow: GET /phone/kmem/task/{id} (`kmem show`, text).
func (a *phoneAPI) kmemTaskShow(w http.ResponseWriter, phone, id string) {
	if !kmemTaskIDRe.MatchString(id) {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", "not a task id")
		return
	}
	out, err, ok := a.kmemRead(w, phone, false, "show", id)
	if !ok {
		return
	}
	text := strings.TrimSpace(out)
	var keep []string
	for _, l := range strings.Split(text, "\n") {
		if !kmemNoise(l) {
			keep = append(keep, l)
		}
	}
	text = strings.TrimSpace(strings.Join(keep, "\n"))
	if err != nil {
		if strings.HasPrefix(text, "no fact or task ") || strings.Contains(text, "was purged") {
			folders.WriteError(w, http.StatusNotFound, "not_found", "no task "+id)
			return
		}
		folders.WriteError(w, http.StatusServiceUnavailable, "kmem_unavailable", "kmem didn't answer")
		return
	}
	text, truncated := capText(kmemScrubber()(text), kmemShowMax)
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "id": id, "text": text, "truncated": truncated})
}
