package main

import (
	"strings"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

// The approval text is short (user 2026-11-23): number → picked option, no
// question text, no scope sentence.
func TestShortApprovalFormats(t *testing.T) {
	yes := true
	qs := []planQ{
		{ID: 1, Num: 2, Kind: qSingle, Text: "Which format?", Options: []string{"Per-question cards", "One list"}},
		{ID: 2, Num: 3, Kind: qSingle, Text: "What first?", Options: []string{"Design first", "Code first"}},
		{ID: 3, Num: 4, Kind: qText, Text: "Tag?"},
		{ID: 4, Num: 5, Kind: qYesNo, Text: "Ship?"},
	}
	ans := map[int]planAnswer{1: {Choice: 0}, 2: {Choice: 1}, 4: {Yes: &yes, Choice: -1}}
	cases := []struct{ name, got, want string }{
		{"plain", planGoWith(""), "Approved."},
		{"button", planGoWith("Ship as v1"), "Approved. Choice: Ship as v1."},
		{"mixed with blank", planGoAnswers(qs, ans), "Approved. Answers: 2 → Per-question cards; 3 → Code first; 4 → no answer; 5 → yes."},
		{"text quoted", planGoAnswers(qs[2:3], map[int]planAnswer{3: {Text: "v2\nx", Choice: -1}}), "Approved. Answers: 4 → \"v2 x\"."},
		{"changes", planGoChanges(planGoFull("", qs[:1], ans), " use blue "), "Approved. Answers: 2 → Per-question cards. Also: use blue"},
		{"choice and answers", planGoFull("Ship", qs[:1], ans), "Approved. Choice: Ship. Answers: 2 → Per-question cards."},
		{"stop", planStopMessage("", qs[:2], ans), "Not approved. Answers: 2 → Per-question cards; 3 → Code first."},
		{"stop with choice", planStopMessage("Later", qs[:1], ans), "Not approved. Choice: Later. Answers: 2 → Per-question cards."},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: %q, want %q", c.name, c.got, c.want)
		}
		if strings.Contains(c.got, "pre-approved") || strings.Contains(c.got, "Which format") {
			t.Errorf("%s: scope or question text in %q", c.name, c.got)
		}
	}
}

// The gate counts every new form and the old text, and nothing typed alike.
func TestShortApprovalGate(t *testing.T) {
	for _, text := range []string{
		"Approved.", planGoWith("Ship"), "Approved. Answers: 2 → A; 3 → no answer.", "Approved. Also: use blue",
		"Approved. Answers: 2 → A. Also: x", planGoLegacy, planGoLegacy + " Answers to your questions: 1. Q? → A.",
	} {
		if !isPlanGo(text) || !approves(text) {
			t.Errorf("not recognised: %q", text)
		}
		s := &deskSession{conv: chat.New()}
		if !approvesPlan(s, text) {
			t.Errorf("approvesPlan(%q) false", text)
		}
	}
	for _, text := range []string{"Approved. but not that", "Not approved.", "Not approved. Answers: 2 → A.", "Approved.. skip 2"} {
		if isPlanGo(text) {
			t.Errorf("counted: %q", text)
		}
	}
	// The retry prefix and peer messages still never approve.
	if approves(retryPrefix + "x") {
		t.Error("retry approves")
	}
}

func TestApprovalHintAndReminder(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.planFirst = true
		p := h.d.sessionPromptFor(s, false)
		if !strings.Contains(p, planApprovalHint) || !s.approvalHint || !strings.Contains(planApprovalHint, "end your turn instead of waiting") {
			t.Fatal("the plan-first session hint carries the scope once")
		}
		if planAsk(s) != planAskMessage {
			t.Fatal("no reminder when the hint is present")
		}
		s.approvalHint = false
		if got := planAsk(s); !strings.Contains(got, planGoReminder) {
			t.Fatalf("plan-first enabled mid-session gets the reminder: %q", got)
		}
		h.d.planFirst = false
		if strings.Contains(h.d.sessionPromptFor(s, false), planApprovalHint) {
			t.Fatal("no hint without plan-first")
		}
	})
}
