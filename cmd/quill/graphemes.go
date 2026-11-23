package main

import (
	"unicode"

	"github.com/rivo/uniseg"
)

// Text is revealed and faded by whole letters (grapheme clusters, user
// 2026-11-23): a combining accent, a Hindi conjunct, a ZWJ emoji or a flag
// is one step, never shown half-built for a frame.

// clusters splits s into grapheme clusters.
func clusters(s string) []string {
	var out []string
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		out = append(out, g.Str())
	}
	return out
}

// shapedScripts: scripts whose letters change shape with their neighbours
// (joining or reordering across clusters), so a fade must not cut a word of
// them between two letters.
var shapedScripts = []*unicode.RangeTable{
	unicode.Arabic, unicode.Syriac, unicode.Mongolian, unicode.Nko, unicode.Thaana,
	unicode.Devanagari, unicode.Bengali, unicode.Gurmukhi, unicode.Gujarati,
	unicode.Oriya, unicode.Tamil, unicode.Telugu, unicode.Kannada,
	unicode.Malayalam, unicode.Sinhala, unicode.Khmer, unicode.Myanmar,
}

func shapedRune(r rune) bool {
	for _, t := range shapedScripts {
		if unicode.Is(t, r) {
			return true
		}
	}
	return false
}

// shapedCluster: the cluster is a letter (or joiner) of a shaped script.
func shapedCluster(c string) bool {
	for _, r := range c {
		if shapedRune(r) {
			return true
		}
		if r == '‌' || r == '‍' {
			return true
		}
		if !unicode.IsMark(r) {
			return false
		}
	}
	return false
}

// snapRun moves a cut between cs[cut-1] and cs[cut] back to the start of the
// run of shaped letters it falls in (it stays when the neighbours do not
// join).
func snapRun(cs []string, cut int) int {
	for cut > 0 && cut < len(cs) && shapedCluster(cs[cut-1]) && shapedCluster(cs[cut]) {
		cut--
	}
	return cut
}
