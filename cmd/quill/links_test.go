package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

type linkCase struct {
	Name  string `json:"name"`
	Text  string `json:"text"`
	Links []struct {
		Kind    string `json:"kind"`
		Start   int    `json:"start"`
		End     int    `json:"end"`
		Text    string `json:"text"`
		Target  string `json:"target"`
		Label   string `json:"label"`
		Line    int    `json:"line"`
		LineEnd int    `json:"lineEnd"`
	} `json:"links"`
}

func loadLinkCases(t *testing.T) []linkCase {
	t.Helper()
	b, err := os.ReadFile("testdata/links.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version int        `json:"version"`
		Cases   []linkCase `json:"cases"`
	}
	if err := json.Unmarshal(b, &doc); err != nil || doc.Version != 1 || len(doc.Cases) == 0 {
		t.Fatalf("links.json: %v (version %d, %d cases)", err, doc.Version, len(doc.Cases))
	}
	return doc.Cases
}

// TestSharedLinkCases runs every row of testdata/links.json (the file Quill
// Android's LinksTest reads too) through findLinks.
func TestSharedLinkCases(t *testing.T) {
	for _, c := range loadLinkCases(t) {
		got := findLinks(c.Text)
		if len(got) != len(c.Links) {
			t.Errorf("%s: %q: got %d links %+v, want %d", c.Name, c.Text, len(got), got, len(c.Links))
			continue
		}
		for i, w := range c.Links {
			g := got[i]
			start, end := utf8.RuneCountInString(c.Text[:g.Start]), utf8.RuneCountInString(c.Text[:g.End])
			if g.Kind != w.Kind || start != w.Start || end != w.End || c.Text[g.Start:g.End] != w.Text || g.Target != w.Target || g.Label != w.Label || g.Line != w.Line || g.LineEnd != w.LineEnd {
				t.Errorf("%s: link %d = %+v (code points %d-%d), want %+v", c.Name, i, g, start, end, w)
			}
		}
	}
}

// TestLinkCasesChecksum: Quill Android checks its copy of links.json against
// this checksum; a rule change must update both.
func TestLinkCasesChecksum(t *testing.T) {
	b, err := os.ReadFile("testdata/links.json")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../docs/phone-links-cases.sha256")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); strings.Fields(string(want))[0] != got {
		t.Fatalf("docs/phone-links-cases.sha256 is out of date: links.json is %s", got)
	}
}

// TestAutolinksAgreeWithDetector: Quill Desktop's autolinks wraps exactly
// the bare web and file URLs the shared detector finds. Two known
// differences stay until Quill Desktop's own rendering moves to findLinks
// (task t421a1e): autolinks has no 50-link cap and wraps file URLs with a
// host or without a path.
var autolinksDiffer = map[string]bool{
	"at most 50 links per item":                     true,
	"file URL with a host or no path is not a link": true,
}

func TestAutolinksAgreeWithDetector(t *testing.T) {
	for _, c := range loadLinkCases(t) {
		if autolinksDiffer[c.Name] {
			continue
		}
		want := c.Text
		links := findLinks(c.Text)
		for i := len(links) - 1; i >= 0; i-- {
			l := links[i]
			raw := c.Text[l.Start:l.End]
			if l.Kind != linkWeb && l.Kind != linkFile || strings.HasPrefix(raw, "[") || strings.HasPrefix(raw, "<") {
				continue
			}
			want = want[:l.Start] + "[" + raw + "](" + raw + ")" + want[l.End:]
		}
		if got := autolinks(c.Text); got != want {
			t.Errorf("%s: autolinks(%q) = %q, detector says %q", c.Name, c.Text, got, want)
		}
	}
}

func TestFindLinksEdges(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"/", nil},
		{"~/", nil},
		{"a /x/y\n```\n/in/fence\n```\n/b/c", []string{"/x/y", "/b/c"}},
		{"[x](file:///)", nil},
		{"[x](file:///a%zz/b)", nil},
		{"[x](https://)", nil},
		{"`~/a`", []string{"~/a"}},
		{"see /a/b:", []string{"/a/b"}},
	} {
		var got []string
		for _, l := range findLinks(c.in) {
			got = append(got, l.Target)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("findLinks(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
