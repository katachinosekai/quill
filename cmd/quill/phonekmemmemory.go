package main

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/katachinosekai/quill/internal/folders"
)

// kmem on Quill Android, phase 3 (docs/design/kmem-android.md §3.5, §4.2;
// kmem task tc6efac, user decision fd77947 #8): the read-only Memory view.
// GET /phone/kmem/find?q=, /phone/kmem/get?topic= and /phone/kmem/fact?id=
// run `kmem find`, `kmem get` and `kmem show` like the kmem window's Memory
// tab; the topic list is phase 1's /phone/kmem/topics. kmem has no --json for
// these, so the text lines are parsed once here (kmem's fmt_fact format,
// unchanged since kmem 1.7.0); a line that can't be read is sent as
// {"text": …}. Same rules as phase 1: one kmem process at a time per phone
// (409 kmem_busy), 503 kmem_unavailable, scrubbed text; find at most once a
// second per phone (429 rate_limited with Retry-After). No route here writes.

const (
	kmemPhoneFactsMax = 200      // facts per find/get answer (§4.2)
	kmemPhoneFactMax  = 32 << 10 // bytes per fact text (§5.5)
	kmemFindQMin      = 2
	kmemFindQMax      = 200
)

var kmemFindEvery = time.Second // tests shorten it

// kmemFindLimit: when each phone last ran a find.
type kmemFindLimit struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// allow reports whether phone may find now and, if not, the seconds to wait.
func (l *kmemFindLimit) allow(phone string, now time.Time) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last == nil {
		l.last = map[string]time.Time{}
	}
	if wait := kmemFindEvery - now.Sub(l.last[phone]); wait > 0 {
		return false, int((wait + time.Second - 1) / time.Second)
	}
	l.last[phone] = now
	return true, 0
}

// kmemPhoneFact is one fact row of find and get.
type kmemPhoneFact struct {
	ID      string `json:"id,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Topic   string `json:"topic,omitempty"`
	Text    string `json:"text"`
	Why     string `json:"why,omitempty"`
	State   string `json:"state,omitempty"` // "" = current; superseded or retired (fact detail)
	Pinned  bool   `json:"pinned"`
	Expired bool   `json:"expired,omitempty"`
}

var (
	// "[f1a2b3c] kind: text…", find prefixes "topic: "; (?s): the text
	// may go on over the next lines
	kmemFactHead   = regexp.MustCompile(`(?s)^(?:(\S+): )?\[(f[0-9a-f]{6,})\] ([A-Za-z][\w-]*): ?(.*)$`)
	kmemFactExpiry = regexp.MustCompile(`  \(EXPIRED: valid to [^)]*\)$`)
	kmemFactState  = regexp.MustCompile(`  \((superseded|retired)\)$`)
	kmemTopicRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	kmemFactIDRe   = regexp.MustCompile(`^f[0-9a-f]{6,16}$`)
	// kmem get's sections list: "  T/sub  7 facts, 3.1 KB" (decimal KB,
	// unlike `kmem topics`, which parseTopics reads)
	kmemSectionLine = regexp.MustCompile(`^\s+(\S+)\s+(\d+) facts?, (\d+(?:\.\d+)?) KB$`)
)

// kmemSection: one row of a get's sections list.
type kmemSection struct {
	Name  string
	Facts int
	KB    float64
}

// splitFactTail takes kmem's suffixes off a fact's text (in fmt_fact's
// order: WHY, state, pinned, expired).
func splitFactTail(f *kmemPhoneFact, body string) {
	if loc := kmemFactExpiry.FindStringIndex(body); loc != nil {
		f.Expired, body = true, body[:loc[0]]
	}
	if strings.HasSuffix(body, "  (pinned)") {
		f.Pinned, body = true, strings.TrimSuffix(body, "  (pinned)")
	}
	if m := kmemFactState.FindStringSubmatchIndex(body); m != nil {
		f.State, body = body[m[2]:m[3]], body[:m[0]]
	}
	if i := strings.Index(body, "  WHY: "); i >= 0 {
		f.Why, body = body[i+len("  WHY: "):], body[:i]
	}
	f.Text = body
}

// kmemNoise: kmem's own notices (stderr, merged by runKmem), not output.
func kmemNoise(l string) bool {
	return strings.HasPrefix(l, "NOTICE ") || strings.HasPrefix(l, "  rules edition ")
}

// parseKmemFacts reads `kmem find` / `kmem get` output: fact lines (a line
// that is not a fact head continues the fact above it: multi-line texts),
// a get's "sections of T" list and its "no current facts"
// line. topic is the get's topic, "" for find.
func parseKmemFacts(out, topic string) (facts []kmemPhoneFact, sections []kmemSection, none bool) {
	var raws []string // fact heads with their continuation lines
	inSections := false
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		switch {
		case kmemNoise(l):
			continue
		case strings.HasPrefix(l, "sections of "):
			inSections = true
			continue
		case strings.HasPrefix(l, "no current facts under "), l == "no matches":
			none = true
			continue
		}
		if inSections {
			if m := kmemSectionLine.FindStringSubmatch(l); m != nil {
				n, _ := strconv.Atoi(m[2])
				kb, _ := strconv.ParseFloat(m[3], 64)
				sections = append(sections, kmemSection{m[1], n, kb})
				continue
			}
		}
		if kmemFactHead.MatchString(l) {
			raws = append(raws, l)
			inSections = false
			continue
		}
		if strings.HasPrefix(l, "(") && strings.Contains(l, "expired fact(s) hidden") { // kmem get's note
			continue
		}
		if len(raws) > 0 && !inSections {
			raws[len(raws)-1] += "\n" + l
			continue
		}
		if strings.TrimSpace(l) != "" {
			raws = append(raws, l) // unreadable: sent as {"text": …}
		}
	}
	for _, r := range raws {
		m := kmemFactHead.FindStringSubmatch(r)
		if m == nil {
			facts = append(facts, kmemPhoneFact{Text: strings.TrimSpace(r)})
			continue
		}
		f := kmemPhoneFact{ID: m[2], Kind: m[3], Topic: firstNonEmpty(m[1], topic)}
		splitFactTail(&f, m[4])
		facts = append(facts, f)
	}
	return facts, sections, none
}

// kmemFactsBody caps and scrubs facts: at most kmemPhoneFactsMax facts,
// kmemPhoneMax bytes in all, kmemPhoneFactMax per text.
func kmemFactsBody(facts []kmemPhoneFact, sc func(string) string) ([]kmemPhoneFact, bool) {
	out := []kmemPhoneFact{}
	size, truncated := 0, false
	for _, f := range facts {
		if len(out) == kmemPhoneFactsMax {
			truncated = true
			break
		}
		f.Topic, f.Why = sc(f.Topic), sc(f.Why)
		var cut bool
		if f.Text, cut = capText(sc(f.Text), kmemPhoneFactMax); cut {
			truncated = true
		}
		if f.Why, cut = capText(f.Why, kmemPhoneTitleMax*2); cut {
			truncated = true
		}
		if size += len(f.Text) + len(f.Why) + len(f.Topic) + len(f.ID) + len(f.Kind) + 80; size > kmemPhoneMax {
			truncated = true
			break
		}
		out = append(out, f)
	}
	return out, truncated
}

// kmemRead runs one kmem read for phone (kmem on, one at a time, find
// rate-limited); on a refusal it wrote the error and returns ok false.
func (a *phoneAPI) kmemRead(w http.ResponseWriter, phone string, find bool, args ...string) (out string, err error, ok bool) {
	var on bool
	a.ui(func() { on = a.d.kmemOn() })
	if !on {
		folders.WriteError(w, http.StatusServiceUnavailable, "kmem_unavailable", "kmem memory is off or kmem is not installed on Quill Desktop")
		return "", nil, false
	}
	if !a.kmem.begin(phone) {
		folders.WriteError(w, http.StatusConflict, "kmem_busy", "a kmem read for this phone is already running")
		return "", nil, false
	}
	defer a.kmem.end(phone)
	if find {
		if okRate, wait := a.kmem.find.allow(phone, time.Now()); !okRate {
			w.Header().Set("Retry-After", strconv.Itoa(wait))
			folders.WriteError(w, http.StatusTooManyRequests, "rate_limited", "at most one kmem search per second")
			return "", nil, false
		}
	}
	out, err = a.d.runKmem("kmem", args...)
	return out, err, true
}

// kmemFind: GET /phone/kmem/find?q= (plain words or a regex, as kmem does).
func (a *phoneAPI) kmemFind(w http.ResponseWriter, r *http.Request, phone string) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if n := utf8.RuneCountInString(q); n < kmemFindQMin || n > kmemFindQMax || strings.ContainsAny(q, "\n\r\x00") {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", "q must be 2 to 200 characters on one line")
		return
	}
	out, err, ok := a.kmemRead(w, phone, true, "find", "--", q)
	if !ok {
		return
	}
	sc := kmemScrubber()
	if err != nil {
		if t := strings.TrimSpace(out); strings.HasPrefix(t, "bad regex:") {
			folders.WriteError(w, http.StatusBadRequest, "bad_request", sc(oneLine(t, 300)))
			return
		}
		folders.WriteError(w, http.StatusServiceUnavailable, "kmem_unavailable", "kmem didn't answer")
		return
	}
	facts, _, _ := parseKmemFacts(out, "")
	list, truncated := kmemFactsBody(facts, sc)
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "facts": list, "truncated": truncated})
}

// kmemGet: GET /phone/kmem/get?topic= (the topic's current facts and, for a
// topic with sections, its sections list; 404 when it has neither).
func (a *phoneAPI) kmemGet(w http.ResponseWriter, r *http.Request, phone string) {
	topic := strings.TrimSpace(r.URL.Query().Get("topic"))
	if !kmemTopicRe.MatchString(topic) || strings.Contains(topic, "//") || strings.Contains(topic, "..") {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", "topic must be a kmem topic name (letters, digits, . _ - /)")
		return
	}
	out, err, ok := a.kmemRead(w, phone, false, "get", "--", topic)
	if !ok {
		return
	}
	if err != nil {
		folders.WriteError(w, http.StatusServiceUnavailable, "kmem_unavailable", "kmem didn't answer")
		return
	}
	sc := kmemScrubber()
	facts, sections, none := parseKmemFacts(out, topic)
	if none && len(facts) == 0 && len(sections) == 0 {
		folders.WriteError(w, http.StatusNotFound, "not_found", "no current facts under this topic")
		return
	}
	list, truncated := kmemFactsBody(facts, sc)
	secs := []map[string]any{}
	for _, s := range sections {
		secs = append(secs, map[string]any{"name": sc(s.Name), "facts": s.Facts, "kb": s.KB})
	}
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "topic": sc(topic), "facts": list, "sections": secs, "truncated": truncated})
}

// kmemPhoneFactDetail: `kmem show` of one fact.
type kmemPhoneFactDetail struct {
	kmemPhoneFact
	Binned       bool                `json:"binned"`
	Recorded     string              `json:"recorded"`
	By           string              `json:"by"`
	Source       string              `json:"source,omitempty"`
	ValidFrom    string              `json:"validFrom,omitempty"`
	ValidTo      string              `json:"validTo,omitempty"`
	Task         string              `json:"task,omitempty"`
	SupersededBy string              `json:"supersededBy,omitempty"`
	History      []map[string]string `json:"history"`
}

var (
	kmemShowMeta = regexp.MustCompile(`^  topic (\S+), recorded (\S+) \(event \d+\) by (.*)$`)
	kmemShowHist = regexp.MustCompile(`^  (\d{4}-\S+) (.*) \(([^()]*)\)$`)
)

// parseKmemShow reads `kmem show FACT` (fmt_fact long): head line(s), the
// recycle bin line, "topic …, recorded … by …[, source S][, valid A .. B]
// [, from task T]", cites and one history line per change. ok is false when
// the output is not a fact.
func parseKmemShow(out string) (kmemPhoneFactDetail, bool) {
	var d kmemPhoneFactDetail
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if !kmemNoise(l) {
			lines = append(lines, l)
		}
	}
	meta := -1
	for i, l := range lines {
		if kmemShowMeta.MatchString(l) {
			meta = i
			break
		}
	}
	if meta < 1 {
		return d, false
	}
	head := lines[:meta]
	if n := len(head); strings.HasPrefix(head[n-1], "  IN THE RECYCLE BIN") {
		d.Binned, head = true, head[:n-1]
	}
	m := kmemFactHead.FindStringSubmatch(strings.Join(head, "\n"))
	if m == nil || m[1] != "" {
		return d, false
	}
	d.ID, d.Kind = m[2], m[3]
	splitFactTail(&d.kmemPhoneFact, m[4])
	mm := kmemShowMeta.FindStringSubmatch(lines[meta])
	d.Topic, d.Recorded = mm[1], mm[2]
	parts := strings.Split(mm[3], ", ")
	d.By = parts[0]
	for _, p := range parts[1:] {
		switch {
		case strings.HasPrefix(p, "source "):
			d.Source = strings.TrimPrefix(p, "source ")
		case strings.HasPrefix(p, "valid "):
			if a, b, ok := strings.Cut(strings.TrimPrefix(p, "valid "), " .. "); ok {
				if a != "?" {
					d.ValidFrom = a
				}
				if b != "now" {
					d.ValidTo = b
				}
			}
		case strings.HasPrefix(p, "from task "):
			d.Task = strings.TrimPrefix(p, "from task ")
		}
	}
	d.History = []map[string]string{}
	for _, l := range lines[meta+1:] {
		h := kmemShowHist.FindStringSubmatch(l)
		if h == nil {
			continue // cites
		}
		d.History = append(d.History, map[string]string{"at": h[1], "what": h[2], "by": h[3]})
		if by, ok := strings.CutPrefix(h[2], "superseded by "); ok {
			d.SupersededBy = by
		}
	}
	return d, true
}

// kmemFact: GET /phone/kmem/fact?id= (`kmem show`; facts only, tasks have
// /phone/kmem/task/{id} in a later phase).
func (a *phoneAPI) kmemFact(w http.ResponseWriter, r *http.Request, phone string) {
	id := r.URL.Query().Get("id")
	if !kmemFactIDRe.MatchString(id) {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", "id must be a kmem fact id (f + hex)")
		return
	}
	out, err, ok := a.kmemRead(w, phone, false, "show", "--", id)
	if !ok {
		return
	}
	t := strings.TrimSpace(out)
	if err != nil {
		if strings.Contains(t, "no fact or task") || strings.Contains(t, "was purged") {
			folders.WriteError(w, http.StatusNotFound, "not_found", "no such kmem fact")
			return
		}
		folders.WriteError(w, http.StatusServiceUnavailable, "kmem_unavailable", "kmem didn't answer")
		return
	}
	d, parsed := parseKmemShow(out)
	if !parsed {
		folders.WriteError(w, http.StatusNotFound, "not_found", "no such kmem fact")
		return
	}
	sc := kmemScrubber()
	d.Text, _ = capText(sc(d.Text), kmemPhoneFactMax)
	d.Why, _ = capText(sc(d.Why), kmemPhoneFactMax)
	d.Topic, d.By, d.Source = sc(d.Topic), sc(d.By), sc(d.Source)
	for _, h := range d.History {
		h["what"], _ = capText(sc(h["what"]), kmemPhoneTitleMax)
		h["by"] = sc(h["by"])
	}
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "fact": d})
}
