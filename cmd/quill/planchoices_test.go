package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

func TestPlanChoices(t *testing.T) {
	for _, c := range []struct {
		q    string
		opts int
		rec  int
	}{
		{"Should kmem 1.5.2 ship as a new Quill Desktop release v0.1.18 (recommended), or replace the files in the existing v0.1.17 release?", 2, 0},
		{"Do you approve steps 1–4? And should it ship as v0.1.18, or as a local commit only (recommended)?", 2, 1},
		{"Should these go out as v0.1.19, or do you want more changes first?", 2, -1},
		{"Do you approve steps 1–5, including the push and the public release?", 0, -1},
		{"Do you approve steps 1–6? Yes (recommended) or no?", 0, -1},
		{"Do you approve steps 1–4? And should I go ahead with t063026 afterwards, stopping after its step 2?", 0, -1},
	} {
		opts, rec := planChoices(c.q)
		if len(opts) != c.opts || rec != c.rec {
			t.Errorf("%q: %d options %q, recommended %d; want %d, %d", c.q, len(opts), opts, rec, c.opts, c.rec)
		}
	}
	if got := planGoWith("A new Quill Desktop release v0.1.18"); !strings.HasPrefix(got, planGoMessage) || !strings.HasSuffix(got, "Choice: A new Quill Desktop release v0.1.18.") {
		t.Fatalf("the approval carries the choice: %q", got)
	}
	s := &deskSession{conv: chat.New(), planWait: true}
	if !approvesPlan(s, planGoWith("but not that")) || !isPlanGo(planGoWith("x")) {
		t.Fatal("an approval with a choice still approves, whatever the choice says")
	}
}

func TestPlanCardAnswersTheChoice(t *testing.T) {
	h := newHarness(t)
	ask := func(q string) []string {
		var labels []string
		h.ui(func() {
			h.d.open(editsID)
			s := h.d.sessions[editsID]
			s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: "Plan:\n1. a\n2. b\n\n**" + q + "**"})
			it := s.conv.AddPlan()
			h.d.chat.show(s)
			deepWalk(h.d.chat.planUI(it), func(o fyne.CanvasObject) {
				if b, ok := o.(*widget.Button); ok && b.Visible() && b.Text != "Show as written" && b.Text != "Send answers" {
					labels = append(labels, b.Text)
				}
				if r, ok := o.(*widget.RadioGroup); ok {
					labels = append(labels, r.Options...)
				}
			})
		})
		return labels
	}
	h.ui(func() { h.d.open(editsID) })
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	// User 2026-11-22 (f4684ce): the picks are the question's own buttons,
	// with a "Don't run" of the card's own.
	rec := strings.Join(ask("Should kmem 1.5.2 ship as a new release v0.1.18 (recommended), or replace the v0.1.17 files?"), "|")
	if rec != "Kmem 1.5.2 ship as a new release v0.1.18 (recommended)|Replace the v0.1.17 files|Don't run" {
		t.Fatalf("recommended: the options as buttons, then Don't run: %s", rec)
	}
	all := strings.Join(ask("Should these go out as v0.1.19, or do you want more changes first?"), "|")
	if all != "These go out as v0.1.19|You want more changes first|Don't run" {
		t.Fatalf("no recommendation: one button per option: %s", all)
	}
	plain := strings.Join(ask("Do you approve steps 1–2?"), "|")
	if !strings.Contains(plain, "Approve plan & run all") {
		t.Fatalf("no choices: the usual card: %s", plain)
	}
	if os.Getenv("DESK_SHOTS") != "" {
		dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
		os.MkdirAll(dir, 0o755)
		ask("Should kmem 1.5.2 ship as a new release v0.1.18 (recommended), or replace the v0.1.17 files?")
		h.ui(func() { shot(t, h.w, dir, "plan-card-recommended.png") })
		ask("Should these go out as v0.1.19, or do you want more changes first?")
		h.ui(func() { shot(t, h.w, dir, "plan-card-options.png") })
	}
}
