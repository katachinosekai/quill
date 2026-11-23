package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/cases"
	xlang "golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// Autocorrect follows the system language (user 2026-11-23): the hunspell
// dictionary is the one for LC_ALL / LC_MESSAGES / LANG when it is installed,
// and autocorrect is off by default when it is not, so Quill Desktop never
// rewrites Spanish or German words into English look-alikes. An unset or "C"
// locale and English locales use en_US, as before.

// acDict is a hunspell dictionary found on disk.
type acDict struct {
	Name string // e.g. "de_DE"
	Path string // hunspell -d argument: the path without .dic/.aff
}

// localeName returns the first usable locale of LC_ALL, LC_MESSAGES, LANG
// (POSIX order) without encoding or modifier ("de_DE.UTF-8@euro" -> "de_DE").
// "C" and "POSIX" count as unset.
func localeName(env func(string) string) string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := strings.TrimSpace(env(k))
		if i := strings.IndexAny(v, ".@"); i >= 0 {
			v = v[:i]
		}
		if v == "" || v == "C" || v == "POSIX" {
			continue
		}
		return v
	}
	return ""
}

// localeLang splits "de_AT" into language "de" and region "AT" (lower and
// upper case). The separator may also be "-".
func localeLang(loc string) (lang, region string) {
	loc = strings.ReplaceAll(loc, "-", "_")
	l, r, _ := strings.Cut(loc, "_")
	return strings.ToLower(l), strings.ToUpper(r)
}

// localeTag is the xlang.Tag of a locale; xlang.Und when it has none.
func localeTag(loc string) xlang.Tag {
	lang, region := localeLang(loc)
	if lang == "" {
		return xlang.Und
	}
	s := lang
	if region != "" {
		s += "-" + region
	}
	if t, err := xlang.Parse(s); err == nil {
		return t
	}
	if t, err := xlang.Parse(lang); err == nil {
		return t
	}
	return xlang.Und
}

// acDictDirs are the folders hunspell reads dictionaries from.
func acDictDirs(env func(string) string) []string {
	var dirs []string
	for _, d := range filepath.SplitList(env("DICPATH")) {
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	if x := env("XDG_DATA_HOME"); x != "" {
		dirs = append(dirs, filepath.Join(x, "hunspell"))
	} else if h := env("HOME"); h != "" {
		dirs = append(dirs, filepath.Join(h, ".local", "share", "hunspell"))
	}
	return append(dirs, "/usr/share/hunspell", "/usr/share/myspell", "/usr/share/myspell/dicts",
		"/usr/local/share/hunspell", "/usr/local/share/myspell")
}

func findDict(dirs []string, name string) (acDict, bool) {
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if isRegularFile(p+".dic") && isRegularFile(p+".aff") {
			return acDict{Name: name, Path: p}, true
		}
	}
	return acDict{}, false
}

func isRegularFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// installedWithPrefix lists dictionary names "<lang>_*" found in dirs, sorted.
func installedWithPrefix(dirs []string, lang string) []string {
	seen := map[string]bool{}
	for _, d := range dirs {
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			n, ok := strings.CutSuffix(e.Name(), ".dic")
			if ok && strings.HasPrefix(n, lang+"_") && !seen[n] {
				if _, found := findDict(dirs, n); found {
					seen[n] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// pickDict chooses the dictionary for a locale: the exact name, then
// <lang>_<LANG> (de_AT -> de_DE), then <lang>, then any <lang>_* installed.
// English locales also fall back to en_US, and an empty locale is en_US.
func pickDict(loc string, dirs []string) (acDict, bool) {
	lang, region := localeLang(loc)
	if lang == "" {
		lang = "en"
	}
	var try []string
	if region != "" {
		try = append(try, lang+"_"+region)
	}
	try = append(try, lang+"_"+strings.ToUpper(lang), lang)
	for _, n := range try {
		if d, ok := findDict(dirs, n); ok {
			return d, true
		}
	}
	for _, n := range installedWithPrefix(dirs, lang) {
		if d, ok := findDict(dirs, n); ok {
			return d, true
		}
	}
	if lang == "en" {
		return findDict(dirs, "en_US")
	}
	return acDict{}, false
}

// Values of the autocorrectDict preference besides a dictionary name.
const (
	prefACDict   = "autocorrectDict"
	acDictSystem = "" // follow the system language (default)
	acDictOff    = "off"
)

// acSystem is the language autocorrect and auto-capitalisation use.
type acSystem struct {
	Locale  string // "de_DE", "" when unset
	Tag     xlang.Tag
	Dict    acDict
	HasDic  bool
	Off     bool   // the user picked "Off"
	Pick    bool   // the dictionary is the user's pick, not the system rule
	Missing string // the saved pick that is no longer installed
}

// label names the system language for the user: the locale, or "en_US" when unset.
func (s acSystem) label() string {
	if s.Locale == "" {
		return "en_US"
	}
	return s.Locale
}

// resolveSystem applies the saved pick (pref: "", "off" or a dictionary name)
// over the system-language rule. A pick that is no longer installed falls back
// to the system rule and is reported in Missing.
func resolveSystem(env func(string) string, pref string) acSystem {
	loc := localeName(env)
	dirs := acDictDirs(env)
	s := acSystem{Locale: loc, Tag: localeTag(loc)}
	switch pref {
	case acDictSystem:
	case acDictOff:
		s.Off = true
		return s
	default:
		if d, ok := findDict(dirs, pref); ok {
			s.Dict, s.HasDic, s.Pick = d, true, true
			return s
		}
		s.Missing = pref
	}
	s.Dict, s.HasDic = pickDict(loc, dirs)
	return s
}

var (
	sysMu    sync.Mutex
	sysCache = map[string]acSystem{}
)

// acSystemFor resolves once per pref per run (the dictionaries on disk are
// not rescanned on every keystroke).
func acSystemFor(pref string) acSystem {
	sysMu.Lock()
	defer sysMu.Unlock()
	s, ok := sysCache[pref]
	if !ok {
		s = resolveSystem(os.Getenv, pref)
		sysCache[pref] = s
	}
	return s
}

// acSystemLang is the system-language rule alone, for casing.
func acSystemLang() acSystem { return acSystemFor(acDictSystem) }

// autocorrectEnabled lets an explicit user choice win over the default, which
// is on exactly when a dictionary is in use. Picking "Off" always wins.
func autocorrectEnabled(explicit, on bool, s acSystem) bool {
	switch {
	case s.Off:
		return false
	case explicit:
		return on
	}
	return s.HasDic
}

// autocorrectStatus is the line Settings shows for the language in use.
func autocorrectStatus(s acSystem) string {
	var line string
	switch {
	case s.Off:
		return "Autocorrect language: off"
	case s.HasDic:
		line = "Autocorrect language: " + s.Dict.Name
	default:
		line = "Autocorrect is off: no dictionary for " + s.label()
	}
	if s.Missing != "" {
		return "The saved dictionary " + s.Missing + " is not installed, so Quill Desktop follows the system language. " + line
	}
	return line
}

// listDicts finds every installed dictionary (both .dic and .aff), sorted by name.
func listDicts(dirs []string) []acDict {
	seen := map[string]bool{}
	var out []acDict
	for _, d := range dirs {
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			n, ok := strings.CutSuffix(e.Name(), ".dic")
			if !ok || seen[n] {
				continue
			}
			if dict, found := findDict(dirs, n); found {
				seen[n] = true
				out = append(out, dict)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// dictLabel is "German — Deutsch (de_DE)", or "English (en_US)" when the
// English and native names agree, or just the name when the language is unknown.
func dictLabel(name string) string {
	t := localeTag(name)
	if t == xlang.Und {
		return name
	}
	lang, _ := t.Base()
	base := xlang.Make(lang.String())
	en, self := display.English.Languages().Name(base), display.Self.Name(base)
	switch {
	case en == "" || strings.HasPrefix(en, "Unknown"):
		return name
	case self == "" || self == en:
		return en + " (" + name + ")"
	}
	return en + " — " + self + " (" + name + ")"
}

// acOption is one entry of the language picker; Value goes into the preference.
type acOption struct{ Label, Value string }

// acOptions: "System language (<detected>)", every installed dictionary, "Off".
func acOptions(sys acSystem, dicts []acDict) []acOption {
	detected := sys.label()
	if sys.HasDic && !sys.Pick {
		detected = sys.Dict.Name
	}
	opts := []acOption{{"System language (" + detected + ")", acDictSystem}}
	for _, d := range dicts {
		opts = append(opts, acOption{dictLabel(d.Name), d.Name})
	}
	return append(opts, acOption{"Off", acDictOff})
}

// localeUpper capitalises r by the casing rules of tag (Turkish i -> İ). It
// falls back to unicode.ToUpper when the result is not a single rune, so the
// cursor arithmetic of the caller stays valid.
func localeUpper(r rune, tag xlang.Tag) rune {
	out := []rune(cases.Upper(tag).String(string(r)))
	if len(out) == 1 {
		return out[0]
	}
	return unicode.ToUpper(r)
}
