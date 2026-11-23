package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xlang "golang.org/x/text/language"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func fakeDicts(t *testing.T, names ...string) []string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		for _, ext := range []string{".dic", ".aff"} {
			if err := os.WriteFile(filepath.Join(dir, n+ext), []byte("0\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return []string{dir}
}

func TestLocaleName(t *testing.T) {
	for _, c := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"LANG": "de_DE.UTF-8"}, "de_DE"},
		{map[string]string{"LANG": "sr_RS@latin"}, "sr_RS"},
		{map[string]string{"LANG": "en_US.UTF-8", "LC_ALL": "es_ES.UTF-8"}, "es_ES"},
		{map[string]string{"LANG": "en_US.UTF-8", "LC_MESSAGES": "fr_FR.UTF-8"}, "fr_FR"},
		{map[string]string{"LC_ALL": "C", "LANG": "tr_TR.UTF-8"}, "tr_TR"},
		{map[string]string{"LANG": "POSIX"}, ""},
		{map[string]string{}, ""},
	} {
		if got := localeName(envOf(c.env)); got != c.want {
			t.Errorf("%v: got %q want %q", c.env, got, c.want)
		}
	}
}

func TestPickDict(t *testing.T) {
	for _, c := range []struct {
		loc       string
		installed []string
		want      string // "" = none
	}{
		{"de_DE", []string{"de_DE", "en_US"}, "de_DE"},
		{"de_AT", []string{"de_AT", "de_DE"}, "de_AT"},
		{"de_AT", []string{"de_DE"}, "de_DE"},
		{"de_AT", []string{"de"}, "de"},
		{"de_CH", []string{"de_AT"}, "de_AT"},
		{"es_MX", []string{"es_ES"}, "es_ES"},
		{"de_DE", []string{"en_US"}, ""},
		{"es_ES", nil, ""},
		{"", []string{"en_US", "de_DE"}, "en_US"},
		{"", []string{"de_DE"}, ""},
		{"en_GB", []string{"en_US"}, "en_US"},
		{"en_GB", []string{"en_GB", "en_US"}, "en_GB"},
		{"tr_TR", []string{"en_US"}, ""},
	} {
		dirs := fakeDicts(t, c.installed...)
		d, ok := pickDict(c.loc, dirs)
		if c.want == "" {
			if ok {
				t.Errorf("%q with %v: got %q, want none", c.loc, c.installed, d.Name)
			}
			continue
		}
		if !ok || d.Name != c.want || d.Path != filepath.Join(dirs[0], c.want) {
			t.Errorf("%q with %v: got %+v ok=%v, want %s", c.loc, c.installed, d, ok, c.want)
		}
	}
}

func TestPickDictNeedsBothFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "de_DE.dic"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := pickDict("de_DE", []string{dir}); ok {
		t.Fatal("a .dic without .aff is not a dictionary")
	}
}

func TestResolveSystemAndDefault(t *testing.T) {
	dir := fakeDicts(t, "de_DE")[0]
	de := resolveSystem(envOf(map[string]string{"LANG": "de_AT.UTF-8", "DICPATH": dir}), "")
	if !de.HasDic || de.Dict.Name != "de_DE" {
		t.Fatalf("de_AT with de_DE installed: %+v", de)
	}
	if got := autocorrectStatus(de); got != "Autocorrect language: de_DE" {
		t.Fatal(got)
	}
	es := resolveSystem(envOf(map[string]string{"LANG": "es_ES.UTF-8", "DICPATH": dir, "HOME": t.TempDir()}), "")
	if es.HasDic && es.Dict.Name != "" && es.Dict.Path != "" && es.Dict.Name[:2] != "es" {
		t.Fatalf("unexpected dictionary %+v", es)
	}
	if !es.HasDic {
		if autocorrectEnabled(false, true, es) {
			t.Fatal("no dictionary: autocorrect must default to off")
		}
		if got := autocorrectStatus(es); got != "Autocorrect is off: no dictionary for es_ES" {
			t.Fatal(got)
		}
	}
}

func TestAutocorrectExplicitChoiceWins(t *testing.T) {
	with := acSystem{Locale: "de_DE", HasDic: true}
	without := acSystem{Locale: "de_DE"}
	for _, c := range []struct {
		explicit, on bool
		s            acSystem
		want         bool
	}{
		{false, true, with, true},
		{false, true, without, false},
		{true, false, with, false},
		{true, true, without, true},
		{true, true, with, true},
	} {
		if got := autocorrectEnabled(c.explicit, c.on, c.s); got != c.want {
			t.Errorf("%+v: got %v", c, got)
		}
	}
}

func TestLocaleUpper(t *testing.T) {
	tr, _ := xlang.Parse("tr-TR")
	az := localeTag("az_AZ")
	for _, c := range []struct {
		r    rune
		tag  xlang.Tag
		want rune
	}{
		{'i', localeTag("tr_TR.UTF-8"), 'İ'},
		{'i', tr, 'İ'},
		{'i', az, 'İ'},
		{'i', localeTag("en_US"), 'I'},
		{'i', localeTag(""), 'I'},
		{'ı', localeTag("tr_TR"), 'I'},
		{'é', localeTag("fr_FR"), 'É'},
		{'ß', localeTag("de_DE"), 'ß'}, // would become "SS": stays one rune
	} {
		if got := localeUpper(c.r, c.tag); got != c.want {
			t.Errorf("%c %v: got %c want %c", c.r, c.tag, got, c.want)
		}
	}
}

func TestListDictsAndOptions(t *testing.T) {
	dirs := fakeDicts(t, "tr_TR", "de_DE", "en_US", "xx_XX")
	// a lone .dic is not offered
	if err := os.WriteFile(filepath.Join(dirs[0], "fr_FR.dic"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ds := listDicts(dirs)
	var names []string
	for _, d := range ds {
		names = append(names, d.Name)
	}
	if got := fmt.Sprint(names); got != "[de_DE en_US tr_TR xx_XX]" {
		t.Fatalf("dictionaries: %s", got)
	}
	sys := resolveSystem(envOf(map[string]string{"LANG": "de_AT.UTF-8", "DICPATH": dirs[0], "HOME": t.TempDir()}), "")
	opts := acOptions(sys, ds)
	want := []acOption{
		{"System language (de_DE)", ""},
		{"German — Deutsch (de_DE)", "de_DE"},
		{"English (en_US)", "en_US"},
		{"Turkish — Türkçe (tr_TR)", "tr_TR"},
		{"xx_XX", "xx_XX"},
		{"Off", "off"},
	}
	if fmt.Sprint(opts) != fmt.Sprint(want) {
		t.Fatalf("options:\n got %v\nwant %v", opts, want)
	}
}

func TestSavedPickOverridesSystem(t *testing.T) {
	dirs := fakeDicts(t, "de_DE", "tr_TR")
	env := envOf(map[string]string{"LANG": "de_DE.UTF-8", "DICPATH": dirs[0], "HOME": t.TempDir()})
	s := resolveSystem(env, "tr_TR")
	if !s.HasDic || s.Dict.Name != "tr_TR" || !s.Pick || s.Missing != "" {
		t.Fatalf("pick: %+v", s)
	}
	if off := resolveSystem(env, "off"); !off.Off || autocorrectEnabled(true, true, off) || autocorrectStatus(off) != "Autocorrect language: off" {
		t.Fatalf("off: %+v", off)
	}
}

func TestSavedPickMissingFallsBack(t *testing.T) {
	dirs := fakeDicts(t, "de_DE")
	env := envOf(map[string]string{"LANG": "de_DE.UTF-8", "DICPATH": dirs[0], "HOME": t.TempDir()})
	s := resolveSystem(env, "tr_TR")
	if !s.HasDic || s.Dict.Name != "de_DE" || s.Pick || s.Missing != "tr_TR" {
		t.Fatalf("fallback: %+v", s)
	}
	got := autocorrectStatus(s)
	if !strings.Contains(got, "tr_TR is not installed") || !strings.Contains(got, "Autocorrect language: de_DE") {
		t.Fatal(got)
	}
	// no system dictionary either: off, with the reason
	es := resolveSystem(envOf(map[string]string{"LANG": "es_ES.UTF-8", "DICPATH": dirs[0], "HOME": t.TempDir()}), "tr_TR")
	if es.HasDic && es.Dict.Name[:2] == "tr" {
		t.Fatalf("missing pick must not be used: %+v", es)
	}
}

func TestPickIsSavedInPreferences(t *testing.T) {
	dirs := fakeDicts(t, "de_DE", "tr_TR")
	t.Setenv("DICPATH", dirs[0])
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("HOME", t.TempDir())
	sysMu.Lock()
	sysCache = map[string]acSystem{}
	sysMu.Unlock()
	h := newHarness(t)
	h.ui(func() {
		d := h.d
		p := d.app.Preferences()
		p.SetString(prefACDict, "")
		p.RemoveValue("autocorrect")
		if got := d.acSys(); got.Dict.Name != "de_DE" || !d.autocorrectOn() {
			t.Fatalf("system rule: %+v", got)
		}
		d.setACPick("tr_TR")
		if p.String(prefACDict) != "tr_TR" || d.acSys().Dict.Name != "tr_TR" || !d.autocorrectOn() {
			t.Fatalf("saved pick not used: %+v", d.acSys())
		}
		d.setACPick("off")
		if p.String(prefACDict) != "off" || d.autocorrectOn() {
			t.Fatal("Off must switch autocorrect off")
		}
		d.setACPick("")
		if d.acSys().Dict.Name != "de_DE" {
			t.Fatal("System language must restore the system rule")
		}
		// the saved dictionary is uninstalled later
		d.setACPick("tr_TR")
		os.Remove(filepath.Join(dirs[0], "tr_TR.dic"))
		sysMu.Lock()
		sysCache = map[string]acSystem{}
		sysMu.Unlock()
		if s := d.acSys(); s.Dict.Name != "de_DE" || s.Missing != "tr_TR" {
			t.Fatalf("fallback: %+v", s)
		}
		p.SetString(prefACDict, "")
	})
}
