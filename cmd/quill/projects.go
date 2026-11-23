package main

import (
	"encoding/json"
	"github.com/katachinosekai/quill/internal/transcript"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// The sidebar lists only live sessions (open tabs and running Quill
// sessions), grouped by kmem project (user 2026-11-21). Ended sessions live
// on as kmem handoffs in the kmem panel. A session's project is keyed by its
// kmem name, because a tab, its Claude session and its kmem name are one
// identity.

const projectPrefix = "p:"

type kmemSessionCfg struct {
	As      string `json:"as"`
	Topic   string `json:"topic"`
	Project string `json:"project"`
	// Topic labels (assumed K1, docs/design/topic-tabs.md §3.1/§4): a label
	// keyed by a top-level topic with a scope marks a topic coordinator.
	Title string    `json:"title"`
	Scope scopeList `json:"scope"`
}

// scopeList reads a scope written as a JSON list or as "a,b,c".
type scopeList []string

func (l *scopeList) UnmarshalJSON(b []byte) error {
	var list []string
	if json.Unmarshal(b, &list) != nil {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return nil // an unknown shape is no scope, not a broken config
		}
		list = strings.Split(s, ",")
	}
	*l = nil
	for _, t := range list {
		if t = strings.TrimSpace(t); t != "" {
			*l = append(*l, t)
		}
	}
	return nil
}

var (
	kmemCfgMu   sync.Mutex
	kmemCfgAt   time.Time
	kmemCfgSess map[string]kmemSessionCfg
	kmemCfgPath = func() string {
		if p := os.Getenv("KMEM_CONFIG"); p != "" { // same override as kmem itself
			return p
		}
		return filepath.Join(homeDir(), ".config", "kmem", "config.json")
	}
)

// resetKmemSessions drops the cached labels (after a rename moved one).
func resetKmemSessions() {
	kmemCfgMu.Lock()
	kmemCfgSess = nil
	kmemCfgMu.Unlock()
}

// kmemSessions reads the "sessions" labels from kmem's config (read-only,
// cached for a minute).
func kmemSessions() map[string]kmemSessionCfg {
	kmemCfgMu.Lock()
	defer kmemCfgMu.Unlock()
	if kmemCfgSess != nil && time.Since(kmemCfgAt) < time.Minute {
		return kmemCfgSess
	}
	kmemCfgAt = time.Now()
	kmemCfgSess = map[string]kmemSessionCfg{}
	if b, err := os.ReadFile(kmemCfgPath()); err == nil {
		var c struct {
			Sessions map[string]kmemSessionCfg `json:"sessions"`
		}
		if json.Unmarshal(b, &c) == nil && c.Sessions != nil {
			kmemCfgSess = c.Sessions
		}
	}
	return kmemCfgSess
}

func firstWord(s string) string {
	f := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, w := range f {
		if strings.Trim(w, "0123456789") == "" {
			continue
		}
		r := []rune(w)
		if len(r) > 1 && strings.ToLower(w) == w {
			r[0] = unicode.ToUpper(r[0])
		}
		return string(r)
	}
	return ""
}

func (d *desk) projectKeyFor(id string) string {
	if k := d.org.Kmem[id]; k != "" {
		return k
	}
	return id
}

// projectOf: the project set in Quill, else kmem's config ("project", then
// the first word of "topic"), else the first word of the session's title.
func (d *desk) projectOf(id string) string {
	if s := d.sessions[id]; s != nil && s.remote != nil {
		return firstNonEmpty(s.remote.project, s.remote.link.conf.Name)
	}
	if p := d.org.Projects[d.projectKeyFor(id)]; p != "" {
		return p
	}
	if k := d.topicOf(id); k != "" { // a topic coordinator groups under its topic
		if t, ok := cachedTopic(k); ok {
			return t.Title
		}
	}
	if c, ok := kmemSessions()[d.org.Kmem[id]]; ok {
		if c.Project != "" {
			return c.Project
		}
		if w := firstWord(c.Topic); w != "" {
			return w
		}
	}
	if transcript.DesktopScratch(d.cwdOf(id)) {
		return desktopScratchLabel
	}
	title, _, _ := d.rowText(id)
	if w := firstWord(title); w != "" && !strings.EqualFold(w, "New") {
		return w
	}
	return "Sessions"
}

const desktopScratchLabel = "Claude desktop app (scratch)"

// folderLabel: how a session's folder is shown; a Claude desktop app scratch
// workspace gets a name instead of its long AppData path.
func folderLabel(cwd string) string {
	if transcript.DesktopScratch(cwd) {
		return desktopScratchLabel
	}
	return tildePath(cwd)
}

func (d *desk) cwdOf(id string) string {
	if s := d.sessions[id]; s != nil && s.cwd != "" {
		return s.cwd
	}
	return d.infos[id].Cwd
}

func (d *desk) setProject(id, project string) {
	project = strings.TrimSpace(project)
	if d.org.Projects == nil {
		d.org.Projects = map[string]string{}
	}
	if project == "" {
		delete(d.org.Projects, d.projectKeyFor(id))
	} else {
		d.org.Projects[d.projectKeyFor(id)] = project
	}
	d.saveOrg()
	d.rebuildRows()
}

func (d *desk) renameProject(from, to string) {
	to = strings.TrimSpace(to)
	if to == "" || to == from {
		return
	}
	for _, g := range d.groups {
		if g.key == projectPrefix+from {
			for _, id := range g.ids {
				if d.org.Projects == nil {
					d.org.Projects = map[string]string{}
				}
				d.org.Projects[d.projectKeyFor(id)] = to
			}
		}
	}
	if v, ok := d.folderOpen[projectPrefix+from]; ok {
		d.folderOpen[projectPrefix+to] = v
	}
	if dirs := d.org.Dirs[from]; len(dirs) > 0 {
		d.org.Dirs[to] = append(d.org.Dirs[to], dirs...)
		delete(d.org.Dirs, from)
	}
	d.saveOrg()
	d.rebuildRows()
}

// sessionDirs: the session's own /add-dir folders plus its project's.
func (d *desk) sessionDirs(s *deskSession) []string {
	out := append([]string(nil), s.addDirs...)
	for _, dir := range d.org.Dirs[d.projectOf(s.id)] {
		if !contains(out, dir) {
			out = append(out, dir)
		}
	}
	return out
}

// setProjectDir adds or removes an extra folder for every session of a project.
func (d *desk) setProjectDir(project, dir string, add bool) {
	if d.org.Dirs == nil {
		d.org.Dirs = map[string][]string{}
	}
	list := removeString(d.org.Dirs[project], dir)
	if add {
		list = append(list, dir)
	}
	if len(list) == 0 {
		delete(d.org.Dirs, project)
	} else {
		d.org.Dirs[project] = list
	}
	d.saveOrg()
}

func (d *desk) projectNames() []string {
	var out []string
	for _, g := range d.groups {
		if strings.HasPrefix(g.key, projectPrefix) {
			out = append(out, g.title)
		}
	}
	return out
}

// liveSession: open in a tab, or a running Quill session (not one held by a
// terminal elsewhere).
func (d *desk) liveSession(id string) bool {
	if d.hiddenID(id, "") {
		return false // the kmem window's assistant (docs/design/kmem-window.md §4)
	}
	for _, t := range d.tabs {
		if t == id && !d.staleTab(id) {
			return true
		}
	}
	s := d.sessions[id]
	return s != nil && s.running() && s.terminal == nil
}

type projectGroup struct {
	name    string
	entries []sideEntry
	ext     []extSession // live outside Quill Desktop (K6): read-only rows
}

func (d *desk) projectGroups(entries []sideEntry) []projectGroup {
	tabPos := map[string]int{}
	for i, t := range d.tabs {
		tabPos[t] = i
	}
	// kmem mode: topic coordinators group under their topic; other live
	// sessions (old names until merged, scratch) stay under their project
	// with a one-time merge notice (topic-tabs decision 14).
	if d.kmemMemory() {
		d.refreshTopics()
		if topicsLoaded() {
			for _, e := range entries {
				d.noteLegacy(e.id)
			}
		}
	}
	by := map[string][]sideEntry{}
	for _, e := range entries {
		p := d.projectOf(e.id)
		by[p] = append(by[p], e)
	}
	ext := map[string][]extSession{} // group name -> external live sessions
	if d.kmemMemory() {
		d.refreshExtSessions(false)
		for slug, es := range d.externalLive() {
			if t, ok := cachedTopic(slug); ok {
				ext[t.Title] = append(ext[t.Title], es...)
				if _, has := by[t.Title]; !has {
					by[t.Title] = nil
				}
			}
		}
	}
	var out []projectGroup
	for name, es := range by {
		sort.SliceStable(es, func(i, j int) bool {
			pi, iok := tabPos[es[i].id]
			pj, jok := tabPos[es[j].id]
			if iok != jok {
				return iok
			}
			if iok {
				return pi < pj
			}
			return es[i].last.After(es[j].last)
		})
		out = append(out, projectGroup{name, es, ext[name]})
	}
	less := sortLess()
	sort.Slice(out, func(i, j int) bool { return less(out[i].name, out[j].name) })
	return out
}
