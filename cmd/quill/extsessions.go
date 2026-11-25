package main

import (
	"fyne.io/fyne/v2/dialog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/katachinosekai/quill/internal/live"
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
		if e.Name == quillOpName || e.Name == kmemWinOperator {
			slug = e.Name
		}
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

// extTakeOverBlock: why e cannot be taken over here, "" if it can. Only a
// session whose Claude process this computer's live-session files list can be
// released; one on another machine (kmem sees it, we do not) cannot.
func (d *desk) extTakeOverBlock(e extSession) string {
	switch {
	case !d.kmemFeatureCached(extSessionsFeature):
		return "needs a newer kmem"
	case topicForName(e.Name) == "":
		return "not a topic"
	}
	if _, ok := d.terms[e.ID]; !ok {
		return "not running on this computer"
	}
	if _, ok := d.infos[e.ID]; !ok {
		return "its conversation is not on this computer"
	}
	return ""
}

// extWhere: where the session runs, for the confirm text.
func extWhere(p live.Process) string {
	switch p.Entry {
	case "", "cli":
		return "the terminal"
	}
	return "the " + p.Entry + " session"
}

// offerTakeOver asks, then takes the external session e over: its terminal
// Claude stops and the topic continues in a Quill Desktop tab (kmem name =
// topic). pending: the "Open a topic" card's tab, closed once the topic's tab
// is open. UI thread.
func (d *desk) offerTakeOver(e extSession, pending *deskSession) {
	slug := topicForName(e.Name)
	if why := d.extTakeOverBlock(e); why != "" {
		dialog.ShowInformation("Can't take over", "Quill Desktop can't take over "+e.Name+": "+why+".", d.win)
		return
	}
	t, ok := cachedTopic(slug)
	if !ok {
		t = topicInfo{Slug: slug, Title: topicTitle(slug), Scope: []string{slug}}
	}
	p := d.terms[e.ID]
	msg := "Take over " + t.Title + " from " + extWhere(p) + "? The terminal session stops and continues here."
	if slug == quillOpName && d.quillOpOn() {
		msg = "Take over the Quill operator from " + extWhere(p) + "? The terminal session stops and continues behind the Quill button."
	}
	if p.Busy() {
		msg += " It is in the middle of a turn; that turn is stopped."
	}
	dialog.ShowConfirm("Take over?", msg, func(ok bool) {
		if ok {
			d.takeOverTopic(e, t, pending)
		}
	}, d.win)
}

// takeOverTopic: the confirmed Take over. Re-checks (the world changed while
// the dialog was open), names the session after the topic, then the existing
// takeOver flow releases the process and resumes the session in a tab.
func (d *desk) takeOverTopic(e extSession, t topicInfo, pending *deskSession) {
	if why := d.extTakeOverBlock(e); why != "" {
		dialog.ShowInformation("Can't take over", "Quill Desktop can't take over "+e.Name+": "+why+".", d.win)
		return
	}
	if owner := d.topicOwner(t.Slug, pending); owner != nil {
		d.openOwner(owner) // someone else got the topic first
		return
	}
	p := d.terms[e.ID]
	s := d.session(e.ID)
	if s == nil || s.running() || s.takeover {
		return
	}
	d.org.Titles[s.id] = t.Title
	d.org.Kmem[s.id] = t.Slug
	s.kmemNew = false
	d.saveOrg()
	operator := t.Slug == quillOpName && d.quillOpOn()
	if operator {
		d.markHiddenAs(s, roleQuill) // it starts with the operator's prompt, never as a tab
	}
	d.takeOverThen(s, p, "", func(err error) {
		if err != nil {
			return
		}
		if operator {
			d.quillAdopt(d.quillAssist(), s, "Taken over from "+extWhere(p)+"; it keeps running here.")
			if pending != nil && pending.pickPending {
				d.closeTab(pending.id)
			}
			d.refreshExtSessions(true)
			return
		}
		d.afterPick(s, false)
		d.open(s.id)
		if pending != nil && pending != s && pending.pickPending {
			d.closeTab(pending.id)
		}
		d.refreshExtSessions(true)
	})
}
