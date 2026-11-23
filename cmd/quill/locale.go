package main

import (
	"os"
	"strings"
	"time"

	"github.com/goodsign/monday"
	"golang.org/x/text/cases"
	"golang.org/x/text/collate"
	xlang "golang.org/x/text/language"
)

// The user's language for dates, sorting and search. It comes from the
// system locale (POSIX precedence: LC_ALL, then the category's own variable,
// then LANG), so the Desktop app follows the computer's language without a
// setting. English (and an unset or C locale) keeps the original layouts.

// envLocale returns the first set locale variable among keys, minus the
// encoding and modifier ("de_DE.UTF-8@euro" gives "de_DE"); "" for C/POSIX.
func envLocale(keys ...string) string {
	for _, k := range keys {
		v := strings.TrimSpace(os.Getenv(k))
		if v == "" {
			continue
		}
		if i := strings.IndexAny(v, ".@"); i >= 0 {
			v = v[:i]
		}
		if v == "C" || v == "POSIX" {
			return ""
		}
		return v
	}
	return ""
}

func timeLocale() string    { return envLocale("LC_ALL", "LC_TIME", "LANG") }
func collateLocale() string { return envLocale("LC_ALL", "LC_COLLATE", "LANG") }

func tagOf(loc string) xlang.Tag {
	if loc == "" {
		return xlang.Und
	}
	t, err := xlang.Parse(strings.ReplaceAll(loc, "_", "-"))
	if err != nil {
		return xlang.Und
	}
	return t
}

// langOf is the bare language code of a locale name ("de_DE" gives "de").
func langOf(loc string) string {
	if i := strings.IndexAny(loc, "_-"); i >= 0 {
		loc = loc[:i]
	}
	return strings.ToLower(loc)
}

// mondayByLang maps a bare language to the locale monday has names for.
var mondayByLang = map[string]monday.Locale{
	"bg": monday.LocaleBgBG, "ca": monday.LocaleCaES, "cs": monday.LocaleCsCZ,
	"da": monday.LocaleDaDK, "de": monday.LocaleDeDE, "el": monday.LocaleElGR,
	"es": monday.LocaleEsES, "et": monday.LocaleEtEE, "fi": monday.LocaleFiFI,
	"fr": monday.LocaleFrFR, "hr": monday.LocaleHrHR, "hu": monday.LocaleHuHU,
	"id": monday.LocaleIdID, "it": monday.LocaleItIT, "ja": monday.LocaleJaJP,
	"kk": monday.LocaleKkKZ, "ko": monday.LocaleKoKR, "lt": monday.LocaleLtLT,
	"lv": monday.LocaleLvLV, "nb": monday.LocaleNbNO, "no": monday.LocaleNbNO,
	"nn": monday.LocaleNnNO, "nl": monday.LocaleNlNL, "pl": monday.LocalePlPL,
	"pt": monday.LocalePtPT, "ro": monday.LocaleRoRO, "ru": monday.LocaleRuRU,
	"sk": monday.LocaleSkSK, "sl": monday.LocaleSlSI, "sv": monday.LocaleSvSE,
	"th": monday.LocaleThTH, "tr": monday.LocaleTrTR, "uk": monday.LocaleUkUA,
	"uz": monday.LocaleUzUZ, "zh": monday.LocaleZhCN,
}

// dateLocale is the monday locale for names; ok is false for English or an
// unsupported language (names then stay English).
func dateLocale(loc string) (monday.Locale, bool) {
	l := strings.ReplaceAll(loc, "-", "_")
	switch l {
	case "pt_BR":
		return monday.LocalePtBR, true
	case "nl_BE":
		return monday.LocaleNlBE, true
	case "zh_TW":
		return monday.LocaleZhTW, true
	case "zh_HK":
		return monday.LocaleZhHK, true
	}
	m, ok := mondayByLang[langOf(loc)]
	return m, ok
}

// localTime formats t with a Go layout, naming months and days in the
// system language. English output is exactly time.Format.
func localTime(t time.Time, layout string) string {
	return localTimeIn(timeLocale(), t, layout)
}

func localTimeIn(loc string, t time.Time, layout string) string {
	if m, ok := dateLocale(loc); ok {
		return monday.Format(t, layout, m)
	}
	return t.Format(layout)
}

// dateKind picks a layout shape; see dateLayout.
type dateKind int

const (
	dateMD    dateKind = iota // month and day: "Nov 21"
	dateDMDow                 // weekday, day, month: "Sat 21 Nov"
	dateDMY                   // day, month, year: "21 Nov 2025"
	dateDowMD                 // weekday, month, day: "Sat Nov 21" (English order)
)

// dateLayout returns the Go layout for a date in the user's language: the
// English layouts are unchanged, other languages get their own order
// (day first, "21. Nov" for German-style, "11月21日" for East Asian).
func dateLayout(loc string, k dateKind) string {
	lang := langOf(loc)
	if _, ok := dateLocale(loc); !ok || lang == "" {
		lang = "en"
	}
	switch lang {
	case "en":
		return [...]string{"Jan 2", "Mon 2 Jan", "2 Jan 2006", "Mon Jan 2"}[k]
	case "ja", "zh":
		return [...]string{"1月2日", "1月2日 Mon", "2006年1月2日", "1月2日 Mon"}[k]
	case "ko":
		return [...]string{"1월 2일", "1월 2일 Mon", "2006년 1월 2일", "1월 2일 Mon"}[k]
	case "de", "da", "nb", "nn", "no", "fi", "cs", "sk", "hr", "sl", "et":
		return [...]string{"2. Jan", "Mon 2. Jan", "2. Jan 2006", "Mon 2. Jan"}[k]
	}
	return [...]string{"2 Jan", "Mon 2 Jan", "2 Jan 2006", "Mon 2 Jan"}[k]
}

// localDate formats t in the shape k, in the system language.
func localDate(t time.Time, k dateKind) string {
	loc := timeLocale()
	return localTimeIn(loc, t, dateLayout(loc, k))
}

// newCollator sorts text the way the system language does (Ä after A in
// German, Å at the end in Swedish, ...).
func newCollator() *collate.Collator { return collate.New(tagOf(collateLocale())) }

// sortLess returns a case-insensitive-by-collation less function for
// display strings; ties fall back to byte order so sorting stays stable.
func sortLess() func(a, b string) bool {
	c := newCollator()
	return func(a, b string) bool {
		if r := c.CompareString(a, b); r != 0 {
			return r < 0
		}
		return a < b
	}
}

// foldKey normalises text for searching: Unicode case folding on both the
// query and the text, so ß matches ss, a final Greek sigma matches σ, and the
// Turkish dotted/dotless i (İ ı) match a plain i.
func foldKey(s string) string {
	s = cases.Fold().String(s)
	s = strings.ReplaceAll(s, "̇", "") // dot above left by İ
	return strings.ReplaceAll(s, "ı", "i")
}

// foldContains reports whether text contains q after folding both.
func foldContains(text, q string) bool {
	return strings.Contains(foldKey(text), foldKey(q))
}
