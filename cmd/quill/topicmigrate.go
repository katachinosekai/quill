package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// One-time migration of the old sidebar data into topic tabs
// (docs/design/topic-tabs.md §3.7, §3.12; decision 15). kmem memory mode
// only, and only with a kmem that has the end-of-session check: without it
// End and rotation still save handoffs and `--resume-all` still reads the
// Inactive place, so the places stay until then.
//
// Before anything changes, the old data is written to a timestamped backup
// file in Quill Desktop's storage folder and copied to the preferences
// sidebarOrgPreTopics / tabsPreTopics. Then the places, folders and the
// sorting state are dropped, and tabs of ended sessions are closed. Titles,
// kmem links, ended/hidden marks, remote names, projects and project folders
// are kept as they are (old-name sessions keep working until they merge).
// The marker topicMigration=1 makes it run once.

const (
	topicMigrationKey     = "topicMigration"
	topicMigrationVersion = 1
	orgPreTopicsKey       = "sidebarOrgPreTopics"
	tabsPreTopicsKey      = "tabsPreTopics"
	topicsAtQuitKey       = "topicsLiveAtQuit"
	topicIDsAtQuitKey     = "topicSessionsAtQuit"
	orgBackupDir          = "backups"
)

// placesRemoved: the topic migration ran, so no session has a sidebar place
// any more (End, rotation and restarts write none).
func (d *desk) placesRemoved() bool {
	return d.app.Preferences().Int(topicMigrationKey) >= topicMigrationVersion
}

// migrateTopicsWhenReady runs the migration once kmem's features are known
// (not on the UI thread: it asks `kmem version`).
func (d *desk) migrateTopicsWhenReady() {
	if d.placesRemoved() || !d.kmemMemory() || !d.kmemHasFeature(endCheckFeature) {
		return
	}
	d.do(func() {
		path, n, err := d.migrateTopics(time.Now())
		switch {
		case err != nil:
			fmt.Fprintln(os.Stderr, "topic tabs migration:", err)
			d.cliNotice("Quill Desktop could not back up its old sidebar data, so it did not switch to topic tabs yet (" + err.Error() + "). It tries again at the next start.")
		case path != "":
			d.cliNotice(topicMigrationNotice(n, path))
		}
	})
}

func topicMigrationNotice(older int, backup string) string {
	return fmt.Sprintf("Sessions are now topic tabs; the state of each topic is in kmem → Topics; %d older conversation(s) stay on disk (kmem window: Find old conversation). The old sidebar data is backed up in %s.", older, tildePath(backup))
}

// migrateTopics (UI thread) migrates once and returns the backup file and the
// number of older conversations that had a place. A second call does nothing
// and returns "". When the backup cannot be written nothing changes.
func (d *desk) migrateTopics(now time.Time) (string, int, error) {
	if d.placesRemoved() {
		return "", 0, nil
	}
	prefs := d.app.Preferences()
	path, err := d.writeOrgBackup(now)
	if err != nil {
		return "", 0, err
	}
	if prefs.String(orgPreTopicsKey) == "" {
		prefs.SetString(orgPreTopicsKey, prefs.String("sidebarOrg"))
		prefs.SetString(tabsPreTopicsKey, prefs.String("tabs"))
	}
	older := 0
	for id, p := range d.org.Place {
		if p != placeActive && !d.liveSession(id) {
			older++
		}
	}
	d.org.Place = map[string]string{}
	d.org.Folders = nil
	for k := range d.org.Names {
		if isPlaceKey(k) {
			delete(d.org.Names, k)
		}
	}
	tabs := d.tabs[:0:0]
	for _, id := range d.tabs {
		if s := d.sessions[id]; d.org.Ended[id] && (s == nil || !s.running()) {
			continue
		}
		tabs = append(tabs, id)
	}
	d.tabs = tabs
	prefs.RemoveValue("sortState") // Active/Inactive/Archive sorting (rules.go), in the backup
	prefs.SetInt(topicMigrationKey, topicMigrationVersion)
	d.saveTabs()
	d.saveOrg()
	return path, older, nil
}

// isPlaceKey: a sidebar key of the old places or custom folders (their
// renamed titles live in org.Names).
func isPlaceKey(k string) bool {
	for _, p := range []string{placeActive, placeInactive, placeArchive, placeAuto} {
		if k == p || strings.HasPrefix(k, p+":") {
			return true
		}
	}
	return strings.HasPrefix(k, "a:") || strings.HasPrefix(k, folderPrefix)
}

// orgBackup is the backup file: the preference values as they were, verbatim.
type orgBackup struct {
	Note       string `json:"note"`
	Created    string `json:"created"`
	SidebarOrg string `json:"sidebarOrg"`
	Tabs       string `json:"tabs"`
	SortState  string `json:"sortState,omitempty"`
	FolderOpen string `json:"folderOpen,omitempty"`
}

// orgBackupRoot: <Quill Desktop's storage folder>/backups
// (~/.config/fyne/<app id>/backups). Tests point orgBackupBase elsewhere.
func (d *desk) orgBackupRoot() (string, error) {
	return orgBackupBase(d)
}

var orgBackupBase = func(d *desk) (string, error) {
	st := d.app.Storage()
	if st == nil || st.RootURI() == nil || st.RootURI().Path() == "" {
		return "", errors.New("no storage folder")
	}
	return filepath.Join(st.RootURI().Path(), orgBackupDir), nil
}

// writeOrgBackup writes <storage>/backups/sidebar-org-pre-topics-<time>.json
// (never over an existing file) and reads it back.
func (d *desk) writeOrgBackup(now time.Time) (string, error) {
	dir, err := d.orgBackupRoot()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	prefs := d.app.Preferences()
	b := orgBackup{
		Note:       "Quill Desktop sidebar data before topic tabs (docs/design/topic-tabs.md §3.12); each field is the preference value as it was.",
		Created:    now.Format(time.RFC3339),
		SidebarOrg: prefs.String("sidebarOrg"),
		Tabs:       prefs.String("tabs"),
		SortState:  prefs.String("sortState"),
		FolderOpen: prefs.String("folderOpen"),
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return "", err
	}
	base := "sidebar-org-pre-topics-" + now.Format("20060102-150405")
	for i := 1; i < 100; i++ {
		name := base + ".json"
		if i > 1 {
			name = fmt.Sprintf("%s-%d.json", base, i)
		}
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr == nil {
			var back orgBackup
			if rb, err := os.ReadFile(path); err != nil || json.Unmarshal(rb, &back) != nil || back.SidebarOrg != b.SidebarOrg || back.Tabs != b.Tabs {
				werr = errors.New("the backup did not read back")
			}
		}
		if werr != nil {
			os.Remove(path)
			return "", werr
		}
		return path, nil
	}
	return "", errors.New("too many backups at " + base)
}

// rememberTopicsAtQuit records the topics live in Quill Desktop when it
// quits; `quill --resume-all` reopens them as fresh sessions (decision 16).
func (d *desk) rememberTopicsAtQuit() {
	var slugs, ids []string
	for _, s := range d.runningVisible() {
		if t := d.topicOf(s.id); t != "" {
			ids = append(ids, s.id)
			if !slices.Contains(slugs, t) {
				slugs = append(slugs, t)
			}
		}
	}
	slices.Sort(slugs)
	d.app.Preferences().SetStringList(topicsAtQuitKey, slugs)
	d.app.Preferences().SetStringList(topicIDsAtQuitKey, ids)
}

// reopenTopicsAtQuit: `quill --resume-all` after the migration. A topic that
// is live already is switched to; the stopped tab of a session that was live
// at the quit (restored after it) is closed and marked ended, then the topic
// starts fresh.
func (d *desk) reopenTopicsAtQuit() []*deskSession {
	d.showWindow()
	slugs := d.app.Preferences().StringList(topicsAtQuitKey)
	atQuit := d.app.Preferences().StringList(topicIDsAtQuitKey)
	if len(slugs) == 0 {
		d.cliNotice("quill --resume-all: no topics were live when Quill Desktop last quit.")
		return nil
	}
	var out []*deskSession
	for _, slug := range slugs {
		for {
			owner := d.topicOwner(slug, &deskSession{})
			if owner == nil || owner.running() || isHiddenSession(owner) || !slices.Contains(atQuit, owner.id) {
				break
			}
			d.retireStoppedTab(owner.id)
		}
		if ns := d.openTopic(slug); ns != nil {
			out = append(out, ns)
		}
	}
	return out
}

// retireStoppedTab closes the tab of a stopped topic session that is not
// continued (topic sessions never resume); the transcript stays on disk.
func (d *desk) retireStoppedTab(id string) {
	d.tabs = slices.DeleteFunc(d.tabs, func(t string) bool { return t == id })
	d.markEnded(id)
	d.saveTabs()
	d.saveOrg()
}

// carryDirsToTopic: when an old-name session merges or ends, its project's
// extra working folders (/add-dir "every session of this project") are
// copied, never moved, to the project of the topic it maps to. The mapping is
// kmem config sessions[name].topic only (decision f574201), read when the
// session ends, so mappings added after the migration count too (user
// 2026-11-23, option C). After the topic migration only; no saving.
func (d *desk) carryDirsToTopic(id string) {
	k := d.org.Kmem[id]
	if !d.placesRemoved() || k == "" || d.topicOf(id) != "" {
		return
	}
	slug := topicForName(k)
	t, ok := cachedTopic(slug)
	if slug == "" || !ok || slug == "kmem" {
		return
	}
	from := d.projectOf(id)
	to := d.org.Projects[slug]
	if to == "" {
		to = firstNonEmpty(t.Title, topicTitle(slug))
	}
	if from == to || len(d.org.Dirs[from]) == 0 {
		return
	}
	if d.org.Dirs == nil {
		d.org.Dirs = map[string][]string{}
	}
	for _, dir := range d.org.Dirs[from] {
		if !slices.Contains(d.org.Dirs[to], dir) {
			d.org.Dirs[to] = append(d.org.Dirs[to], dir)
		}
	}
}
