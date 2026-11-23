package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
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

// topicBriefingSince is the first kmem version assumed to have the topic
// briefing `kmem context --topic T` (K2); older versions get the ranking
// fallback. Assumption until kmem-lock-audit ships K2.
const topicBriefingSince = "1.9.0"

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
}

func resetTopicCache() {
	topicCache.mu.Lock()
	topicCache.at, topicCache.loaded, topicCache.busy, topicCache.list, topicCache.extra = time.Time{}, false, false, nil, nil
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

// statusLine: the text of the first fact `kmem get T/status` prints
// ("[f1234ab] fact: text"), "" when there is none.
func statusLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "[") {
			continue
		}
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
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
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
		t := &out[i]
		t.Status = status[t.Slug]
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
	topicCache.mu.Lock()
	topicCache.list, topicCache.at, topicCache.loaded, topicCache.busy = list, time.Now(), true, false
	topicCache.mu.Unlock()
	return list
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
	return topicInfo{}, false
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
	return topicPromptFor(t, d.kmemInstalledVersion(), isNew)
}

func topicPromptFor(t topicInfo, kmemVersion string, isNew bool) string {
	scope := strings.Join(firstNonEmptyList(t.Scope, []string{t.Slug}), ",")
	var load, sub string
	if kmemVersion != "" && !versionLess(kmemVersion, topicBriefingSince) {
		load = fmt.Sprintf("Run `kmem brief` and `kmem context --topic %s`", t.Slug)
		sub = fmt.Sprintf("`kmem context \"<task>\" --topic %s`", t.Slug)
	} else {
		// kmem before the topic briefing: --topic only ranks, so ask for the
		// claimed and open tasks explicitly and name the whole scope.
		load = fmt.Sprintf("Run `kmem brief`, `kmem task list` and `kmem context \"%s coordinator: claimed tasks, open tasks, status, next steps\" --topic %s`", t.Title, scope)
		sub = fmt.Sprintf("`kmem context \"<task>\" --topic %s`", scope)
	}
	p := fmt.Sprintf("You are the %s coordinator; kmem name %q (`kmem --as %s`). %s; "+
		"continue the claimed tasks from their checkpoints and take the open ones as next steps; "+
		"keep `%s/status` current (one fact, updated with `--replaces`); brief every subagent with %s. "+
		"Follow the shared rules (`kmem rules`).", t.Title, t.Slug, t.Slug, load, t.Slug, sub)
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
	if owner := d.topicOwner(t.Slug, s); owner != nil {
		d.open(owner.id)
		return
	}
	s.pickPending = false
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
	slug := kmemSlug(title, func(string) bool { return false })
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
// start a fresh one.
func (d *desk) openTopic(slug string) *deskSession {
	if owner := d.topicOwner(slug, &deskSession{}); owner != nil {
		d.open(owner.id)
		return owner
	}
	t, ok := cachedTopic(slug)
	if !ok {
		t = topicInfo{Slug: slug, Title: topicTitle(slug), Scope: []string{slug}}
	}
	ns := d.createSession(homeDir(), "default", "default")
	ns.pickPending = true
	d.pickTopic(ns, t, false)
	return ns
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
