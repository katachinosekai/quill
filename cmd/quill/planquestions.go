package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/katachinosekai/quill/internal/chat"
)

// Plan questions (user 2026-11-21): a plan often ends with several open
// questions, or asks things in its items: yes/no, "1, 2 or 3?", "pick any of
// a–d", or something to write. The plan card gives each its own control, and
// Approve sends every answer with the approval in one message. Rule 28 asks
// sessions to number those questions, one per line.

type planQKind string

const (
	qYesNo  planQKind = "yesno"
	qSingle planQKind = "single"
	qMulti  planQKind = "multi"
	qText   planQKind = "text"
)

type planQ struct {
	ID          int       `json:"id"`
	Num         int       `json:"num,omitempty"` // the question's own number in the plan ("2." → 2), 0 = none
	Text        string    `json:"text"`
	Kind        planQKind `json:"kind"`
	Options     []string  `json:"options,omitempty"`
	Recommended int       `json:"recommended"` // -1 = none
	// RecommendedPicks: a multi question's options picked as the
	// recommendation ("All of a–e (recommended)" picks every one).
	RecommendedPicks []int `json:"recommendedPicks,omitempty"`

	// line, end: the original lines of the plan text the question is on
	// (end exclusive), for showing its control in place (planoverlay.go).
	line, end int
}

var (
	qSentenceEnd = regexp.MustCompile(`[.!]\s+`) // not the dots in 1.3.7, not "Topic: …"
	codeOrURL    = regexp.MustCompile("`[^`]*`|https?://\\S+")
	qNumLead     = regexp.MustCompile(`^\s*(?:[-*+]\s+)?(?:\*\*)?(\d+)[.)]\s+`)
	qQuoted      = regexp.MustCompile("\"[^\"]*\"|“[^”]*”|`[^`]*`|\\([^)]*\\)")
	fenceLine    = regexp.MustCompile("^\\s*```")
	listLead     = regexp.MustCompile(`^\s*(?:[-*+]\s+|\d+[.)]\s+|[a-hA-H][.)]\s+)?`)
	letterItem   = regexp.MustCompile(`^\s*[-*+]?\s*(?:\*\*)?([a-h])[.)](?:\*\*)?\s+(?:\*\*)?(.+)$`)
	multiAsk     = regexp.MustCompile(`(?i)\bwhich of\b|\bpick any\b|\bany of\b|\bwhich ones?\b|\bselect any\b`)
	// inlineLetter: an inline lettered option's mark ("pick from: a) x, b) y"),
	// not "(a)" (optionMark's) or a word ending in a letter.
	inlineLetter = regexp.MustCompile(`(?:^|[\s,;:])([a-h])\)\s+`)
	allRecommend = regexp.MustCompile(`(?i)\ball\b[^,;?]{0,24}\(\s*recommended\s*\)`)
	yesNoStart   = regexp.MustCompile(`(?i)^(?:and |also |then |so )?(?:should|shall|do|does|did|can|could|will|would|is|are|was|may|want|ok|okay)\b`)
	stepsAsk     = regexp.MustCompile(`(?i)\b(?:run|carry out|make|do|start)\b.*\b(?:steps?|changes?) \d`)
	approveOnly  = regexp.MustCompile(`(?i)^(?:and |so )?(?:do you |would you |can you )?(?:approve|ok to run|go ahead|proceed)\b|\bapprove (?:steps?|the plan|this plan|it|all|both|these|them|[a-z]+ \d)`)
)

// planQuestions reads the plan text for questions to answer, besides the
// approval itself. Fewer than one useful question returns nil (the plain
// card, or the single-choice card from planChoices, is used then).
func planQuestions(text string) []planQ {
	lines, first := chat.JoinOptionLineSpans(text)
	nOrig := strings.Count(text, "\n") + 1
	// Lettered items ("a. …", "- **b.** …") are the options a "which of"
	// question offers.
	var letters []string
	inFence := false
	for _, l := range lines {
		if fenceLine.MatchString(l) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if inl := inlineLetterOpts(mdNoise.Replace(l)); inl != nil {
			letters = inl // "Optional parts: a) x, b) y, c) z."
			continue
		}
		if m := letterItem.FindStringSubmatch(mdNoise.Replace(l)); m != nil {
			if m[1] == "a" {
				letters = nil // a new list starts: the one closest to the question counts
			}
			opt := strings.TrimSpace(strings.TrimRight(m[2], "*_ "))
			if c := strings.IndexAny(opt, ":(—,"); c > 0 {
				opt = strings.TrimSpace(opt[:c])
			}
			letters = append(letters, m[1]+": "+shortLabel(opt, 50))
		}
	}
	// Questions for the user come from the end of the plan: the lines after
	// the last plain line (a plan step, a sentence), not a "?" inside a step,
	// code or a URL. Lettered items and headings ("Questions:") don't end it.
	clean := make([]string, len(lines))
	start := 0
	inFence = false
	for i, l := range lines {
		if fenceLine.MatchString(l) {
			inFence = !inFence
			start = i + 1
			continue
		}
		if inFence {
			start = i + 1
			continue
		}
		c := codeOrURL.ReplaceAllString(l, " ")
		clean[i] = c
		t := strings.TrimSpace(strings.Trim(strings.TrimSpace(mdNoise.Replace(c)), "*_ "))
		// A "?" only inside quotes is an example in prose, not a question
		// (user 2026-11-22: a quoted "Where…? A or B?" became card questions).
		asks := strings.Contains(qQuoted.ReplaceAllString(t, " "), "?")
		if t == "" || asks || strings.HasSuffix(t, ":") || strings.HasPrefix(t, "#") || letterItem.MatchString(mdNoise.Replace(l)) {
			continue
		}
		start = i + 1
	}
	// When the closing block has numbered questions, they are the questions:
	// question-like prose above the first one is not.
	for i := start; i < len(lines); i++ {
		if qNumLead.MatchString(mdNoise.Replace(lines[i])) && strings.Contains(lines[i], "?") {
			start = i
			break
		}
	}
	var out []planQ
	seen := map[string]bool{}
	lineOf := -1 // the line of the last question in out
	for li, l := range lines[start:] {
		if !strings.Contains(l, "?") {
			continue
		}
		num := 0
		if m := qNumLead.FindStringSubmatch(mdNoise.Replace(l)); m != nil {
			fmt.Sscan(m[1], &num)
		}
		line := keepCode(l)
		line = strings.TrimSpace(mdNoise.Replace(line))
		line = strings.TrimSpace(listLead.ReplaceAllString(line, ""))
		line = strings.TrimSpace(strings.TrimLeft(line, "#>*_ "))
		afterApproval := false
		for _, sent := range questionSentences(line) {
			key := strings.ToLower(sent)
			if seen[key] {
				continue
			}
			seen[key] = true
			// "Yes (recommended) or no?" after a question answers how to read
			// that question: it is a yes/no with a default, not a new one.
			// Likewise "Inside (recommended) or linked?" after "Where should
			// it live?" on the same line gives that question its options.
			// "Which parts should be built? All of a–e (recommended), or pick
			// from: a) x, b) y, …?": the second sentence gives the first its
			// options, any of them (Quill Android 2026-11-22).
			if opts := inlineLetterOpts(sent); opts != nil && !afterApproval && len(out) > 0 && out[len(out)-1].Num == num && lineOf == li && (out[len(out)-1].Kind == qText || out[len(out)-1].Kind == qYesNo) {
				prev := &out[len(out)-1]
				prev.Kind, prev.Options, prev.Recommended = qMulti, opts, -1
				prev.RecommendedPicks = allPicks(sent, len(opts))
				continue
			}
			if _, opts, ok := parseChoices(sent); ok && (afterApproval || len(out) > 0 && out[len(out)-1].Num == num && lineOf == li) {
				if !afterApproval {
					prev := &out[len(out)-1]
					for i, o := range opts {
						opts[i] = dropQuestionWord(o)
					}
					if isYesNoPair(opts) {
						prev.Kind, prev.Options, prev.Recommended = qYesNo, []string{"Yes", "No"}, recommendedOption(sent, 2)
					} else if len(opts) == 2 && yesNoOptions(opts[0], opts[1]) {
						prev.Kind, prev.Options, prev.Recommended = qSingle, opts, recommendedOption(sent, 2)
					} else if prev.Kind == qText || prev.Kind == qYesNo {
						prev.Kind, prev.Options, prev.Recommended = qSingle, opts, recommendedOption(sent, len(opts))
					}
				}
				afterApproval = false
				continue
			}
			afterApproval = false
			q, ok := classifyQuestion(sent, letters)
			if !ok {
				afterApproval = true // the approval itself: the card's main button
				continue
			}
			q.ID, q.Num = len(out)+1, num
			q.line, q.end = first[start+li], nOrig
			if start+li+1 < len(first) {
				q.end = first[start+li+1]
			}
			out = append(out, q)
			lineOf = li
		}
	}
	return out
}

// keepCode keeps inline code and URLs in a question's text (Q3 lost
// `dist/quill -tags wayland`, user 2026-11-22), dropping only those with a
// "?" inside, which would split the question.
func keepCode(l string) string {
	return codeOrURL.ReplaceAllStringFunc(l, func(m string) string {
		if strings.Contains(m, "?") {
			return " "
		}
		return strings.Trim(m, "`")
	})
}

// questionSentences: the sentences of a line that end in "?" (quoted words
// and parentheses don't split).
func questionSentences(line string) []string {
	var out []string
	start, depth := 0, 0
	for i, r := range line {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '?':
			if depth == 0 {
				s := strings.TrimSpace(line[start : i+1])
				// The sentence starts after the last ". " / "! " outside
				// parentheses ("(as in option B: the safe path)" stays whole).
				body := qQuoted.ReplaceAllStringFunc(s[:len(s)-1], func(m string) string { return strings.Repeat("x", len(m)) })
				if m := qSentenceEnd.FindAllStringIndex(body, -1); len(m) > 0 {
					s = strings.TrimSpace(s[m[len(m)-1][1]:])
				}
				if s != "?" && len(s) > 3 {
					out = append(out, s)
				}
				start = i + 1
			}
		}
	}
	return out
}

func classifyQuestion(sent string, letters []string) (planQ, bool) {
	q := planQ{Text: strings.TrimSpace(strings.TrimRight(sent, "*_ ")), Recommended: -1}
	bare := qQuoted.ReplaceAllString(q.Text, " ")
	if opts := inlineLetterOpts(q.Text); opts != nil {
		q.Kind, q.Options, q.RecommendedPicks = qMulti, opts, allPicks(q.Text, len(opts))
		return q, true
	}
	if multiAsk.MatchString(bare) && len(letters) >= 2 {
		q.Kind, q.Options = qMulti, letters
		return q, true
	}
	// The approval itself is the card's main button, not a question here
	// ("Do you approve steps 1–3, or should I change something first?" is
	// still the approval); a question joined to it ("…steps 1–7, and do you
	// also want X?") stays.
	// "Can I run steps 1–5?" asks to go ahead too (chat's approval words).
	if approveOnly.MatchString(strings.TrimSpace(bare)) || yesNoStart.MatchString(strings.TrimSpace(bare)) && chat.AsksToApprove(bare) && stepsAsk.MatchString(bare) {
		for _, sep := range []string{", and ", "; and ", ", also ", " and also "} {
			if i := strings.LastIndex(q.Text, sep); i > 0 {
				if rest := strings.TrimSpace(q.Text[i+len(sep):]); len(rest) > 3 && !approveOnly.MatchString(rest) {
					r := []rune(rest)
					r[0] = []rune(strings.ToUpper(string(r[0])))[0]
					return classifyQuestion(string(r), letters)
				}
			}
		}
		return q, false
	}
	if _, opts, ok := parseChoices(q.Text); ok {
		for i, o := range opts {
			opts[i] = dropQuestionWord(o)
		}
		if isYesNoPair(opts) {
			q.Kind, q.Options, q.Recommended = qYesNo, []string{"Yes", "No"}, recommendedOption(q.Text, 2)
			return q, true
		}
		q.Kind, q.Options, q.Recommended = qSingle, opts, recommendedOption(q.Text, len(opts))
		return q, true
	}
	if yesNoStart.MatchString(strings.TrimSpace(bare)) {
		q.Kind, q.Options = qYesNo, []string{"Yes", "No"}
		return q, true
	}
	q.Kind = qText
	return q, true
}

// inlineLetterOpts: the lettered options listed inline in s ("pick from:
// a) local check, b) guardrails?" or "a) x b) y c) z"), as "a: local check",
// or nil. They follow a colon or start s, or there are at least three.
func inlineLetterOpts(s string) []string {
	ms := inlineLetter.FindAllStringSubmatchIndex(s, -1)
	type mark struct{ start, text int }
	var run []mark
	for _, m := range ms {
		if s[m[2]] == byte('a'+len(run)) {
			run = append(run, mark{m[2], m[1]})
		}
	}
	if len(run) < 2 {
		return nil
	}
	before := strings.TrimRight(strings.TrimSpace(mdNoise.Replace(s[:run[0].start])), "*_ ")
	if before != "" && !strings.HasSuffix(before, ":") && len(run) < 3 {
		return nil
	}
	var out []string
	for i, r := range run {
		end := len(s)
		if i+1 < len(run) {
			end = run[i+1].start
		}
		o := strings.TrimSpace(s[r.text:end])
		if i+1 == len(run) {
			o = strings.TrimSpace(strings.TrimSuffix(o, "?"))
		}
		for _, tail := range []string{" and", " or"} {
			o = strings.TrimSpace(strings.TrimSuffix(o, tail))
		}
		o = strings.TrimSpace(strings.TrimRight(o, ",;.?*_ "))
		for _, cut := range []string{" (", " — ", ": "} {
			if c := strings.Index(o, cut); c > 0 {
				o = strings.TrimSpace(o[:c])
			}
		}
		if o == "" {
			return nil
		}
		out = append(out, string(s[r.start])+": "+shortLabel(o, 50))
	}
	return out
}

// allPicks: every option index when s recommends all of them ("All of a–e
// (recommended)", "all (recommended)"), else nil.
func allPicks(s string, n int) []int {
	if !allRecommend.MatchString(s) {
		return nil
	}
	picks := make([]int, n)
	for i := range picks {
		picks[i] = i
	}
	return picks
}

func isYesNoPair(opts []string) bool {
	if len(opts) != 2 {
		return false
	}
	a, b := strings.ToLower(opts[0]), strings.ToLower(opts[1])
	return strings.HasSuffix(a, "yes") && strings.HasPrefix(b, "no")
}

// planAnswer is one question's answer from a card or the phone API.
type planAnswer struct {
	Yes    *bool  // yesno
	Choice int    // single: option index, -1 = none
	Picks  []int  // multi: option indexes
	Text   string // text
}

// planGoAnswers: the approval with every question's answer, in order, so
// Claude reads them against its numbered questions.
func planGoAnswers(qs []planQ, ans map[int]planAnswer) string {
	if len(qs) == 0 {
		return planGoMessage
	}
	var b strings.Builder
	b.WriteString(planGoMessage)
	b.WriteString(" Answers to your questions:")
	for i, q := range qs {
		a, ok := ans[q.ID]
		val := "no answer"
		if ok {
			switch q.Kind {
			case qYesNo:
				if a.Yes != nil {
					val = map[bool]string{true: "yes", false: "no"}[*a.Yes]
				}
			case qSingle:
				if a.Choice >= 0 && a.Choice < len(q.Options) {
					val = q.Options[a.Choice]
				}
			case qMulti:
				var picks []string
				for _, p := range a.Picks {
					if p >= 0 && p < len(q.Options) {
						picks = append(picks, q.Options[p])
					}
				}
				if len(picks) > 0 {
					val = strings.Join(picks, "; ")
				} else if a.Picks != nil {
					val = "none"
				}
			case qText:
				// One line, so an answer can't look like more answers.
				if t := strings.Join(strings.Fields(a.Text), " "); t != "" {
					val = "\"" + t + "\""
				}
			}
		}
		n := i + 1
		if q.Num > 0 {
			n = q.Num
		}
		fmt.Fprintf(&b, " %d. %s → %s.", n, shortLabel(q.Text, 90), val)
	}
	return b.String()
}

// planAnswersJSON: the phone API's "answers" for these answers (question id
// → "yes"/"no", option index, option indexes, or text).
func planAnswersJSON(qs []planQ, ans map[int]planAnswer) map[string]any {
	out := map[string]any{}
	for _, q := range qs {
		a, ok := ans[q.ID]
		if !ok {
			continue
		}
		key := fmt.Sprint(q.ID)
		switch q.Kind {
		case qYesNo:
			if a.Yes != nil {
				out[key] = map[bool]string{true: "yes", false: "no"}[*a.Yes]
			}
		case qSingle:
			if a.Choice >= 0 {
				out[key] = a.Choice
			}
		case qMulti:
			if a.Picks != nil {
				out[key] = a.Picks
			}
		case qText:
			if strings.TrimSpace(a.Text) != "" {
				out[key] = a.Text
			}
		}
	}
	return out
}

// planItemText: the plan's text plus its closing question, as the card reads it.
func planItemText(s *deskSession, it *chat.Item) string {
	text := s.conv.PlanText(it)
	if q := planQuestion(s.conv, it); !strings.Contains(text, q) {
		text += "\n" + q
	}
	return text
}

// parsePlanAnswers reads the phone API's planAnswers against the questions.
func parsePlanAnswers(qs []planQ, raw map[string]json.RawMessage) (map[int]planAnswer, error) {
	byID := map[string]planQ{}
	for _, q := range qs {
		byID[fmt.Sprint(q.ID)] = q
	}
	out := map[int]planAnswer{}
	for k, v := range raw {
		q, ok := byID[k]
		if !ok {
			return nil, fmt.Errorf("no plan question %s", k)
		}
		a := planAnswer{Choice: -1}
		if strings.TrimSpace(string(v)) == "null" {
			return nil, fmt.Errorf("question %s: leave it out instead of null", k)
		}
		switch q.Kind {
		case qYesNo:
			var yn string
			if json.Unmarshal(v, &yn) != nil || yn != "yes" && yn != "no" {
				return nil, fmt.Errorf("question %s needs \"yes\" or \"no\"", k)
			}
			yes := yn == "yes"
			a.Yes = &yes
		case qSingle:
			if json.Unmarshal(v, &a.Choice) != nil || a.Choice < 0 || a.Choice >= len(q.Options) {
				return nil, fmt.Errorf("question %s needs an option index from 0 to %d", k, len(q.Options)-1)
			}
		case qMulti:
			if json.Unmarshal(v, &a.Picks) != nil {
				return nil, fmt.Errorf("question %s needs a list of option indexes", k)
			}
			seenPick := map[int]bool{}
			for _, p := range a.Picks {
				if p < 0 || p >= len(q.Options) {
					return nil, fmt.Errorf("question %s: option %d is out of range", k, p)
				}
				if seenPick[p] {
					return nil, fmt.Errorf("question %s: option %d is picked twice", k, p)
				}
				seenPick[p] = true
			}
			if a.Picks == nil {
				a.Picks = []int{}
			}
		case qText:
			if json.Unmarshal(v, &a.Text) != nil {
				return nil, fmt.Errorf("question %s needs text", k)
			}
		}
		out[q.ID] = a
	}
	return out, nil
}

// planItemQuestions: the questions a plan card shows for this plan item.
// The card's own question (its title, the approval) is left out: a
// proposal like "Should I ship 1.3.7 now?" is answered by Approve itself, and
// a pick in it ("…v0.1.18 (recommended), or …?") by planChoices' buttons.
func planItemQuestions(s *deskSession, it *chat.Item) []planQ {
	return planQuestionsFor(planQuestion(s.conv, it), planItemText(s, it))
}

// planQuestionsFor: planItemQuestions for a card titled title over text.
func planQuestionsFor(title, text string) []planQ {
	// Only when the title has no explicit approval ("Do you approve…?") is
	// its question the approval itself.
	own := ""
	explicit := false
	sents := questionSentences(strings.TrimSpace(strings.Trim(mdNoise.Replace(title), "*_ ")))
	for _, q := range sents {
		explicit = explicit || approveOnly.MatchString(strings.TrimSpace(qQuoted.ReplaceAllString(q, " ")))
	}
	if !explicit && len(sents) > 0 {
		own = normQ(sents[len(sents)-1])
	}
	var out []planQ
	for _, q := range planQuestions(text) {
		if own != "" && normQ(q.Text) == own {
			continue
		}
		q.ID = len(out) + 1
		out = append(out, q)
	}
	return out
}

func normQ(s string) string {
	s = strings.ToLower(mdNoise.Replace(s))
	return strings.TrimSpace(strings.Trim(listLead.ReplaceAllString(strings.TrimSpace(s), ""), "*_ "))
}
