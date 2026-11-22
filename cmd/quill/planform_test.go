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

// A plan with several questions gets a control for each; Approve sends every
// answer with the approval.
func TestPlanCardQuestionForm(t *testing.T) {
	h := newHarness(t)
	h.ui(func() { h.d.open(editsID) })
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	var plan *chat.Item
	var radios []*widget.RadioGroup
	var checks []*widget.CheckGroup
	var entries []*widget.Entry
	var approve *widget.Button
	h.ui(func() {
		s := h.d.sessions[editsID]
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, InPlan: true, Text: "Plan:\n1. Bundle kmem.\n2. Test.\n\n" +
			"Improvements:\n- a. Plan-first\n- b. Memory\n- c. Pair a phone\n\n" +
			"1. Should v0.1.20 ship now (recommended), or wait for Quill Android's card?\n2. Should rule 21 stay as it is?\n3. Which of a–c should I add?\n4. What should the title say?\n\n**Do you approve steps 1–2?**"})
		plan = s.conv.AddPlan()
		h.d.chat.show(s)
		deepWalk(h.d.chat.views[plan.Seq].holder, func(o fyne.CanvasObject) {
			switch w := o.(type) {
			case *widget.RadioGroup:
				radios = append(radios, w)
			case *widget.CheckGroup:
				checks = append(checks, w)
			case *widget.Entry:
				entries = append(entries, w)
			case *widget.Button:
				if w.Text == "Approve plan & send answers" {
					approve = w
				}
			}
		})
	})
	if len(radios) != 2 || len(checks) != 1 || len(entries) != 1 || approve == nil {
		t.Fatalf("controls: %d radio groups, %d check groups, %d text fields, approve %v", len(radios), len(checks), len(entries), approve != nil)
	}
	if radios[0].Selected != "v0.1.20 ship now" {
		t.Fatalf("the recommended option starts picked: %q", radios[0].Selected)
	}
	if os.Getenv("DESK_SHOTS") != "" {
		dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
		os.MkdirAll(dir, 0o755)
		h.ui(func() { h.w.Resize(fyne.NewSize(1280, 1000)); h.d.chat.scrollBottom() })
		h.ui(func() { shot(t, h.w, dir, "plan-card-questions.png") })
	}
	var sent string
	h.ui(func() {
		radios[1].SetSelected("No")
		checks[0].SetSelected([]string{"a: Plan-first", "c: Pair a phone"})
		entries[0].SetText("Welcome to Quill")
		ans := h.d.chat.planAns[plan]
		sent = planGoAnswers(planQuestions(planItemText(h.d.sessions[editsID], plan)), ans)
	})
	for _, want := range []string{"→ v0.1.20 ship now.", "Should rule 21 stay as it is? → no.", "→ a: Plan-first; c: Pair a phone.", "→ Welcome to Quill."} {
		if !strings.Contains(sent, want) {
			t.Errorf("Approve's message lacks %q:\n%s", want, sent)
		}
	}
}
