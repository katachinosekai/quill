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
	Text        string    `json:"text"`
	Kind        planQKind `json:"kind"`
	Options     []string  `json:"options,omitempty"`
	Recommended int       `json:"recommended"` // -1 = none
}

var (
	qSentenceEnd = regexp.MustCompile(`[.!:]\s+`) // not the dots in 1.3.7
	qQuoted      = regexp.MustCompile("\"[^\"]*\"|“[^”]*”|`[^`]*`|\\([^)]*\\)")
	fenceLine    = regexp.MustCompile("^\\s*```")
	listLead     = regexp.MustCompile(`^\s*(?:[-*+]\s+|\d+[.)]\s+|[a-hA-H][.)]\s+)?`)
	letterItem   = regexp.MustCompile(`^\s*[-*+]?\s*(?:\*\*)?([a-h])[.)](?:\*\*)?\s+(?:\*\*)?(.+)$`)
	multiAsk     = regexp.MustCompile(`(?i)\bwhich of\b|\bpick any\b|\bany of\b|\bwhich ones?\b|\bselect any\b`)
	yesNoStart   = regexp.MustCompile(`(?i)^(?:and |also |then |so )?(?:should|shall|do|does|did|can|could|will|would|is|are|was|may|want|ok|okay)\b`)
	approveOnly  = regexp.MustCompile(`(?i)^(?:and |so )?(?:do you |would you |can you )?(?:approve|ok to run|go ahead|proceed)\b|\bapprove (?:steps?|the plan|this plan|it|all|both|these|them|[a-z]+ \d)`)
)

// planQuestions reads the plan text for questions to answer, besides the
// approval itself. Fewer than one useful question returns nil (the plain
// card, or the single-choice card from planChoices, is used then).
func planQuestions(text string) []planQ {
	lines := strings.Split(text, "\n")
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
		if m := letterItem.FindStringSubmatch(mdNoise.Replace(l)); m != nil {
			opt := strings.TrimSpace(strings.TrimRight(m[2], "*_ "))
			if c := strings.IndexAny(opt, ":(—,"); c > 0 {
				opt = strings.TrimSpace(opt[:c])
			}
			letters = append(letters, m[1]+": "+shortLabel(opt, 50))
		}
	}
	var out []planQ
	seen := map[string]bool{}
	inFence = false
	for _, l := range lines {
		if fenceLine.MatchString(l) {
			inFence = !inFence
			continue
		}
		if inFence || !strings.Contains(l, "?") {
			continue
		}
		clean := strings.TrimSpace(mdNoise.Replace(l))
		clean = strings.TrimSpace(listLead.ReplaceAllString(clean, ""))
		clean = strings.TrimSpace(strings.TrimLeft(clean, "#>*_ "))
		for _, sent := range questionSentences(clean) {
			key := strings.ToLower(sent)
			if seen[key] {
				continue
			}
			seen[key] = true
			q, ok := classifyQuestion(sent, letters)
			if !ok {
				// The approval sentence: what follows on its line ("Yes
				// (recommended) or no?") belongs to it, not a new question.
				break
			}
			q.ID = len(out) + 1
			out = append(out, q)
		}
	}
	return out
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
				// The sentence starts after the last ". " / "! " / ": " before it.
				if m := qSentenceEnd.FindAllStringIndex(s[:len(s)-1], -1); len(m) > 0 {
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
	if multiAsk.MatchString(bare) && len(letters) >= 2 {
		q.Kind, q.Options = qMulti, letters
		return q, true
	}
	if _, opts, ok := parseChoices(q.Text); ok {
		for i, o := range opts {
			opts[i] = dropQuestionWord(o)
		}
		if isYesNoPair(opts) {
			q.Kind = qYesNo
			return q, true
		}
		q.Kind, q.Options, q.Recommended = qSingle, opts, recommendedOption(q.Text, len(opts))
		return q, true
	}
	// The approval itself is the card's main button, not a question here; a
	// question joined to it ("…steps 1–7, and do you also want X?") stays.
	if approveOnly.MatchString(strings.TrimSpace(bare)) {
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
	if yesNoStart.MatchString(strings.TrimSpace(bare)) {
		q.Kind = qYesNo
		return q, true
	}
	q.Kind = qText
	return q, true
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
				if t := strings.TrimSpace(a.Text); t != "" {
					val = t
				}
			}
		}
		fmt.Fprintf(&b, " %d. %s → %s.", i+1, shortLabel(q.Text, 90), val)
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
			for _, p := range a.Picks {
				if p < 0 || p >= len(q.Options) {
					return nil, fmt.Errorf("question %s: option %d is out of range", k, p)
				}
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
