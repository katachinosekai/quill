package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Topic tabs (docs/design/topic-tabs.md §3.1-§3.2, user 2026-11-22): a tab
// is the coordinator of one top-level kmem topic. Its kmem name (`--as`) and
// title follow the topic; opening a topic always starts a fresh session
// (no resume, no handoff) and a topic has at most one live session.
//
// kmem 1.8.1 has no topic labels (K1), no topic briefing (K2) and no live
// session names (K6). Until they ship Quill reads an optional "scope" and
// "title" from kmem's config labels (sessions[TOPIC]), falls back to the
// topic itself as its scope, and briefs with today's `kmem context TASK
// --topic SCOPE` ranking (see topicPrompt).

// topicBriefingFeature: the `kmem version --json` feature of the topic
// briefing `kmem context --topic T` (K2, kmem 1.10.0). Without it --topic only
// ranks, so older kmem gets the ranking fallback.
const topicBriefingFeature = "topic-context"

type topicInfo struct {
	Slug    string
	Title   string
	Scope   []string // top-level kmem topics the briefing draws from
	Labeled bool     // has a kmem label with a scope (shown first)
	Status  string   // first line of the current TOPIC/status fact
	Claimed int
	Open    int
}

var topicCache struct {
	mu     sync.Mutex
	at     time.Time
	loaded bool
	busy   bool
	list   []topicInfo
	extra  map[string]string // topics named in "New topic" this run: slug -> title
	saved  map[string]string // titles persisted in Quill Desktop's org file: slug -> title
}

func resetTopicCache() {
	topicCache.mu.Lock()
	topicCache.at, topicCache.loaded, topicCache.busy, topicCache.list, topicCache.extra, topicCache.saved = time.Time{}, false, false, nil, nil, nil
	topicCache.mu.Unlock()
}

// topicTitle: a topic without a label is shown as its slug with a capital.
func topicTitle(slug string) string {
	r := []rune(slug)
	if len(r) == 0 {
		return ""
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// statusLine: the text of the newest fact `kmem get T/status` prints
// ("[f1234ab] fact: text"), "" when there is none. kmem prints several
// current facts oldest first (by recorded time) and uses the last one, so the
// last fact line wins.
func statusLine(out string) string {
	var facts []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "[") {
			facts = append(facts, l)
		}
	}
	if len(facts) > 0 {
		l := facts[len(facts)-1]
		if i := strings.Index(l, "] "); i > 0 {
			l = strings.TrimSpace(l[i+2:])
		}
		if i := strings.Index(l, ": "); i > 0 && !strings.Contains(l[:i], " ") {
			l = strings.TrimSpace(l[i+2:])
		}
		return l
	}
	return ""
}

// countTasks sets each topic's claimed/open counts from the tasks whose
// top-level topic is in its scope.
func countTasks(list []topicInfo, tasks []kmemTask) {
	for i := range list {
		t := &list[i]
		t.Claimed, t.Open = 0, 0
		for _, task := range tasks {
			if !contains(t.Scope, topLevel(task.Topic)) {
				continue
			}
			switch task.Status {
			case "claimed":
				t.Claimed++
			case "open":
				t.Open++
			}
		}
	}
}

// buildTopics assembles the topic list from `kmem topics`, the config labels
// and the task list. The kmem topic belongs to the kmem window, never a tab.
func buildTopics(topicsOut string, labels map[string]kmemSessionCfg, tasks []kmemTask, status map[string]string) []topicInfo {
	var out []topicInfo
	seen := map[string]bool{"kmem": true}
	var keys []string
	for k, c := range labels {
		if len(c.Scope) > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		c := labels[k]
		out = append(out, topicInfo{Slug: k, Title: firstNonEmpty(c.Title, firstNonEmpty(c.As, topicTitle(k))), Scope: c.Scope, Labeled: true})
	}
	less := sortLess()
	sort.SliceStable(out, func(i, j int) bool { return less(out[i].Title, out[j].Title) })
	var rest []string
	for _, t := range parseTopics(topicsOut) {
		top := topLevel(t.Name)
		if !seen[top] {
			seen[top] = true
			rest = append(rest, top)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		title := topicTitle(k)
		if c, ok := labels[k]; ok {
			title = firstNonEmpty(c.Title, firstNonEmpty(c.As, title))
		}
		out = append(out, topicInfo{Slug: k, Title: title, Scope: []string{k}})
	}
	for i := range out {
		out[i].Status = status[out[i].Slug]
	}
	countTasks(out, tasks)
	return out
}

// loadTopics reads the topics from kmem (not on the UI thread) and caches
// them for isTopic.
func (d *desk) loadTopics() []topicInfo {
	topicsOut, _ := d.runKmem("kmem", "topics")
	var ts struct {
		Tasks []kmemTask `json:"tasks"`
	}
	d.kmemJSON(&ts, "task", "list")
	labels := kmemSessions()
	status := map[string]string{}
	for _, t := range parseTopics(topicsOut) {
		if strings.HasSuffix(t.Name, "/status") && !strings.Contains(strings.TrimSuffix(t.Name, "/status"), "/") {
			slug := strings.TrimSuffix(t.Name, "/status")
			if out, err := d.runKmem("kmem", "get", t.Name); err == nil {
				status[slug] = statusLine(out)
			}
		}
	}
	list := buildTopics(topicsOut, labels, ts.Tasks, status)
	if d.applyKmemScopes(list) {
		countTasks(list, ts.Tasks) // kmem's scope, as in its briefing
	}
	applySavedTitles(list, labels)
	topicCache.mu.Lock()
	topicCache.list, topicCache.at, topicCache.loaded, topicCache.busy = list, time.Now(), true, false
	topicCache.mu.Unlock()
	return list
}

// topicScopeFeature: the `kmem version --json` feature of `kmem scope T` and
// of the scope in `kmem context --topic T` (kmem 1.10.1). With it kmem's
// stored scope replaces the label's scope from Quill's config; Quill no
// longer passes a scope list to kmem.
const topicScopeFeature = "topic-scope"

// applyKmemScopes reads each topic's stored scope (`kmem scope T --json`)
// for display and the task counts. A topic with no stored scope keeps what
// it had. Reports whether kmem's scopes were used. Not on the UI thread.
func (d *desk) applyKmemScopes(list []topicInfo) bool {
	if !d.kmemHasFeature(topicScopeFeature) {
		return false
	}
	for i := range list {
		t := &list[i]
		// kmem 1.10.1: {"scope": {"topic": T, "topics": [T, ...]}, "api": 1};
		// "topics" starts with T and is [T] with no stored scope.
		var r struct {
			Scope struct {
				Topics []string `json:"topics"`
			} `json:"scope"`
		}
		if d.kmemJSON(&r, "scope", t.Slug) && len(r.Scope.Topics) > 0 {
			if len(r.Scope.Topics) > 1 {
				t.Labeled = true
			}
			t.Scope = r.Scope.Topics
		}
	}
	return true
}

// refreshTopics reloads the cache in the background when it is older than a
// minute, then rebuilds the sidebar (topic grouping, merge notices).
func (d *desk) refreshTopics() {
	topicCache.mu.Lock()
	stale := !topicCache.busy && (!topicCache.loaded || time.Since(topicCache.at) > time.Minute)
	if stale {
		topicCache.busy = true
	}
	topicCache.mu.Unlock()
	if !stale {
		return
	}
	go func() {
		d.loadTopics()
		d.do(d.rebuildRows)
	}()
}

// cachedTopic: the topic with this slug from the last load (or named in
// "New topic" this run).
func cachedTopic(slug string) (topicInfo, bool) {
	topicCache.mu.Lock()
	defer topicCache.mu.Unlock()
	for _, t := range topicCache.list {
		if t.Slug == slug {
			return t, true
		}
	}
	if title, ok := topicCache.extra[slug]; ok {
		return topicInfo{Slug: slug, Title: title, Scope: []string{slug}}, true
	}
	if title, ok := topicCache.saved[slug]; ok {
		return topicInfo{Slug: slug, Title: title, Scope: []string{slug}}, true
	}
	return topicInfo{}, false
}

// seedSavedTitles / applySavedTitles: the titles typed in "New topic" and
// persisted in the org file. kmem's config title or label still wins.
func seedSavedTopicTitles(m map[string]string) {
	topicCache.mu.Lock()
	topicCache.saved = map[string]string{}
	for k, v := range m {
		topicCache.saved[k] = v
	}
	topicCache.mu.Unlock()
}

func applySavedTitles(list []topicInfo, labels map[string]kmemSessionCfg) {
	topicCache.mu.Lock()
	defer topicCache.mu.Unlock()
	for i := range list {
		c := labels[list[i].Slug]
		if title, ok := topicCache.saved[list[i].Slug]; ok && c.Title == "" && c.As == "" {
			list[i].Title = title
		}
	}
}

// titleKey: a title compared for "same topic": trimmed, NFC, case-folded,
// inner spaces collapsed.
func titleKey(s string) string {
	s = norm.NFC.String(strings.Join(strings.Fields(s), " "))
	return strings.ToLower(strings.ToUpper(s))
}

// topicSlugByTitle: the slug of a known topic (loaded list, persisted titles,
// this run's) whose title equals title, "" if none.
func topicSlugByTitle(title string) string {
	key := titleKey(title)
	topicCache.mu.Lock()
	defer topicCache.mu.Unlock()
	for _, t := range topicCache.list {
		if titleKey(t.Title) == key {
			return t.Slug
		}
	}
	var hit []string
	for _, m := range []map[string]string{topicCache.saved, topicCache.extra} {
		for slug, t := range m {
			if titleKey(t) == key {
				hit = append(hit, slug)
			}
		}
	}
	sort.Strings(hit) // the first-created topic-N
	if len(hit) > 0 {
		return hit[0]
	}
	return ""
}

// promptTitle: a title made safe to sit inside a prompt or a quoted shell-ish
// command: no quotes, backticks, $, backslashes or control characters;
// spaces collapsed. The displayed title stays as typed.
func promptTitle(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '"' || r == '`' || r == '$' || r == '\\' || r == '\'':
			return ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r):
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// topicSlugFor is the slug "New topic" gives a typed title, for the card's
// preview, pickNewTopic and the phone API alike. A title with no Latin letter
// or digit (e.g. Japanese, "!!!") has nothing to slugify: it gets "topic-N",
// the first N no known topic (loaded or named this run) uses.
func topicSlugFor(title string) string {
	if strings.Trim(slugJunk.ReplaceAllString(strings.ToLower(title), "-"), "-") != "" {
		return kmemSlug(title, func(string) bool { return false })
	}
	if slug := topicSlugByTitle(title); slug != "" {
		return slug // the same title reopens its topic
	}
	for n := 1; ; n++ {
		slug := fmt.Sprintf("topic-%d", n)
		if _, ok := cachedTopic(slug); !ok {
			return slug
		}
	}
}

func topicsLoaded() bool {
	topicCache.mu.Lock()
	defer topicCache.mu.Unlock()
	return topicCache.loaded
}

func rememberNewTopic(t topicInfo) {
	topicCache.mu.Lock()
	if topicCache.extra == nil {
		topicCache.extra = map[string]string{}
	}
	topicCache.extra[t.Slug] = t.Title
	topicCache.mu.Unlock()
}

// topicOf: the topic a session coordinates ("" for an old-name or scratch
// session).
func (d *desk) topicOf(id string) string {
	k := d.org.Kmem[id]
	if k == "" {
		return ""
	}
	if _, ok := cachedTopic(k); ok {
		return k
	}
	return ""
}

// topicOwner: the live session of a topic (a tab, a running session or the
// kmem window), nil if none. External terminal sessions need K6.
func (d *desk) topicOwner(slug string, self *deskSession) *deskSession {
	return d.handoffOwner(slug, self)
}

// topicPrompt: the first prompt of a fresh topic session (replaces the
// handoff resume prompt). Not on the UI thread (asks kmem's version).
func (d *desk) topicPrompt(t topicInfo, isNew bool) string {
	return topicPromptFor(t, d.kmemHasFeature(topicBriefingFeature), isNew)
}

func topicPromptFor(t topicInfo, briefing bool, isNew bool) string {
	scope := strings.Join(firstNonEmptyList(t.Scope, []string{t.Slug}), ",")
	var load, sub string
	if briefing {
		load = fmt.Sprintf("Run `kmem brief` and `kmem context --topic %s`", t.Slug)
		sub = fmt.Sprintf("`kmem context \"<task>\" --topic %s`", t.Slug)
	} else {
		// kmem before the topic briefing: --topic only ranks, so ask for the
		// claimed and open tasks explicitly and name the whole scope.
		load = fmt.Sprintf("Run `kmem brief`, `kmem task list` and `kmem context \"%s coordinator: claimed tasks, open tasks, status, next steps\" --topic %s`", promptTitle(t.Title), scope)
		sub = fmt.Sprintf("`kmem context \"<task>\" --topic %s`", scope)
	}
	p := fmt.Sprintf("You are the %s coordinator; kmem name %q (`kmem --as %s`). %s; "+
		"continue the claimed tasks from their checkpoints and take the open ones as next steps; "+
		"keep `%s/status` current (one fact, updated with `--replaces`); brief every subagent with %s. "+
		"Follow the shared rules (`kmem rules`).", promptTitle(t.Title), t.Slug, t.Slug, load, t.Slug, sub)
	if isNew {
		p += fmt.Sprintf(" This topic is new and has no facts yet: write its first `%s/status` fact once there is something to record.", t.Slug)
	}
	return p
}

func firstNonEmptyList(a, b []string) []string {
	if len(a) > 0 {
		return a
	}
	return b
}

// pickTopic opens topic t in the pending tab s: a live topic switches to its
// tab instead; otherwise s becomes the topic's fresh coordinator.
func (d *desk) pickTopic(s *deskSession, t topicInfo, isNew bool) {
	if !s.pickPending {
		return
	}
	d.setDormant(t.Slug, false)
	if owner := d.topicOwner(t.Slug, s); owner != nil {
		d.noteRecentTopic(t.Slug)
		d.openOwner(owner)
		return
	}
	if t.Slug == quillOpName && d.quillOpOn() {
		d.noteRecentTopic(t.Slug)
		d.notify(s, "Quill is the Quill operator's topic: it runs behind the Quill button, not in a tab.")
		d.openQuillOperator()
		return
	}
	if e, ok := d.topicExternal(t.Slug); ok {
		d.refreshExtSessions(true)
		d.notify(s, extNotice(t, e)) // the card stays: pick another topic
		return
	}
	d.noteRecentTopic(t.Slug)
	s.pickPending = false
	if isNew && t.Title != "" {
		if d.org.TopicTitles == nil {
			d.org.TopicTitles = map[string]string{}
		}
		d.org.TopicTitles[t.Slug] = t.Title
		seedSavedTopicTitles(d.org.TopicTitles)
	}
	s.kmemNew = false // the topic prompt names the kmem name itself
	d.org.Titles[s.id] = t.Title
	d.org.Kmem[s.id] = t.Slug
	d.afterPick(s, false)
	go func() {
		p := d.topicPrompt(t, isNew)
		d.do(func() { d.sendResume(s, t.Slug, p) })
	}()
}

// pickNewTopic: "New topic…" with a free name. A name that is an existing
// topic opens that topic.
func (d *desk) pickNewTopic(s *deskSession, text string) {
	title := strings.TrimSpace(text)
	if title == "" || !s.pickPending {
		return
	}
	slug := topicSlugFor(title)
	if slug == "kmem" {
		d.notify(s, "kmem is the kmem window's topic, not a tab: open it with the kmem button.")
		return
	}
	if t, ok := cachedTopic(slug); ok {
		d.pickTopic(s, t, false)
		return
	}
	t := topicInfo{Slug: slug, Title: title, Scope: []string{slug}}
	rememberNewTopic(t)
	d.pickTopic(s, t, true)
}

// openTopic: the one entry for paths that start a topic session outside the
// card (phone, --resume-all, kmem window): switch to its live session, or
// start a fresh one. nil when the topic is live outside Quill Desktop.
func (d *desk) openTopic(slug string) *deskSession {
	t, ok := cachedTopic(slug)
	if !ok {
		t = topicInfo{Slug: slug, Title: topicTitle(slug), Scope: []string{slug}}
	}
	return d.openTopicInfo(t, false, homeDir())
}

// openTopicInfo is openTopic for a known topic: a live one switches, a topic
// live outside Quill Desktop (K6) is refused (nil), otherwise a fresh
// coordinator starts in cwd (isNew: the topic has no facts yet).
func (d *desk) openTopicInfo(t topicInfo, isNew bool, cwd string) *deskSession {
	if owner := d.topicOwner(t.Slug, &deskSession{}); owner != nil {
		d.setDormant(t.Slug, false)
		d.noteRecentTopic(t.Slug)
		d.openOwner(owner)
		return owner
	}
	if t.Slug == quillOpName && d.quillOpOn() {
		// The operator's topic never opens a tab; it may still be starting (nil).
		d.setDormant(t.Slug, false)
		d.noteRecentTopic(t.Slug)
		d.openQuillOperator()
		if a := d.quillAssist(); a.s != nil && a.s.running() {
			return a.s
		}
		return nil
	}
	if _, ext := d.topicExternal(t.Slug); ext {
		return nil // live outside Quill Desktop (K6): refused, no tab
	}
	ns := d.createSession(cwd, "default", "default")
	ns.pickPending = true
	d.pickTopic(ns, t, isNew)
	return ns
}

// openOwner brings the topic's live session forward: its tab, or the host of
// a hidden one (the Quill operator's box).
func (d *desk) openOwner(owner *deskSession) {
	if isHiddenSession(owner) {
		d.openHidden(owner.id)
		return
	}
	d.open(owner.id)
}

// isDormant: the topic is hidden from the "Open a topic" list until opened.
func (d *desk) isDormant(slug string) bool { return slices.Contains(d.org.Dormant, slug) }

// setDormant hides (or shows again) a topic; opening a topic in any way
// calls it with false. Saves the org only on a change.
func (d *desk) setDormant(slug string, on bool) {
	if d.isDormant(slug) == on {
		return
	}
	if on {
		d.org.Dormant = append(d.org.Dormant, slug)
	} else {
		d.org.Dormant = slices.DeleteFunc(d.org.Dormant, func(x string) bool { return x == slug })
	}
	d.saveOrg()
	for _, refresh := range dormantCards {
		refresh()
	}
}

// dormantCards: the open "Open a topic" cards' list refreshers (UI thread).
var dormantCards = map[*deskSession]func(){}

// dormantCommand: `quill --hide-topic` / `--show-topic` (UI thread): a
// comma-separated slug list. false when a slug is not a kmem name.
func (d *desk) dormantCommand(hide bool, list string) bool {
	slugs := strings.Split(list, ",")
	for _, sl := range slugs {
		if !validKmemName(sl) {
			return false
		}
	}
	for _, sl := range slugs {
		d.setDormant(sl, hide)
	}
	return true
}

// topicButtonLabel: "Title · <status> · 1 claimed · 4 open".
func topicButtonLabel(t topicInfo) string {
	parts := []string{t.Title}
	if t.Status != "" {
		st := []rune(t.Status)
		if len(st) > 60 {
			st = append(st[:59], '…')
		}
		parts = append(parts, string(st))
	}
	if t.Claimed > 0 || t.Open > 0 {
		parts = append(parts, fmt.Sprintf("%d claimed · %d open", t.Claimed, t.Open))
	}
	return strings.Join(parts, " · ")
}

const legacyNoticeKey = "topicLegacyNoticed"

const legacyNotice = "This session predates topic tabs. Merge it into a topic at a safe point."

// noteLegacy posts the one-time merge notice (design §3.6, decision 14) on a
// live session whose kmem name is an old, non-topic one. kmem mode only, once
// the topics are known. Sessions without a kmem name (scratch, or a tab being
// set up) get none.
func (d *desk) noteLegacy(id string) {
	s := d.sessions[id]
	if s == nil || s.pickPending || isHiddenSession(s) || d.org.Kmem[id] == "" || d.topicOf(id) != "" {
		return
	}
	prefs := d.app.Preferences()
	done := strings.Split(prefs.String(legacyNoticeKey), "\n")
	if contains(done, id) {
		return
	}
	if len(done) > 200 {
		done = done[len(done)-200:]
	}
	prefs.SetString(legacyNoticeKey, strings.Trim(strings.Join(append(done, id), "\n"), "\n"))
	d.notify(s, legacyNotice)
}
