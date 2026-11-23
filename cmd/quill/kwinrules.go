package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// KDE window-position rules (user 2026-11-23, kmem decision fa7e8c7).
// Wayland apps cannot set their own position, so on KDE Plasma KWin rules
// are how a window's position (and, redundantly, its size) is remembered.
// Quill Desktop asks once whether to install them (kwinask.go); Settings →
// General switches them later. Only the rules with our IDs (and the old
// "quill-remember-place") are ever touched; every other rule in kwinrulesrc
// stays as it is. Nothing is written outside KDE Plasma.

const (
	kwinRuleMain   = "quill-main"
	kwinRuleKmem   = "quill-kmem"
	kwinRuleLegacy = "quill-remember-place"

	kwinRemember  = "4" // KWin rule value: Remember
	kwinMaxBackup = 3
)

type kwinRule struct {
	ID, Desc, Class string
}

var kwinRulesOurs = []kwinRule{
	{kwinRuleMain, "Quill Desktop: remember position and size", appID},
	{kwinRuleKmem, "Quill Desktop kmem window: remember position and size", appIDKmem},
}

// kwinRules writes and removes the rules. The fields are hooks: tests give a
// temp ConfigDir and a fake Run, never the real KWin.
type kwinRules struct {
	Desktop   string                                  // XDG_CURRENT_DESKTOP
	ConfigDir string                                  // holds kwinrulesrc
	Run       func(name string, args ...string) error // runs a program
	Has       func(name string) bool                  // is the program installed
	Now       func() time.Time

	backedUp bool // the backup is made once per run, before the first write
}

// newKwinRules is the real thing: the user's config dir and programs.
func newKwinRules() *kwinRules {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		if h, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(h, ".config")
		}
	}
	return &kwinRules{
		Desktop:   os.Getenv("XDG_CURRENT_DESKTOP"),
		ConfigDir: dir,
		Run:       func(name string, args ...string) error { return exec.Command(name, args...).Run() },
		Has:       func(name string) bool { _, err := exec.LookPath(name); return err == nil },
		Now:       time.Now,
	}
}

// onKDE: KDE Plasma, Wayland or X11 (XDG_CURRENT_DESKTOP lists KDE).
func (k *kwinRules) onKDE() bool {
	if k == nil || k.ConfigDir == "" {
		return false
	}
	for _, p := range strings.Split(k.Desktop, ":") {
		if strings.EqualFold(strings.TrimSpace(p), "KDE") {
			return true
		}
	}
	return false
}

func (k *kwinRules) path() string { return filepath.Join(k.ConfigDir, "kwinrulesrc") }

// Apply makes the rules match on: installed (and the legacy rule removed)
// or removed. It does nothing when the file already matches (no backup, no
// reload) and never on a non-KDE desktop.
func (k *kwinRules) Apply(on bool) error {
	if !k.onKDE() {
		return nil
	}
	data, err := os.ReadFile(k.path())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	ini := parseINI(string(data))
	list := splitList(ini.get("General", "rules"))
	wantList, ops := k.plan(ini, list, on)
	if len(ops) == 0 && sameList(list, wantList) && (!on || countOK(ini, wantList)) {
		return nil
	}
	if err := k.backup(); err != nil {
		return err
	}
	ops = append(ops, kwinOp{"General", "rules", strings.Join(wantList, ","), len(wantList) > 0},
		kwinOp{"General", "count", strconv.Itoa(len(wantList)), true})
	if err := k.write(ops); err != nil {
		return err
	}
	k.reload()
	return nil
}

func countOK(ini *iniFile, list []string) bool {
	return ini.get("General", "count") == strconv.Itoa(len(list))
}

func sameList(a, b []string) bool {
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

type kwinOp struct {
	Group, Key, Val string
	Set             bool // false: delete the key
}

// plan returns the new [General] rules list and the group changes. Other
// rules keep their place in the list; ours are appended when missing.
func (k *kwinRules) plan(ini *iniFile, list []string, on bool) ([]string, []kwinOp) {
	ours := map[string]bool{kwinRuleMain: true, kwinRuleKmem: true}
	var out []string
	var ops []kwinOp
	have := map[string]bool{}
	for _, id := range list {
		switch {
		case id == kwinRuleLegacy:
		case ours[id]:
			if on && !have[id] {
				out = append(out, id)
			}
		default:
			out = append(out, id)
		}
		have[id] = true
	}
	for _, id := range []string{kwinRuleLegacy, kwinRuleMain, kwinRuleKmem} {
		if on && ours[id] {
			continue
		}
		for _, key := range ini.keys(id) { // a group with no keys left is gone
			ops = append(ops, kwinOp{id, key, "", false})
		}
	}
	if !on {
		return out, ops
	}
	for _, r := range kwinRulesOurs {
		if !contains(out, r.ID) {
			out = append(out, r.ID)
		}
		want := [][2]string{
			{"Description", r.Desc}, {"positionrule", kwinRemember}, {"sizerule", kwinRemember},
			{"wmclass", r.Class}, {"wmclassmatch", "1"},
		}
		for _, kv := range want {
			if ini.get(r.ID, kv[0]) != kv[1] {
				ops = append(ops, kwinOp{r.ID, kv[0], kv[1], true})
			}
		}
	}
	return out, ops
}

// write applies the changes: kwriteconfig6 (else 5) when installed, else the
// file is edited directly with all other content kept.
func (k *kwinRules) write(ops []kwinOp) error {
	prog := ""
	for _, p := range []string{"kwriteconfig6", "kwriteconfig5"} {
		if k.Has(p) {
			prog = p
			break
		}
	}
	if prog == "" {
		b, err := os.ReadFile(k.path())
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		ini := parseINI(string(b))
		for _, o := range ops {
			if o.Set {
				ini.set(o.Group, o.Key, o.Val)
			} else {
				ini.del(o.Group, o.Key)
			}
		}
		if err := os.MkdirAll(k.ConfigDir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(k.path(), []byte(ini.String()), 0o644)
	}
	for _, o := range ops {
		args := []string{"--file", k.path(), "--group", o.Group, "--key", o.Key}
		if o.Set {
			args = append(args, o.Val)
		} else {
			args = append(args, "--delete")
		}
		if err := k.Run(prog, args...); err != nil {
			return err
		}
	}
	return nil
}

// backup copies kwinrulesrc to kwinrulesrc.quill-backup-<time> before the
// first write of a run and keeps the newest three.
func (k *kwinRules) backup() error {
	if k.backedUp {
		return nil
	}
	b, err := os.ReadFile(k.path())
	if errors.Is(err, os.ErrNotExist) {
		k.backedUp = true
		return nil
	}
	if err != nil {
		return err
	}
	name := k.path() + ".quill-backup-" + k.Now().Format("20060102-150405")
	if err := os.WriteFile(name, b, 0o600); err != nil {
		return err
	}
	k.backedUp = true
	old, _ := filepath.Glob(k.path() + ".quill-backup-*")
	sort.Strings(old)
	for len(old) > kwinMaxBackup {
		os.Remove(old[0])
		old = old[1:]
	}
	return nil
}

// reload asks KWin to read the rules again; the first way that works wins.
func (k *kwinRules) reload() {
	for _, c := range [][]string{
		{"qdbus6", "org.kde.KWin", "/KWin", "reconfigure"},
		{"qdbus", "org.kde.KWin", "/KWin", "reconfigure"},
		{"dbus-send", "--session", "--type=method_call", "--dest=org.kde.KWin", "/KWin", "org.kde.KWin.reconfigure"},
	} {
		if k.Has(c[0]) && k.Run(c[0], c[1:]...) == nil {
			return
		}
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// iniFile is a minimal order- and content-preserving INI model: every line
// not touched is written back as it was.
type iniFile struct{ lines []string }

func parseINI(s string) *iniFile {
	if s == "" {
		return &iniFile{}
	}
	return &iniFile{lines: strings.Split(strings.TrimSuffix(s, "\n"), "\n")}
}

func (f *iniFile) String() string {
	if len(f.lines) == 0 {
		return ""
	}
	return strings.Join(f.lines, "\n") + "\n"
}

func groupName(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
		return t[1 : len(t)-1], true
	}
	return "", false
}

// span: the line range [start, end) of group g's body (after its header).
func (f *iniFile) span(g string) (int, int, bool) {
	start := -1
	for i, l := range f.lines {
		if n, ok := groupName(l); ok {
			if start >= 0 {
				return start, i, true
			}
			if n == g {
				start = i + 1
			}
		}
	}
	if start >= 0 {
		return start, len(f.lines), true
	}
	return 0, 0, false
}

func keyOf(line string) (string, string, bool) {
	t := strings.TrimSpace(line)
	if t == "" || t[0] == '#' || t[0] == ';' {
		return "", "", false
	}
	k, v, ok := strings.Cut(t, "=")
	return strings.TrimSpace(k), strings.TrimSpace(v), ok
}

func (f *iniFile) get(g, key string) string {
	s, e, ok := f.span(g)
	if !ok {
		return ""
	}
	for _, l := range f.lines[s:e] {
		if k, v, ok := keyOf(l); ok && k == key {
			return v
		}
	}
	return ""
}

func (f *iniFile) keys(g string) []string {
	s, e, ok := f.span(g)
	if !ok {
		return nil
	}
	var out []string
	for _, l := range f.lines[s:e] {
		if k, _, ok := keyOf(l); ok {
			out = append(out, k)
		}
	}
	return out
}

func (f *iniFile) set(g, key, val string) {
	s, e, ok := f.span(g)
	if !ok {
		if len(f.lines) > 0 && strings.TrimSpace(f.lines[len(f.lines)-1]) != "" {
			f.lines = append(f.lines, "")
		}
		f.lines = append(f.lines, "["+g+"]", key+"="+val)
		return
	}
	for i := s; i < e; i++ {
		if k, _, ok := keyOf(f.lines[i]); ok && k == key {
			f.lines[i] = key + "=" + val
			return
		}
	}
	for e > s && strings.TrimSpace(f.lines[e-1]) == "" {
		e-- // before the group's trailing blank lines
	}
	f.lines = append(f.lines[:e], append([]string{key + "=" + val}, f.lines[e:]...)...)
}

// del removes a key; a group left with no keys is removed with its header
// (as KConfig does).
func (f *iniFile) del(g, key string) {
	s, e, ok := f.span(g)
	if !ok {
		return
	}
	for i := s; i < e; i++ {
		if k, _, ok := keyOf(f.lines[i]); ok && k == key {
			f.lines = append(f.lines[:i], f.lines[i+1:]...)
			e--
			break
		}
	}
	if len(f.keys(g)) == 0 {
		hs := s - 1
		for e > s && strings.TrimSpace(f.lines[e-1]) == "" {
			e--
		}
		end := e
		for end < len(f.lines) && strings.TrimSpace(f.lines[end]) == "" {
			end++
		}
		f.lines = append(f.lines[:hs], f.lines[end:]...)
	}
}
