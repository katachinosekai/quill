package main

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// Placeholder session ids and names (no real data).
const (
	migLive     = "00000000-0000-4000-8000-000000000001" // topic tab, live
	migOldName  = "00000000-0000-4000-8000-000000000002" // old-name tab, not merged yet
	migEnded    = "00000000-0000-4000-8000-000000000003" // ended, still a tab
	migArchived = "00000000-0000-4000-8000-000000000004" // archived, handoff saved
	migFolder   = "00000000-0000-4000-8000-000000000005" // in a custom folder
	migAuto     = "00000000-0000-4000-8000-000000000006" // Created by Claude
	migHidden   = "00000000-0000-4000-8000-000000000007" // kmem window assistant
)

const migOrgJSON = `{
 "place": {"` + migLive + `": "active", "` + migOldName + `": "active", "` + migEnded + `": "inactive",
           "` + migArchived + `": "archive", "` + migFolder + `": "f:17", "` + migAuto + `": "auto"},
 "titles": {"` + migLive + `": "Alpha", "` + migOldName + `": "Alpha Worker", "` + migArchived + `": "Old experiment", "` + migFolder + `": "Notes"},
 "names": {"inactive": "Paused", "archive:` + "/tmp/proj" + `": "Old proj", "f:17": "My folder", "p:Alpha": "Alpha project"},
 "folders": [{"id": "17", "name": "My folder"}],
 "rc": {"` + migLive + `": {"name": "Alpha", "auto": true}},
 "kmem": {"` + migLive + `": "alpha", "` + migOldName + `": "alpha-worker", "` + migEnded + `": "beta-helper", "` + migArchived + `": "old-exp", "` + migHidden + `": "kmem-assistant"},
 "ended": {"` + migEnded + `": true, "` + migArchived + `": true},
 "hidden": {"` + migHidden + `": true},
 "projects": {"alpha-worker": "Alpha", "old-exp": "Lab"},
 "dirs": {"Alpha": ["/tmp/alpha-extra"], "Lab": ["/tmp/lab"]}
}`

// migHarness: a desk whose preferences hold the old org, tabs and sorting
// state (the app is a temp app, never Quill Desktop's own preferences).
func migHarness(t *testing.T) (*harness, sideOrg) {
	h := newHarness(t)
	tempBackups(t)
	prefs := h.d.app.Preferences()
	tabs := `["` + migLive + `","` + migOldName + `","` + migEnded + `"]`
	h.ui(func() {
		prefs.SetString("sidebarOrg", migOrgJSON)
		prefs.SetString("tabs", tabs)
		prefs.SetString("sortState", `{"place":{"`+migArchived+`":"archive"},"at":1,"active":["`+migLive+`"]}`)
		prefs.SetString("folderOpen", `{"p:Alpha":true}`)
		h.d.loadOrg()
		h.d.tabs = []string{migLive, migOldName, migEnded}
	})
	var before sideOrg
	if err := json.Unmarshal([]byte(migOrgJSON), &before); err != nil {
		t.Fatal(err)
	}
	return h, before
}

// tempBackups keeps the backup files in the test's temp folder (a test app's
// storage folder is the shared /tmp).
func tempBackups(t *testing.T) {
	dir := filepath.Join(t.TempDir(), orgBackupDir)
	old := orgBackupBase
	orgBackupBase = func(*desk) (string, error) { return dir, nil }
	t.Cleanup(func() { orgBackupBase = old })
}

func backupsIn(t *testing.T, h *harness) []string {
	dir, err := h.d.orgBackupRoot()
	if err != nil {
		t.Fatal(err)
	}
	m, _ := filepath.Glob(filepath.Join(dir, "*"))
	return m
}

func TestTopicMigrationRealisticOrg(t *testing.T) {
	h, before := migHarness(t)
	prefs := h.d.app.Preferences()
	origOrg, origTabs := prefs.String("sidebarOrg"), prefs.String("tabs")
	at := time.Date(2026, 11, 23, 15, 4, 5, 0, time.Local)
	var path string
	var older int
	h.ui(func() {
		var err error
		path, older, err = h.d.migrateTopics(at)
		if err != nil {
			t.Fatal(err)
		}
	})
	// The backup: timestamped, in the storage folder, the old values verbatim.
	if filepath.Base(path) != "sidebar-org-pre-topics-20261123-150405.json" || filepath.Base(filepath.Dir(path)) != orgBackupDir {
		t.Fatalf("backup path: %s", path)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("backup file: %v %v", err, st)
	}
	var b orgBackup
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	if b.SidebarOrg != origOrg || b.Tabs != origTabs || !strings.Contains(b.SortState, migArchived) || b.FolderOpen != `{"p:Alpha":true}` {
		t.Fatalf("backup content: %+v", b)
	}
	if prefs.String(orgPreTopicsKey) != origOrg || prefs.String(tabsPreTopicsKey) != origTabs {
		t.Fatal("design §3.12 preference copies")
	}
	if older != 4 {
		t.Fatalf("older conversations with a place: %d", older)
	}
	h.ui(func() {
		o := h.d.org
		if len(o.Place) != 0 || o.Folders != nil {
			t.Fatalf("places and folders dropped: %v %v", o.Place, o.Folders)
		}
		if !reflect.DeepEqual(o.Names, map[string]string{"p:Alpha": "Alpha project"}) {
			t.Fatalf("only place and folder names dropped: %v", o.Names)
		}
		// Nothing about sessions is lost.
		for what, pair := range map[string][2]any{
			"titles":   {o.Titles, before.Titles},
			"kmem":     {o.Kmem, before.Kmem},
			"ended":    {o.Ended, before.Ended},
			"hidden":   {o.Hidden, before.Hidden},
			"rc":       {o.RC, before.RC},
			"projects": {o.Projects, before.Projects},
			"dirs":     {o.Dirs, before.Dirs},
		} {
			if !reflect.DeepEqual(pair[0], pair[1]) {
				t.Fatalf("%s changed: %v -> %v", what, pair[1], pair[0])
			}
		}
		if !slices.Equal(h.d.tabs, []string{migLive, migOldName}) {
			t.Fatalf("only the ended session's tab closes: %v", h.d.tabs)
		}
		if !h.d.placesRemoved() || prefs.String("sortState") != "" {
			t.Fatal("marker set, sorting state dropped")
		}
		// Saved: a fresh load (Quill Desktop restarted) reads the same.
		saved := h.d.org
		h.d.loadOrg()
		if len(h.d.org.Place) != 0 || !maps.Equal(h.d.org.Kmem, saved.Kmem) || !strings.Contains(prefs.String("tabs"), migOldName) || strings.Contains(prefs.String("tabs"), migEnded) {
			t.Fatal("migrated org and tabs saved")
		}
	})
	if n := topicMigrationNotice(4, path); !strings.Contains(n, "Sessions are now topic tabs") || !strings.Contains(n, "4 older conversation(s)") {
		t.Fatalf("notice: %s", n)
	}
}

func TestTopicMigrationIdempotentAndNoPlacesAfter(t *testing.T) {
	h, _ := migHarness(t)
	h.ui(func() {
		if _, _, err := h.d.migrateTopics(time.Now()); err != nil {
			t.Fatal(err)
		}
	})
	first := backupsIn(t, h)
	prefs := h.d.app.Preferences()
	org, tabs := prefs.String("sidebarOrg"), prefs.String("tabs")
	h.ui(func() {
		h.d.loadOrg() // a restart
		path, _, err := h.d.migrateTopics(time.Now().Add(time.Hour))
		if path != "" || err != nil {
			t.Fatalf("second run does nothing: %q %v", path, err)
		}
		// No place is written any more.
		h.d.setPlacement(migOldName, placeInactive)
		h.d.putPlace(migLive, placeArchive)
		if len(h.d.org.Place) != 0 || h.d.endPlace() != "" || h.d.movedTo(placeInactive) != "" {
			t.Fatalf("no places after the migration: %v", h.d.org.Place)
		}
	})
	if len(first) != 1 || !slices.Equal(backupsIn(t, h), first) {
		t.Fatalf("one backup only: %v", backupsIn(t, h))
	}
	if prefs.String("tabs") != tabs || !strings.Contains(prefs.String("sidebarOrg"), `"kmem"`) || prefs.String("sidebarOrg") != org {
		t.Fatal("second run changed nothing")
	}
}

func TestTopicMigrationBackupFailureChangesNothing(t *testing.T) {
	h, _ := migHarness(t)
	dir, err := h.d.orgBackupRoot()
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(dir), 0o700)
	if err := os.WriteFile(dir, []byte("in the way"), 0o600); err != nil { // backups/ cannot be made
		t.Fatal(err)
	}
	h.ui(func() {
		if _, _, err := h.d.migrateTopics(time.Now()); err == nil {
			t.Fatal("the backup must fail")
		}
		if h.d.placesRemoved() || len(h.d.org.Place) != 6 || len(h.d.tabs) != 3 || h.d.app.Preferences().String("sortState") == "" {
			t.Fatal("nothing migrated without a backup")
		}
		if h.d.endPlace() != placeInactive {
			t.Fatal("places still written before the migration")
		}
	})
}

func TestTopicMigrationNeedsKmemModeAndEndCheck(t *testing.T) {
	h, _ := migHarness(t)
	prefs := h.d.app.Preferences()
	prefs.SetString(memoryModeKey, memoryModeKmem)
	k := endCheckKmem(t, h, false) // installed kmem: no features
	h.d.migrateTopicsWhenReady()
	h.ui(func() {
		if h.d.placesRemoved() || len(h.d.org.Place) != 6 || len(backupsIn(t, h)) != 0 {
			t.Fatal("no end-check feature: places stay, no migration")
		}
	})
	// Claude-memory mode never migrates, even with the feature.
	k.mu.Lock()
	k.features = true
	k.mu.Unlock()
	h.d.kmemVer.reset()
	prefs.SetString(memoryModeKey, memoryModeClaude)
	h.d.migrateTopicsWhenReady()
	h.ui(func() {
		if h.d.placesRemoved() || len(backupsIn(t, h)) != 0 {
			t.Fatal("Claude-memory mode: no migration")
		}
	})
	prefs.SetString(memoryModeKey, memoryModeKmem)
	h.d.migrateTopicsWhenReady()
	h.ui(func() {
		if !h.d.placesRemoved() || len(h.d.org.Place) != 0 || len(backupsIn(t, h)) != 1 {
			t.Fatal("kmem mode with the end-check: migrated")
		}
	})
}

func TestTopicRestartWording(t *testing.T) {
	txt := restartTopicText("Alpha", "Alpha")
	if strings.Contains(txt, "handoff") || !strings.Contains(txt, "end-of-session check") || !strings.Contains(txt, "fresh Alpha session") {
		t.Fatalf("restart text: %s", txt)
	}
}

func TestTopicQuitAndResumeAll(t *testing.T) {
	h := newHarness(t)
	tempBackups(t)
	h.d.app.Preferences().SetString(memoryModeKey, memoryModeKmem)
	k := endCheckKmem(t, h, true)
	k.set("alpha", ecOK)
	h.ui(func() {
		if _, _, err := h.d.migrateTopics(time.Now()); err != nil {
			t.Fatal(err)
		}
	})
	a := topicSession(h, "alpha")
	old := topicSession(h, "alpha-worker") // old name: not reopened
	h.ui(func() {
		h.d.addTab(a.id)
		h.d.addTab(old.id)
		h.d.saveOrg() // the kmem links as the topic card saves them
		h.d.rememberTopicsAtQuit()
		if got := h.d.app.Preferences().StringList(topicsAtQuitKey); !slices.Equal(got, []string{"alpha"}) {
			t.Fatalf("topics live at quit: %v", got)
		}
	})
	// Quit stops everything; the restart restores the tabs, stopped.
	var running []*deskSession
	h.ui(func() { running = h.d.runningSessions() })
	h.d.stopAll(running)
	h.eventually("stopped", func() bool { return !a.running() && !old.running() })
	var fresh []*deskSession
	h.ui(func() {
		h.d.loadOrg()
		fresh = h.d.reopenTopicsAtQuit()
	})
	if len(fresh) != 1 || fresh[0] == a || fresh[0].id == a.id {
		t.Fatalf("one fresh alpha session: %v", fresh)
	}
	h.eventually("fresh alpha session", func() bool { return h.d.org.Kmem[fresh[0].id] == "alpha" })
	h.ui(func() {
		if slices.Contains(h.d.tabs, a.id) || !h.d.org.Ended[a.id] {
			t.Fatalf("the stopped alpha tab is retired, not resumed: tabs %v a %s ended %v", h.d.tabs, a.id, h.d.org.Ended)
		}
		if h.d.org.Kmem[a.id] != "alpha" {
			t.Fatal("its kmem link stays")
		}
		if !slices.Contains(h.d.tabs, old.id) {
			t.Fatal("the old-name tab stays until it merges")
		}
		if len(h.d.org.Place) != 0 {
			t.Fatalf("no places: %v", h.d.org.Place)
		}
		// A second --resume-all switches to the live topic, no duplicate.
		again := h.d.reopenTopicsAtQuit()
		if len(again) != 1 || again[0] != fresh[0] {
			t.Fatalf("one live session per topic: %d %v fresh %s", len(again), func() []string { var o []string; for _, s := range again { o = append(o, s.id) }; return o }(), fresh[0].id)
		}
	})
}

func TestOldNameEndCopiesDirsToTopic(t *testing.T) {
	h := newHarness(t)
	tempBackups(t)
	endCheckKmem(t, h, true)
	cfg := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(cfg, []byte(`{"sessions": {"alpha-worker": {"as": "alpha-worker", "topic": "alpha"}, "beta-helper": {"topic": "Beta helper work"}}}`), 0o600)
	oldCfg := kmemCfgPath
	kmemCfgPath = func() string { return cfg }
	resetKmemSessions()
	t.Cleanup(func() { kmemCfgPath = oldCfg; resetKmemSessions() })
	h.ui(func() {
		o := &h.d.org
		o.Kmem[migOldName] = "alpha-worker"
		o.Kmem[migEnded] = "beta-helper"
		o.Projects = map[string]string{"alpha-worker": "Lab", "beta-helper": "Helpers"}
		o.Dirs = map[string][]string{"Lab": {"/tmp/lab", "/tmp/shared"}, "Alpha": {"/tmp/shared"}, "Helpers": {"/tmp/helpers"}}
		// Before the migration nothing is carried.
		h.d.markEnded(migOldName)
		if len(o.Dirs["Alpha"]) != 1 {
			t.Fatalf("no copy before topic tabs: %v", o.Dirs)
		}
		delete(o.Ended, migOldName)
		if _, _, err := h.d.migrateTopics(time.Now()); err != nil {
			t.Fatal(err)
		}
		h.d.markEnded(migOldName)
		if !slices.Equal(o.Dirs["Alpha"], []string{"/tmp/shared", "/tmp/lab"}) {
			t.Fatalf("mapped old name: folders copied to its topic: %v", o.Dirs["Alpha"])
		}
		if !slices.Equal(o.Dirs["Lab"], []string{"/tmp/lab", "/tmp/shared"}) {
			t.Fatal("copied, never moved")
		}
		// A free-text topic is no mapping (no guessing from words).
		h.d.markEnded(migEnded)
		if len(o.Dirs) != 3 || len(o.Dirs["Helpers"]) != 1 {
			t.Fatalf("unmapped old name: nothing copied: %v", o.Dirs)
		}
	})
}
