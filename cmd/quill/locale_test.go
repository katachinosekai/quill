package main

import (
	"sort"
	"testing"
	"time"
)

var localeDay = time.Date(2025, time.March, 8, 14, 5, 0, 0, time.UTC) // a Saturday

func TestLocalDateNames(t *testing.T) {
	cases := []struct {
		loc  string
		kind dateKind
		want string
	}{
		{"", dateMD, "Mar 8"},
		{"en_US", dateMD, "Mar 8"},
		{"en_GB", dateDMDow, "Sat 8 Mar"},
		{"C", dateDMY, "8 Mar 2025"},
		{"", dateDowMD, "Sat Mar 8"},
		{"de_DE", dateMD, "8. Mär"},
		{"de_DE", dateDMDow, "Sa 8. Mär"},
		{"ja_JP", dateMD, "3月8日"},
		{"ja_JP", dateDMY, "2025年3月8日"},
		{"tr_TR", dateDMY, "8 Mar 2025"},
	}
	for _, c := range cases {
		if got := localTimeIn(c.loc, localeDay, dateLayout(c.loc, c.kind)); got != c.want {
			t.Errorf("%q kind %d: got %q, want %q", c.loc, c.kind, got, c.want)
		}
	}
	// Months that differ from English.
	if got := localTimeIn("de_DE.UTF-8", localeDay, "January"); got == "March" {
		t.Errorf("German month name still English: %q", got)
	}
	if got := localTimeIn("tr_TR", localeDay, "January Monday"); got != "Mart Cumartesi" {
		t.Errorf("Turkish names: %q", got)
	}
	if got := localTimeIn("ja_JP", localeDay, "Monday"); got != "土曜日" {
		t.Errorf("Japanese weekday: %q", got)
	}
}

func TestLocalDateFollowsEnv(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_TIME", "")
	t.Setenv("LANG", "en_US.UTF-8")
	if got := localDate(localeDay, dateMD); got != "Mar 8" {
		t.Errorf("english: %q", got)
	}
	t.Setenv("LANG", "ja_JP.UTF-8")
	if got := localDate(localeDay, dateMD); got != "3月8日" {
		t.Errorf("japanese: %q", got)
	}
	t.Setenv("LC_TIME", "de_DE.UTF-8")
	if got := localDate(localeDay, dateMD); got != "8. Mär" {
		t.Errorf("LC_TIME beats LANG: %q", got)
	}
	t.Setenv("LC_ALL", "C")
	if got := localDate(localeDay, dateMD); got != "Mar 8" {
		t.Errorf("LC_ALL=C beats LC_TIME: %q", got)
	}
}

func TestMsgTimeLayoutLocalised(t *testing.T) {
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	// A user pattern with names is translated too.
	l, _ := patternLayout("dddd d MMMM")
	if got := localTime(localeDay, l); got != "Samstag 8 März" {
		t.Errorf("got %q", got)
	}
}

func TestCollationByLocale(t *testing.T) {
	words := []string{"zebra", "Apfel", "Äpfel", "apple", "Zulu"}
	sortFor := func(loc string) []string {
		t.Setenv("LC_ALL", loc)
		out := append([]string(nil), words...)
		less := sortLess()
		sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
		return out
	}
	de := sortFor("de_DE.UTF-8")
	// German: Ä sorts with A (before Z), not after every ASCII letter.
	if de[len(de)-1] != "zebra" && de[len(de)-1] != "Zulu" {
		t.Errorf("german order: %v", de)
	}
	sv := sortFor("sv_SE.UTF-8")
	if sv[len(sv)-1] != "Äpfel" {
		t.Errorf("swedish puts Ä last: %v", sv)
	}
	en := sortFor("en_US.UTF-8")
	if en[0] != "Apfel" || en[1] != "Äpfel" || en[2] != "apple" {
		t.Errorf("english order: %v", en)
	}
	// Japanese: kana in gojuon order, not byte order.
	t.Setenv("LC_ALL", "ja_JP.UTF-8")
	less := sortLess()
	if !less("あ", "か") || !less("か", "さ") || less("さ", "か") {
		t.Error("japanese kana order")
	}
	// Turkish: ç sorts after c and before d, ı before i.
	t.Setenv("LC_ALL", "tr_TR.UTF-8")
	less = sortLess()
	if !less("cb", "çb") || !less("çb", "da") || !less("ı", "i") {
		t.Error("turkish order")
	}
}

func TestFoldMatches(t *testing.T) {
	yes := [][2]string{
		{"Straße", "STRASSE"},
		{"Straße", "strasse"},
		{"strasse", "Straße"},
		{"İstanbul", "istanbul"},
		{"ISPARTA", "ısparta"},
		{"ISPARTA", "isparta"},
		{"ΟΔΥΣΣΕΥΣ", "οδυσσευς"},
		{"ΟΔΥΣΣΕΥΣ", "οδυσσευσ"},
		{"Hello World", "o wor"},
		{"ÄPFEL", "äpfel"},
	}
	for _, c := range yes {
		if !foldContains(c[0], c[1]) {
			t.Errorf("%q should match %q", c[0], c[1])
		}
	}
	if foldContains("Apfel", "äpfel") || foldContains("hello", "help") {
		t.Error("false match")
	}
	if got := queryWords("Straße Ünï"); len(got) != 2 || got[0] != "strasse" {
		t.Errorf("queryWords %v", got)
	}
	if !hasAllWords("Die STRASSE nach İzmir", queryWords("straße izmir")) {
		t.Error("hasAllWords fold")
	}
}
