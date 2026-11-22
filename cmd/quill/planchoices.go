package main

import (
	"strings"
)

// Plan cards answer the choice in their question (user 2026-11-21): when the
// approval question also offers options ("…, and should it ship as v0.1.18
// (recommended) or replace v0.1.17?"), Approve sends the recommended option
// with the approval; with no recommendation there is one Approve per option.

// planChoices finds the options in a plan card's question: the last sentence
// of it that offers them, and the index of the one marked recommended (-1 if
// none).
func planChoices(question string) ([]string, int) {
	q := strings.TrimSpace(question)
	var sentences []string
	start := 0
	for i, r := range q {
		if r == '?' {
			sentences = append(sentences, strings.TrimSpace(q[start:i+1]))
			start = i + 1
		}
	}
	for i := len(sentences) - 1; i >= 0; i-- {
		sent := sentences[i]
		_, opts, ok := parseChoices(sent)
		if !ok {
			continue
		}
		for i, o := range opts {
			opts[i] = dropQuestionWord(o)
		}
		return opts, recommendedOption(sent, len(opts))
	}
	return nil, -1
}

// dropQuestionWord: "Should kmem ship as v0.1.18" → "Kmem ship as v0.1.18"
// reads as an answer on an Approve button.
func dropQuestionWord(o string) string {
	f := strings.Fields(o)
	if len(f) < 3 {
		return o
	}
	switch strings.ToLower(f[0]) {
	case "should", "shall", "do", "does", "will", "would", "can", "could", "is", "are":
		r := []rune(strings.Join(f[1:], " "))
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
		return string(r)
	}
	return o
}

// recommendedOption: which of n options (split as parseChoices splits them,
// but with their parenthesised asides kept) says "recommended".
func recommendedOption(sentence string, n int) int {
	ask := sentence
	if q := strings.LastIndex(ask, "?"); q >= 0 {
		ask = ask[:q]
	}
	segs := splitOutsideParens(ask, ", or ")
	if len(segs) < 2 {
		segs = splitOutsideParens(ask, " or ")
	} else {
		head := splitOutsideParens(segs[0], ", ")
		segs = append(head, segs[1:]...)
	}
	if len(segs) != n {
		return -1
	}
	rec := -1
	for i, s := range segs {
		if strings.Contains(strings.ToLower(s), "recommend") {
			if rec >= 0 {
				return -1 // more than one: no clear recommendation
			}
			rec = i
		}
	}
	return rec
}

// splitOutsideParens splits at the last sep that is outside parentheses
// (", or " / " or ") or, for ", ", at every one outside parentheses.
func splitOutsideParens(s, sep string) []string {
	depth := 0
	var cuts []int
	for i := 0; i+len(sep) <= len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		if depth == 0 && strings.HasPrefix(s[i:], sep) {
			cuts = append(cuts, i)
		}
	}
	if len(cuts) == 0 {
		return []string{s}
	}
	if sep != ", " {
		c := cuts[len(cuts)-1]
		return []string{s[:c], s[c+len(sep):]}
	}
	var out []string
	prev := 0
	for _, c := range cuts {
		out = append(out, s[prev:c])
		prev = c + len(sep)
	}
	return append(out, s[prev:])
}
