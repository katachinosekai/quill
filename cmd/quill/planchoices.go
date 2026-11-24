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
// starting a new one (user 2026-11-22). "as in …" and "about 30%" too (user
// 2026-11-23: 'Keep the name, as in "…"', 'light, about 30%').
var continuation = regexp.MustCompile(`(?i)^(?:with|without|including|plus|so|so that|which|that|where|while|because|but|then|except|unless|since|using|via|like|such as|as in|about|around|roughly|approximately|e\.g\.|i\.e\.)\b`)

// continuesPrev: p goes on with prev. Not when prev already uses p's lead word
// ("replaced with X (recommended), with Y, or keep both?": parallel options,
// not an elaboration; user 2026-11-23).
func continuesPrev(prev, p string) bool {
	lead := continuation.FindString(strings.TrimSpace(p))
	if lead == "" {
		return false
	}
	has, _ := regexp.MatchString(`(?i)\b`+regexp.QuoteMeta(lead)+`\b`, parenAside.ReplaceAllString(prev, ""))
	return !has
}

// mergeContinuations joins comma parts that continue the part before them.
func mergeContinuations(parts []string) []string {
	var out []string
	for _, p := range parts {
		if len(out) > 0 && continuesPrev(out[len(out)-1], p) {
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
	segs, _ := choiceParts(ask)
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

// choiceStems: a question's lead the first option drops ("Should I keep it,
// or drop it?" → "Keep it").
var choiceStems = []string{"do you want me to ", "would you like me to ", "should i ", "shall i ", "should we ", "shall we ", "do you want to ", "want me to ", "should it ", "do i "}

// choiceParts: the options an options sentence (no "?") names, asides kept,
// nil if it names none. wh: the first part is the question itself ("Which
// panels should I build, or is there something else?"), no options.
//
// Splitting happens at the top level only, never inside quotes or
// parentheses (user 2026-11-23, tc3712f): at the last ", or " (else " or "),
// and the part before it at ", " ("a, b, or c" and "a, b or c" list three)
// unless the options carry commas of their own: a yes/no pair ("Yes, 2
// subagents (recommended), or no, I'll do it myself"), or a last option with
// a comma ("light, about 30% (recommended), or medium, about 50%" is two). A
// topic before a colon ("Settings control: a, b, or c") is no part of the
// first option; a colon after the first comma or inside quotes stays.
func choiceParts(ask string) (opts []string, wh bool) {
	ask = strings.TrimSpace(ask)
	sep := ", or "
	cuts := topCuts(ask, sep)
	if len(cuts) == 0 {
		sep = " or "
		cuts = topCuts(ask, sep)
	}
	if len(cuts) == 0 {
		return nil, false
	}
	c := cuts[len(cuts)-1]
	a, b := strings.TrimSpace(ask[:c]), strings.TrimSpace(ask[c+len(sep):])
	lead := a
	if cs := topCuts(a, ", "); len(cs) > 0 {
		lead = a[:cs[0]]
	}
	if cs := topCuts(lead, ": "); len(cs) > 0 {
		a = strings.TrimSpace(a[cs[len(cs)-1]+2:])
	}
	low := strings.ToLower(a)
	for _, stem := range choiceStems {
		if strings.HasPrefix(low, stem) {
			a = strings.TrimSpace(a[len(stem):])
			break
		}
	}
	switch strings.ToLower(strings.Fields(a + " x")[0]) {
	case "which", "what", "who", "where", "when", "why", "how":
		wh = true
	}
	opts = []string{a}
	if !yesNoOptions(a, b) && len(topCuts(b, ", ")) == 0 {
		if parts := splitTop(a, ", "); len(parts) > 1 {
			opts = mergeContinuations(parts)
			// A lead-in before the first comma is not an option ("For the
			// icon, use blue, green, or red?").
			if len(opts) >= 2 && leadIn.MatchString(opts[0]) {
				opts = opts[1:]
			}
		}
	}
	return append(opts, b), wh
}

// quotedBytes marks the bytes of s inside quotes ("…", “…”, `…`; straight
// quotes only when they pair up), the quote marks included.
func quotedBytes(s string) []bool {
	in := make([]bool, len(s))
	straight := strings.Count(s, `"`)%2 == 0
	closer := ""
	for i := 0; i < len(s); i++ {
		if closer != "" {
			in[i] = true
			if strings.HasPrefix(s[i:], closer) {
				for j := 1; j < len(closer); j++ {
					in[i+j] = true
				}
				i += len(closer) - 1
				closer = ""
			}
			continue
		}
		switch {
		case s[i] == '"' && straight:
			closer = `"`
		case s[i] == '`':
			closer = "`"
		case strings.HasPrefix(s[i:], "“"):
			closer = "”"
			for j := 1; j < len("“"); j++ {
				in[i+j] = true
			}
			i += len("“") - 1
		default:
			continue
		}
		in[i] = true
	}
	return in
}

// topCuts: the byte offsets of sep in s outside parentheses and quotes.
func topCuts(s, sep string) []int {
	q := quotedBytes(s)
	depth := 0
	var cuts []int
	for i := 0; i < len(s); i++ {
		if q[i] {
			continue
		}
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
			i += len(sep) - 1
		}
	}
	return cuts
}

// splitTop splits s at every top-level sep (topCuts).
func splitTop(s, sep string) []string {
	var out []string
	prev := 0
	for _, c := range topCuts(s, sep) {
		out = append(out, s[prev:c])
		prev = c + len(sep)
	}
	return append(out, s[prev:])
}

// stripAsides drops the parenthesised asides of s ("(recommended)") that
// are not inside quotes.
func stripAsides(s string) string {
	q := quotedBytes(s)
	var b strings.Builder
	at := 0
	for _, m := range parenAside.FindAllStringIndex(s, -1) {
		if p := m[0] + strings.Index(s[m[0]:m[1]], "("); q[p] {
			continue
		}
		b.WriteString(s[at:m[0]])
		at = m[1]
	}
	b.WriteString(s[at:])
	return b.String()
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
