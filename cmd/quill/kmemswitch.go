package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"

	"github.com/katachinosekai/quill/internal/transcript"
)

// Switching from Claude Code's auto memory to kmem (docs/design/kmem-switch.md,
// approved 2026-11-24): detect kmem, preview every change, back up, import
// each project's MEMORY.md into its own kmem topic, add the kmem hooks, turn
// Claude Code's auto memory off (autoMemoryEnabled: false, the setting Claude
// Code's /memory switch writes), and set the memory mode to kmem. Running
// sessions are not touched. Any failure restores the backup; Settings →
// Memory "Switch back to Claude memory" undoes a finished switch.

const (
	// kmemImportFeature: `kmem features --json` lists it from kmem 1.14.0.
	kmemImportFeature    = "import-memory-md"
	kmemImportMinVersion = "1.14.0"
	// prefKmemSwitchBackup: the backup folder of the last finished switch
	// (what Undo restores); "" when there is none.
	prefKmemSwitchBackup = "kmemSwitchBackup"
	kmemSwitchManifest   = "switch.json"
)

// kmemSwitchCheck: what detection found. Problems say what is missing and how
// to fix it; any problem means no switch.
type kmemSwitchCheck struct {
	Bin, Guard, Version, Python string
	Problems                    []string
}

// switchPython: the Python kmem runs on ("" when none).
var switchPython = func() string {
	if runtime.GOOS == "windows" {
		return windowsPython()
	}
	if p, err := exec.LookPath("python3"); err == nil {
		return p
	}
	return ""
}

// findKmemGuard: kmem-guard next to kmem, else on PATH, else in
// ~/.local/bin ("" if none).
func findKmemGuard(home, kmem string) string {
	cands := []string{}
	if filepath.IsAbs(kmem) {
		cands = append(cands, filepath.Join(filepath.Dir(kmem), "kmem-guard"))
	}
	if p, err := exec.LookPath("kmem-guard"); err == nil {
		cands = append(cands, p)
	}
	cands = append(cands, filepath.Join(home, ".local", "bin", "kmem-guard"))
	for _, p := range cands {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// kmemJSONOut: the JSON object in a kmem command's output (stderr may come
// first, runKmem mixes both).
func kmemJSONOut(out string, v any) error {
	out = strings.TrimSpace(out)
	if i := strings.IndexAny(out, "{["); i > 0 {
		out = out[i:]
	}
	return json.Unmarshal([]byte(out), v)
}

// kmemSwitchDetect (not on the UI thread): kmem and kmem-guard, kmem's
// version and its import feature, python3 and kmem's core rules.
func (d *desk) kmemSwitchDetect(home string) kmemSwitchCheck {
	var c kmemSwitchCheck
	if !d.kmemOff {
		c.Bin = kmemBin()
		if d.kmemRun != nil {
			c.Bin = firstNonEmpty(c.Bin, "kmem")
		}
	}
	if c.Bin == "" {
		c.Problems = append(c.Problems, "kmem is not installed (not on PATH or in ~/.local/bin). Install kmem first; it is available to Patreon patrons.")
		return c
	}
	c.Guard = findKmemGuard(home, c.Bin)
	if c.Guard == "" {
		c.Problems = append(c.Problems, "kmem-guard is missing (not on PATH or in ~/.local/bin). Reinstall kmem with its installer, which puts kmem-guard next to kmem.")
	}
	c.Python = switchPython()
	if c.Python == "" {
		c.Problems = append(c.Problems, "python3 is not installed; kmem runs on it. Install Python 3 and try again.")
	}
	var ver struct {
		Version  string   `json:"kmem"`
		API      int      `json:"api"`
		Features []string `json:"features"`
	}
	out, err := d.runKmemRaw("kmem", "version", "--json")
	if err != nil || kmemJSONOut(out, &ver) != nil || ver.API != kmemAPI {
		c.Problems = append(c.Problems, "kmem doesn't answer `kmem version --json` (it needs kmem with API 1). Reinstall or update kmem.")
		return c
	}
	c.Version = ver.Version
	if d.kmemRun == nil && !supportedKmemVersion(ver.API, ver.Version) {
		c.Problems = append(c.Problems, fmt.Sprintf("kmem %s is incompatible with this Quill build. Supported versions: %s.", ver.Version, supportedKmemSeries))
		return c
	}
	features := ver.Features
	if out, err := d.runKmemRaw("kmem", "features", "--json"); err == nil {
		var obj struct {
			Features []string `json:"features"`
		}
		var list []string
		if kmemJSONOut(out, &obj) == nil && obj.Features != nil {
			features = obj.Features
		} else if kmemJSONOut(out, &list) == nil {
			features = list
		}
	}
	if !contains(features, kmemImportFeature) {
		c.Problems = append(c.Problems, fmt.Sprintf("kmem %s is too old for the switch: it needs kmem %s or newer (import of MEMORY.md). Update kmem.", firstNonEmpty(ver.Version, "(unknown version)"), kmemImportMinVersion))
	}
	var rules struct {
		Rules struct {
			Rules []json.RawMessage `json:"rules"`
		} `json:"rules"`
	}
	if out, err := d.runKmemRaw("kmem", "rules", "--json"); err != nil || kmemJSONOut(out, &rules) != nil || len(rules.Rules.Rules) == 0 {
		c.Problems = append(c.Problems, "kmem's core rules are not installed (`kmem rules` lists none). Reinstall kmem: its installer puts the core rules in place.")
	}
	return c
}

// memoryProject: one project's MEMORY.md for the preview.
type memoryProject struct {
	Slug  string   // the folder under ~/.claude/projects
	Cwd   string   // the project folder ("" if unknown)
	File  string   // .../memory/MEMORY.md
	Notes []string // linked note files beside it (what kmem imports with it)
	Facts int      // linked notes plus plain bullets
	Topic string   // default target topic: the project folder's name
}

var mdLink = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
var mdBullet = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+\S`)

// memoryFacts: what kmem imports from a MEMORY.md (kmem 1.14.0 follows its
// links): one fact per linked note file that exists beside it, plus one per
// bullet that links no note.
func memoryFacts(file string) (notes []string, facts int) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, 0
	}
	dir := filepath.Dir(file)
	seen := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		linked := false
		for _, m := range mdLink.FindAllStringSubmatch(line, -1) {
			target := m[1]
			if i := strings.IndexByte(target, '#'); i >= 0 {
				target = target[:i]
			}
			if target == "" || strings.Contains(target, "://") || !strings.HasSuffix(strings.ToLower(target), ".md") {
				continue
			}
			p := filepath.Clean(filepath.Join(dir, filepath.FromSlash(target)))
			if rel, err := filepath.Rel(dir, p); err != nil || strings.HasPrefix(rel, "..") || filepath.Base(p) == "MEMORY.md" {
				continue
			}
			if st, err := os.Stat(p); err != nil || st.IsDir() {
				continue
			}
			linked = true
			if !seen[p] {
				seen[p] = true
				notes = append(notes, p)
			}
		}
		if !linked && mdBullet.MatchString(line) {
			facts++
		}
	}
	return notes, facts + len(notes)
}

// switchTopic: a typed topic made a kmem topic name ("" if nothing is left).
func switchTopic(s string) string {
	t := strings.Trim(slugJunk.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-")
	if len(t) > 40 {
		t = strings.TrimRight(t[:40], "-")
	}
	return t
}

// projectCwd: the project folder of a ~/.claude/projects folder, from its
// newest transcript ("" if none says).
func projectCwd(dir string) string {
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	sort.Slice(files, func(i, j int) bool {
		a, _ := os.Stat(files[i])
		b, _ := os.Stat(files[j])
		return a != nil && b != nil && a.ModTime().After(b.ModTime())
	})
	for _, f := range files {
		if info, err := transcript.ScanFile(f); err == nil && info.Cwd != "" {
			return info.Cwd
		}
	}
	return ""
}

// scanMemoryProjects: every project under root with a MEMORY.md that has
// something to import, sorted by topic.
func scanMemoryProjects(root string) []memoryProject {
	files, _ := filepath.Glob(filepath.Join(root, "*", "memory", "MEMORY.md"))
	var out []memoryProject
	for _, f := range files {
		notes, facts := memoryFacts(f)
		if facts == 0 {
			continue
		}
		dir := filepath.Dir(filepath.Dir(f))
		p := memoryProject{Slug: filepath.Base(dir), File: f, Notes: notes, Facts: facts, Cwd: projectCwd(dir)}
		if p.Cwd != "" {
			p.Topic = switchTopic(filepath.Base(p.Cwd))
		}
		if p.Topic == "" {
			// The slug lost the separators: its last part is the best guess.
			parts := strings.FieldsFunc(p.Slug, func(r rune) bool { return r == '-' })
			if len(parts) > 0 {
				p.Topic = switchTopic(parts[len(parts)-1])
			}
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Topic < out[j].Topic })
	return out
}

// kmemSwitchPlan: what "Switch" does.
type kmemSwitchPlan struct {
	Home       string
	Settings   string // claudeSettingsFile: $CLAUDE_CONFIG_DIR or ~/.claude
	PrefsFile  string // Quill Desktop's preferences file ("" if unknown)
	BackupRoot string // ~/.local/share/quill
	Hooks      map[string]map[string]any
	Projects   []memoryProject // the ticked ones, Topic as typed
	Now        time.Time
}

// kmemSwitchProject: one project's import result.
type kmemSwitchProject struct {
	File     string `json:"file"`
	Topic    string `json:"topic"`
	Imported int    `json:"imported"`
	Skipped  int    `json:"skipped"`
	// Missing / Refused: kmem 1.14.1's lists of linked notes it couldn't find
	// or wouldn't import (omitted by kmem when empty).
	Missing []string `json:"missing,omitempty"`
	Refused []string `json:"refused,omitempty"`
	Done    bool     `json:"done"`
}

// kmemListItems turns kmem's missing/refused entries into lines: strings as
// they are, anything else (an object with a reason) as compact JSON.
func kmemListItems(raw []json.RawMessage) []string {
	var out []string
	for _, r := range raw {
		var s string
		if json.Unmarshal(r, &s) == nil {
			out = append(out, s)
			continue
		}
		out = append(out, strings.TrimSpace(string(r)))
	}
	return out
}

// kmemSwitchRecord is switch.json in the backup folder: what the switch
// changed, so Undo reverts exactly that.
type kmemSwitchRecord struct {
	Created          time.Time           `json:"created"`
	State            string              `json:"state"` // backup, switched, rolled-back, undone
	Settings         string              `json:"settings"`
	SettingsExisted  bool                `json:"settingsExisted"`
	AutoMemoryBefore *bool               `json:"autoMemoryEnabledBefore"` // nil: not set (on)
	HooksAdded       []string            `json:"hooksAdded,omitempty"`
	EventsBefore     []string            `json:"eventsBefore,omitempty"`
	HadHooks         bool                `json:"hadHooks"`
	MemoryModeBefore string              `json:"memoryModeBefore"`
	OfferedBefore    bool                `json:"switchOfferedBefore"`
	Projects         []kmemSwitchProject `json:"projects"`
	Error            string              `json:"error,omitempty"`
}

// kmemSwitchResult: what the dialog shows afterwards.
type kmemSwitchResult struct {
	Backup   string
	Projects []kmemSwitchProject
	Err      error
	// RolledBack: the failure was undone from the backup (always on failure
	// after the backup exists; RestoreErr says what couldn't be restored).
	RolledBack bool
	RestoreErr error
}

// kmemImportError: kmem's failure JSON (exit 2).
type kmemImportError struct {
	Message string `json:"error"`
	Code    string `json:"code"`
}

func (e *kmemImportError) Error() string {
	if e.Code != "" {
		return e.Message + " (" + e.Code + ")"
	}
	return e.Message
}

// kmemImport runs `kmem import memory-md FILE --topic T --json`.
func (d *desk) kmemImport(file, topic string) (kmemSwitchProject, error) {
	r := kmemSwitchProject{File: file, Topic: topic}
	out, err := d.runKmemRaw("kmem", "import", "memory-md", file, "--topic", topic, "--json")
	var ok struct {
		Imported *int              `json:"imported"`
		Skipped  int               `json:"skipped"`
		Topic    string            `json:"topic"`
		Error    string            `json:"error"`
		Code     string            `json:"code"`
		Missing  []json.RawMessage `json:"missing"`
		Refused  []json.RawMessage `json:"refused"`
	}
	perr := kmemJSONOut(out, &ok)
	switch {
	case perr == nil && ok.Error != "":
		return r, fmt.Errorf("kmem could not import %s: %w", tildePath(file), &kmemImportError{ok.Error, ok.Code})
	case err != nil:
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = err.Error()
		}
		return r, fmt.Errorf("kmem could not import %s: %s", tildePath(file), msg)
	case perr != nil || ok.Imported == nil:
		return r, fmt.Errorf("kmem's answer to the import of %s is not the expected JSON: %q", tildePath(file), strings.TrimSpace(out))
	}
	r.Imported, r.Skipped, r.Done = *ok.Imported, ok.Skipped, true
	r.Missing, r.Refused = kmemListItems(ok.Missing), kmemListItems(ok.Refused)
	if ok.Topic != "" {
		r.Topic = ok.Topic
	}
	return r, nil
}

// kmemSwitchBackupDir: a new dated backup folder under root.
func kmemSwitchBackupDir(root string, now time.Time) (string, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	base := filepath.Join(root, "kmem-switch-backup-"+now.Format("20060102-1504"))
	dir := base
	for n := 2; ; n++ {
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
		dir = fmt.Sprintf("%s-%d", base, n)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func writeSwitchRecord(dir string, r *kmemSwitchRecord) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, kmemSwitchManifest+".tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, kmemSwitchManifest))
}

func readSwitchRecord(dir string) (*kmemSwitchRecord, error) {
	b, err := os.ReadFile(filepath.Join(dir, kmemSwitchManifest))
	if err != nil {
		return nil, err
	}
	var r kmemSwitchRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s is damaged: %w", tildePath(filepath.Join(dir, kmemSwitchManifest)), err)
	}
	return &r, nil
}

// settingsTarget: the real file behind settings.json (a symlinked dotfile).
func settingsTarget(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}

// backUpForSwitch writes the backup folder: settings.json, every listed
// MEMORY.md with its linked notes, the preferences file and switch.json.
func backUpForSwitch(dir string, plan kmemSwitchPlan, rec *kmemSwitchRecord) error {
	if _, err := os.Stat(settingsTarget(plan.Settings)); err == nil {
		rec.SettingsExisted = true
		if err := copyFile(settingsTarget(plan.Settings), filepath.Join(dir, "settings.json")); err != nil {
			return fmt.Errorf("backup of %s: %w", tildePath(plan.Settings), err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, p := range plan.Projects {
		dst := filepath.Join(dir, "memory", filepath.Base(filepath.Dir(filepath.Dir(p.File))))
		for _, f := range append([]string{p.File}, p.Notes...) {
			if err := copyFile(f, filepath.Join(dst, filepath.Base(f))); err != nil {
				return fmt.Errorf("backup of %s: %w", tildePath(f), err)
			}
		}
	}
	if plan.PrefsFile != "" {
		if _, err := os.Stat(plan.PrefsFile); err == nil {
			if err := copyFile(plan.PrefsFile, filepath.Join(dir, "preferences.json")); err != nil {
				return fmt.Errorf("backup of Quill Desktop's preferences: %w", err)
			}
		}
	}
	return writeSwitchRecord(dir, rec)
}

// restoreSettings puts the backed-up settings.json back (or removes the one
// the switch created).
func restoreSettings(dir string, rec *kmemSwitchRecord) error {
	target := settingsTarget(rec.Settings)
	if !rec.SettingsExisted {
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	tmp := target + ".quill-tmp"
	if err := copyFile(filepath.Join(dir, "settings.json"), tmp); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// kmemSwitchAfterSettings: tests make a step after the settings write fail.
var kmemSwitchAfterSettings func() error

// runKmemSwitch does the switch (not on the UI thread). prefs is changed
// last; any failure restores the backup.
func (d *desk) runKmemSwitch(plan kmemSwitchPlan, prefs fyne.Preferences) kmemSwitchResult {
	var res kmemSwitchResult
	dir, err := kmemSwitchBackupDir(plan.BackupRoot, plan.Now)
	if err != nil {
		res.Err = fmt.Errorf("could not create the backup folder: %w", err)
		return res
	}
	res.Backup = dir
	rec := &kmemSwitchRecord{Created: plan.Now, State: "backup", Settings: plan.Settings,
		MemoryModeBefore: prefs.String(memoryModeKey), OfferedBefore: prefs.Bool(prefKmemSwitchOffered)}
	if m, err := readSettings(settingsTarget(plan.Settings)); err == nil {
		if v, ok := m["autoMemoryEnabled"].(bool); ok {
			rec.AutoMemoryBefore = &v
		}
		if hooks, ok := m["hooks"].(map[string]any); ok {
			rec.HadHooks = true
			for e := range hooks {
				rec.EventsBefore = append(rec.EventsBefore, e)
			}
			sort.Strings(rec.EventsBefore)
		}
	}
	settingsTouched, prefsTouched := false, false
	fail := func(err error) kmemSwitchResult {
		res.Err = err
		res.RolledBack = true
		var errs []error
		if settingsTouched {
			if e := restoreSettings(dir, rec); e != nil {
				errs = append(errs, fmt.Errorf("%s: %w", tildePath(plan.Settings), e))
			}
		}
		if prefsTouched {
			prefs.SetString(memoryModeKey, rec.MemoryModeBefore)
			prefs.SetBool(prefKmemSwitchOffered, rec.OfferedBefore)
			prefs.RemoveValue(prefKmemSwitchBackup)
		}
		rec.State, rec.Error = "rolled-back", err.Error()
		if e := writeSwitchRecord(dir, rec); e != nil {
			errs = append(errs, e)
		}
		res.RestoreErr = errors.Join(errs...)
		return res
	}
	if err := backUpForSwitch(dir, plan, rec); err != nil {
		return fail(err)
	}
	for _, p := range plan.Projects {
		r, err := d.kmemImport(p.File, p.Topic)
		rec.Projects = append(rec.Projects, r)
		res.Projects = rec.Projects
		if err != nil {
			return fail(err)
		}
	}
	settingsTouched = true
	err = updateSettings(plan.Settings, func(m map[string]any) error {
		added, _, err := mergeKmemHookGroups(m, plan.Hooks)
		if err != nil {
			return fmt.Errorf("%s %w", tildePath(plan.Settings), err)
		}
		rec.HooksAdded = added
		m["autoMemoryEnabled"] = false
		return nil
	})
	if err != nil {
		return fail(fmt.Errorf("could not update %s: %w", tildePath(plan.Settings), err))
	}
	if kmemSwitchAfterSettings != nil {
		if err := kmemSwitchAfterSettings(); err != nil {
			return fail(err)
		}
	}
	rec.State = "switched"
	if err := writeSwitchRecord(dir, rec); err != nil {
		return fail(err)
	}
	prefsTouched = true
	prefs.SetString(memoryModeKey, memoryModeKmem)
	prefs.SetBool(prefKmemSwitchOffered, true)
	prefs.SetString(prefKmemSwitchBackup, dir)
	return res
}

// undoKmemSwitch reverts a finished switch: removes the hooks it added,
// restores autoMemoryEnabled, and sets the memory mode to Claude. The user's
// later settings changes stay; imported facts stay in kmem (marked).
func undoKmemSwitch(dir string, prefs fyne.Preferences) error {
	rec, err := readSwitchRecord(dir)
	if err != nil {
		return err
	}
	if rec.State != "switched" {
		return fmt.Errorf("the switch in %s is %s; nothing to undo", tildePath(dir), rec.State)
	}
	keep := map[string]bool{"hooks": rec.HadHooks}
	for _, e := range rec.EventsBefore {
		keep[e] = true
	}
	err = updateSettings(rec.Settings, func(m map[string]any) error {
		removeKmemHookGroups(m, rec.HooksAdded, keep)
		if rec.AutoMemoryBefore == nil {
			delete(m, "autoMemoryEnabled")
		} else {
			m["autoMemoryEnabled"] = *rec.AutoMemoryBefore
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("could not restore %s: %w", tildePath(rec.Settings), err)
	}
	prefs.SetString(memoryModeKey, memoryModeClaude)
	prefs.RemoveValue(prefKmemSwitchBackup)
	rec.State = "undone"
	return writeSwitchRecord(dir, rec)
}

// removeLeftKmemHooks: kmem was removed but its hooks are still in
// settings.json (the "Remove hooks" button).
func removeLeftKmemHooks(home string) error {
	return updateSettings(claudeSettingsFile(home), func(m map[string]any) error {
		removeKmemHookGroups(m, []string{"PreToolUse", "SessionStart"}, nil)
		return nil
	})
}

// kmemHooksAnyPresent: any kmem hook in settings.json.
func kmemHooksAnyPresent(home string) bool {
	m, err := readSettings(claudeSettingsFile(home))
	if err != nil {
		return false
	}
	hooks, _ := m["hooks"].(map[string]any)
	for _, e := range []string{"PreToolUse", "SessionStart"} {
		if groups, _ := hooks[e].([]any); hasHookCommand(groups, kmemHookNeedle(e)) {
			return true
		}
	}
	return false
}

// prefsFile: Quill Desktop's preferences file ("" if the app keeps none).
func (d *desk) prefsFile() string {
	if st := d.app.Storage(); st != nil && st.RootURI() != nil {
		return filepath.Join(st.RootURI().Path(), "preferences.json")
	}
	return ""
}

// kmemSwitchUndoDir: the backup of a finished switch Undo can revert ("").
func (d *desk) kmemSwitchUndoDir() string {
	dir := d.app.Preferences().String(prefKmemSwitchBackup)
	if dir == "" {
		return ""
	}
	if rec, err := readSwitchRecord(dir); err != nil || rec.State != "switched" {
		return ""
	}
	return dir
}
