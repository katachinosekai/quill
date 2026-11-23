package main

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// External live sessions (docs/design/topic-tabs.md §3.2, §3.6, K6): a Claude
// session outside Quill Desktop (a terminal using `kmem --as zomboid`) shows
// under its topic as a read-only "running elsewhere" row, and counts as the
// topic's live session for the one-live-per-topic check. The list comes from
// `kmem sessions --live --json` (kmem 1.10.0, feature "sessions"); without
// the feature nothing is shown and nothing is checked, as before.

// extSessionsFeature: the `kmem version --json` feature of `kmem sessions`.
const extSessionsFeature = "sessions"

// extRefresh: the cached list is reloaded in the background at most this
// often (the sidebar rebuilds far more often); a forced load runs on demand.
const extRefresh = 30 * time.Second

// extSession: a live Claude session some other process runs.
type extSession struct {
	ID   string
	Name string // the last kmem --as name
}

var extCache struct {
	mu     sync.Mutex
	at     time.Time
	loaded bool
	busy   bool
	list   []extSession
}

func resetExtSessions() {
	extCache.mu.Lock()
	extCache.at, extCache.loaded, extCache.busy, extCache.list = time.Time{}, false, false, nil
	extCache.mu.Unlock()
}

// loadExtSessions asks kmem for the live sessions and caches them. Not on the
// UI thread. Only sessions kmem reports "live" count; "gone" and "unknown"
// (kmem cannot tell) are never shown as live.
func (d *desk) loadExtSessions() []extSession {
	var list []extSession
	if d.kmemHasFeature(extSessionsFeature) {
		var r struct {
			Sessions []struct {
				Session string `json:"session"`
				Name    string `json:"name"`
				Live    string `json:"live"`
			} `json:"sessions"`
		}
		if d.kmemJSON(&r, "sessions", "--live") {
			for _, s := range r.Sessions {
				if s.Live == "live" && s.Session != "" && strings.TrimSpace(s.Name) != "" {
					list = append(list, extSession{ID: s.Session, Name: strings.TrimSpace(s.Name)})
				}
			}
		}
	}
	extCache.mu.Lock()
	extCache.list, extCache.at, extCache.loaded, extCache.busy = list, time.Now(), true, false
	extCache.mu.Unlock()
	return list
}

// refreshExtSessions reloads the cache in the background when it is older
// than extRefresh (or force), then rebuilds the sidebar if the list changed.
// UI thread; does nothing without the kmem feature.
func (d *desk) refreshExtSessions(force bool) {
	if !d.kmemFeatureCached(extSessionsFeature) {
		return
	}
	extCache.mu.Lock()
	stale := !extCache.busy && (force || !extCache.loaded || time.Since(extCache.at) >= extRefresh)
	if stale {
		extCache.busy = true
	}
	old := extCache.list
	extCache.mu.Unlock()
	if !stale {
		return
	}
	go func() {
		list := d.loadExtSessions()
		if !sameExt(old, list) {
			d.do(d.rebuildRows)
		}
	}()
}

func sameExt(a, b []extSession) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// externalLive: the cached external live sessions that hold a topic, by
// topic slug (design: explicit kmem config mapping, topicForName). Sessions
// Quill itself runs (any entry of d.sessions, including the kmem window's)
// are not external. UI thread; cache only.
func (d *desk) externalLive() map[string][]extSession {
	extCache.mu.Lock()
	list := extCache.list
	extCache.mu.Unlock()
	out := map[string][]extSession{}
	for _, e := range list {
		if d.sessions[e.ID] != nil {
			continue
		}
		slug := topicForName(e.Name)
		if slug == "" || slug == "kmem" {
			continue
		}
		out[slug] = append(out[slug], e)
	}
	for _, es := range out {
		sort.Slice(es, func(i, j int) bool { return es[i].ID < es[j].ID })
	}
	return out
}

// topicExternal: an external live session holding the topic.
func (d *desk) topicExternal(slug string) (extSession, bool) {
	if es := d.externalLive()[slug]; len(es) > 0 {
		return es[0], true
	}
	return extSession{}, false
}

// extNotice: why a topic cannot be opened in a tab.
func extNotice(t topicInfo, e extSession) string {
	return t.Title + " is already live outside Quill Desktop (kmem name " + e.Name +
		"). End that session first; a topic has one live session."
}
