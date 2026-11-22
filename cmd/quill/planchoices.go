package main

import (
	"regexp"
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
		// "Do you approve steps 1–3, or should I change something first?" is
		// the approval, not a pick between two things to approve.
		if approveOnly.MatchString(strings.TrimSpace(qQuoted.ReplaceAllString(sent, " "))) {
			continue
		}
		_, opts, ok := parseChoices(sent)
		if !ok {
			continue
		}
		for i, o := range opts {
			opts[i] = dropQuestionWord(o)
		}
		if isYesNoPair(opts) {
			continue // "Do you approve …? Yes or no?": the card's own buttons answer it
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
		if !strings.ContainsAny(f[1], "0123456789") { // "v0.1.20 ship now" stays lower case
			r[0] = []rune(strings.ToUpper(string(r[0])))[0]
		}
		return string(r)
	}
	return o
}

// continuation: a comma part that goes on with the option before it
// ("all of them, with single-action buttons getting …") rather than
// starting a new one (user 2026-11-22).
var continuation = regexp.MustCompile(`(?i)^(?:with|without|including|plus|so|so that|which|that|where|while|because|but|then|except|unless|since|using|via|like|such as|e\.g\.|i\.e\.)\b`)

// mergeContinuations joins comma parts that continue the part before them.
func mergeContinuations(parts []string) []string {
	var out []string
	for _, p := range parts {
		if len(out) > 0 && continuation.MatchString(strings.TrimSpace(p)) {
			out[len(out)-1] += ", " + p
			continue
		}
		out = append(out, p)
	}
	return out
}

// recommendedOption: which of n options (split as parseChoices splits them,
// but with their parenthesised asides kept) says "recommended".
func recommendedOption(sentence string, n int) int {
	ask := sentence
	if q := strings.LastIndex(ask, "?"); q >= 0 {
		ask = ask[:q]
	}
	if p := lastQOutsideParens(ask); p >= 0 {
		ask = ask[p+1:] // the options sentence after its question
	}
	segs := splitOutsideParens(ask, ", or ")
	if n == 2 && len(segs) < 2 {
		if yn := splitOutsideParens(ask, " or "); len(yn) == 2 && yesNoOptions(yn[0], yn[1]) {
			segs = yn
		}
	}
	if len(segs) == 2 && n == 2 && yesNoOptions(segs[0], segs[1]) {
		// "Yes, x (recommended), or no, y": keep the pair whole
	} else if len(segs) < 2 {
		segs = splitOutsideParens(ask, " or ")
	} else {
		head := mergeContinuations(splitOutsideParens(segs[0], ", "))
		segs = append(head, segs[1:]...)
	}
	if len(segs) != n {
		return -1
	}
	rec := -1
	for i, s := range segs {
		if isRecommended(s) {
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

var recommendedWord = regexp.MustCompile(`(?i)(not\s+)?\brecommended\b`)

// isRecommended: the option is marked "recommended" (not "not recommended",
// not a "recommendation").
func isRecommended(s string) bool {
	for _, m := range recommendedWord.FindAllStringSubmatch(s, -1) {
		if m[1] == "" {
			return true
		}
	}
	return false
}
