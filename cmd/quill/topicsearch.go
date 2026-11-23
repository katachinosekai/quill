package main

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// Search-first "Open a topic" card (user decision 2026-11-23, kmem fb10aa6):
// a search box with the last five opened topics under it; typing matches
// slugs, titles and kmem content ("mentions ..."); a new name close to an
// existing topic gets "Did you mean ...?". Shared by the card and the phone
// API (GET /phone/topics/search).

const (
	recentTopicsKey  = "recentTopics"
	recentTopicsKeep = 20
	recentTopicsShow = 5
	recallHitLimit   = "8"
	snippetMax       = 100
	// relateFeature: `kmem version --json` lists it from kmem 1.12.0; the
	// related-topics line stays hidden without it.
	relateFeature = "relate"
)

// topicHit is one search result. Why: "name" (slug), "title", "content" or
// "recent" (empty search).
type topicHit struct {
	Topic   topicInfo
	Why     string
	Snippet string // content hits: one line of the matching fact
	Dormant bool
	rank    int
}

// topicSearchResult: what typing a query shows.
type topicSearchResult struct {
	Hits        []topicHit
	DidYouMean  *topicInfo
	NeedContent bool
}

// noteRecentTopic records that a topic was opened (UI thread): newest first.
func (d *desk) noteRecentTopic(slug string) {
	if slug == "" {
		return
	}
	prefs := d.app.Preferences()
	cur := prefs.StringList(recentTopicsKey)
	next := []string{slug}
	for _, s := range cur {
		if s != slug && len(next) < recentTopicsKeep {
			next = append(next, s)
		}
	}
	prefs.SetStringList(recentTopicsKey, next)
}

// recentTopics: the last n opened topics that still exist and are not
// dormant, newest first. When fewer than n were opened, the gap fills with
// topics that have a status fact (kmem reports no status age, so the list
// order stands for "most recently active").
func (d *desk) recentTopics(ts []topicInfo, n int) []topicInfo {
	by := map[string]topicInfo{}
	for _, t := range ts {
		by[t.Slug] = t
	}
	var out []topicInfo
	seen := map[string]bool{}
	for _, s := range d.app.Preferences().StringList(recentTopicsKey) {
		t, ok := by[s]
		if !ok || seen[s] || d.isDormant(s) {
			continue
		}
		seen[s] = true
		out = append(out, t)
		if len(out) == n {
			return out
		}
	}
	for _, t := range ts {
		if t.Status == "" || seen[t.Slug] || d.isDormant(t.Slug) {
			continue
		}
		seen[t.Slug] = true
		out = append(out, t)
		if len(out) == n {
			break
		}
	}
	return out
}

func searchKey(s string) string { return foldKey(titleKey(s)) }

// matchTopicsLocal: topics whose slug or title matches q: exact, then
// prefix (of the text or of a word), then substring. Dormant topics are
// included (and marked): they appear only because the user typed.
func matchTopicsLocal(q string, ts []topicInfo, dormant func(string) bool) []topicHit {
	k := searchKey(q)
	if k == "" {
		return nil
	}
	var out []topicHit
	for _, t := range ts {
		why, rank := "", 9
		for _, f := range []struct{ field, text string }{{"name", t.Slug}, {"title", t.Title}} {
			if r := matchRank(k, searchKey(f.text)); r < rank {
				rank, why = r, f.field
			}
		}
		if rank < 9 {
			out = append(out, topicHit{Topic: t, Why: why, Dormant: dormant(t.Slug), rank: rank})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Dormant != out[j].Dormant {
			return !out[i].Dormant
		}
		if out[i].rank != out[j].rank {
			return out[i].rank < out[j].rank
		}
		return out[i].Topic.Title < out[j].Topic.Title
	})
	return out
}

func matchRank(q, text string) int {
	switch {
	case text == q:
		return 0
	case strings.HasPrefix(text, q):
		return 1
	}
	for _, w := range strings.FieldsFunc(text, func(r rune) bool { return r == ' ' || r == '-' || r == '_' }) {
		if strings.HasPrefix(w, q) {
			return 1
		}
	}
	if strings.Contains(text, q) {
		return 2
	}
	return 9
}

var recallLine = regexp.MustCompile(`^(\S+): \[[0-9a-f]+\] [a-z-]+: (.*)$`)

// recallSnippet: one line of a fact, cut around the first query word.
func recallSnippet(text, q string) string {
	text = strings.Join(strings.Fields(text), " ")
	r := []rune(text)
	if len(r) <= snippetMax {
		return text
	}
	start := 0
	if lt := strings.ToLower(text); len(lt) == len(text) {
		for _, w := range strings.Fields(strings.ToLower(q)) {
			if i := strings.Index(lt, w); i >= 0 {
				start = max(0, len([]rune(text[:i]))-30)
				break
			}
		}
	}
	end := min(len(r), start+snippetMax)
	s := string(r[start:end])
	if start > 0 {
		s = "…" + s
	}
	if end < len(r) {
		s += "…"
	}
	return s
}

// topicForTop: the topic a kmem top-level name belongs to (its own slug,
// else a labeled topic whose scope includes it).
func topicForTop(top string, ts []topicInfo) (topicInfo, bool) {
	for _, t := range ts {
		if t.Slug == top {
			return t, true
		}
	}
	for _, t := range ts {
		if contains(t.Scope, top) {
			return t, true
		}
	}
	return topicInfo{}, false
}

// contentHits: `kmem recall q -n 8` mapped to top-level topics, one hit per
// topic (the best-ranked fact's line), skipping topics in skip. Not on the UI
// thread.
func (d *desk) contentHits(q string, ts []topicInfo, skip map[string]bool, dormant func(string) bool) []topicHit {
	q = strings.TrimSpace(q)
	if q == "" || !d.kmemAvailable() {
		return nil
	}
	out, err := d.runKmem("kmem", "recall", "-n", recallHitLimit, "--", q)
	if err != nil {
		return nil
	}
	var hits []topicHit
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		m := recallLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		t, ok := topicForTop(topLevel(m[1]), ts)
		if !ok || seen[t.Slug] || skip[t.Slug] || t.Slug == "kmem" {
			continue
		}
		seen[t.Slug] = true
		hits = append(hits, topicHit{Topic: t, Why: "content", Snippet: kmemScrubber()(recallSnippet(m[2], q)), Dormant: dormant(t.Slug)})
	}
	sort.SliceStable(hits, func(i, j int) bool { return !hits[i].Dormant && hits[j].Dormant })
	return hits
}

// didYouMean: the existing topic a new name resembles (slug or title within
// a small edit distance, or one a prefix of the other), nil when the name
// is a topic already (it opens that one) or resembles none.
func didYouMean(text string, ts []topicInfo) *topicInfo {
	k := searchKey(text)
	slug := topicSlugFor(text)
	if len([]rune(k)) < 3 {
		return nil
	}
	for _, t := range ts {
		if t.Slug == slug || searchKey(t.Title) == k {
			return nil
		}
	}
	kslug := strings.Trim(slugJunk.ReplaceAllString(k, "-"), "-")
	var best *topicInfo
	bestD := 1 << 30
	for i, t := range ts {
		if t.Slug == "kmem" {
			continue
		}
		tk := searchKey(t.Title)
		dist := min(levenshtein(k, tk), levenshtein(kslug, t.Slug), levenshtein(k, t.Slug))
		limit := 1
		if n := len([]rune(k)); n >= 10 {
			limit = 3
		} else if n >= 4 {
			limit = 2
		}
		if dist > limit {
			dist = 1 << 30
		}
		for _, other := range []string{tk, t.Slug} {
			if len([]rune(k)) >= 3 && k != other && (strings.HasPrefix(other, k) || strings.HasPrefix(k, other) && len([]rune(other)) >= 3) {
				dist = min(dist, 0)
			}
		}
		if dist < bestD {
			bestD, best = dist, &ts[i]
		}
	}
	if bestD == 1<<30 {
		return nil
	}
	return best
}

// searchLocal: everything a query shows without asking kmem.
func (d *desk) searchLocal(q string, ts []topicInfo) topicSearchResult {
	r := topicSearchResult{Hits: matchTopicsLocal(q, ts, d.isDormant)}
	if strings.TrimSpace(q) != "" {
		r.DidYouMean = didYouMean(q, ts)
		r.NeedContent = true
	}
	return r
}

// searchTopics: local matches plus kmem content matches. Not on the UI thread.
func (d *desk) searchTopics(q string, ts []topicInfo) topicSearchResult {
	r := d.searchLocal(q, ts)
	skip := map[string]bool{}
	for _, h := range r.Hits {
		skip[h.Topic.Slug] = true
	}
	r.Hits = append(r.Hits, d.contentHits(q, ts, skip, d.isDormant)...)
	return r
}

// relatedTopics: topics kmem relates to slug (kmem 1.12.0 "relate"); nil
// without the feature. Not on the UI thread.
func (d *desk) relatedTopics(slug string) []string {
	if !d.kmemHasFeature(relateFeature) {
		return nil
	}
	var r struct {
		Related []json.RawMessage `json:"related"`
	}
	if !d.kmemJSON(&r, "relate", slug) {
		return nil
	}
	var out []string
	for _, raw := range r.Related {
		var s string
		var o struct {
			Topic string `json:"topic"`
			Name  string `json:"name"`
		}
		switch {
		case json.Unmarshal(raw, &s) == nil:
		case json.Unmarshal(raw, &o) == nil:
			s = firstNonEmpty(o.Topic, o.Name)
		}
		if s = topLevel(s); s != "" && s != slug && !contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
