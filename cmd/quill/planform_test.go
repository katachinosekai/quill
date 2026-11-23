package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// planFormText: a plan whose reply ends with numbered questions.
const planFormText = "Plan:\n1. Bundle kmem.\n2. Test.\n\n" +
	"Improvements:\n- a. Plan-first\n- b. Memory\n- c. Pair a phone\n\n" +
	"1. Should v0.1.20 ship now (recommended), or wait for Quill Android's card?\n2. Should rule 21 stay as it is?\n3. Which of a–c should I add?\n4. What should the title say?\n\n**Do you approve steps 1–2?**"

type planControls struct {
	radios  []*widget.RadioGroup
	checks  []*widget.CheckGroup
	entries []*widget.Entry
	buttons map[string]*widget.Button
	texts   []string
}

func findPlanControls(o fyne.CanvasObject) planControls {
	c := planControls{buttons: map[string]*widget.Button{}}
	deepWalk(o, func(o fyne.CanvasObject) {
		switch w := o.(type) {
		case *widget.RadioGroup:
			c.radios = append(c.radios, w)
		case *widget.CheckGroup:
			c.checks = append(c.checks, w)
		case *widget.Entry:
			c.entries = append(c.entries, w)
		case *widget.Button:
			c.buttons[w.Text] = w
		case *widget.RichText:
			c.texts = append(c.texts, w.String())
		case *widget.Label:
			c.texts = append(c.texts, w.Text)
		}
	})
	return c
}

// A plan with several numbered questions shows each control in the reply,
// under its question; there is no separate card (user 2026-11-22).
func TestPlanCardQuestionForm(t *testing.T) {
	h := newHarness(t)
	h.ui(func() { h.d.open(editsID) })
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	var plan, reply *chat.Item
	var c planControls
	planShown := true
	h.ui(func() {
		s := h.d.sessions[editsID]
		reply = &chat.Item{Kind: chat.Assistant, InPlan: true, Text: planFormText}
		s.conv.Items = append(s.conv.Items, reply)
		plan = s.conv.AddPlan()
		h.d.chat.show(s)
		c = findPlanControls(h.d.chat.views[reply.Seq].holder)
		planShown = h.d.chat.views[plan.Seq].holder.Visible()
	})
	if planShown {
		t.Fatal("the overlaid plan draws no separate card")
	}
	approve := c.buttons["Approve plan & send answers"]
	if len(c.radios) != 2 || len(c.checks) != 1 || len(c.entries) != 1 || approve == nil || c.buttons["Don't run"] == nil || c.buttons["Show as written"] == nil {
		t.Fatalf("controls: %d radio groups, %d check groups, %d text fields, buttons %v", len(c.radios), len(c.checks), len(c.entries), c.buttons)
	}
	all := strings.Join(c.texts, "\n")
	for _, want := range []string{"Improvements:", "2.\u00a0Should rule 21 stay as it is?", "4.\u00a0What should the title say?"} {
		if !strings.Contains(all, want) {
			t.Errorf("the reply keeps %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "Do you approve steps") {
		t.Errorf("the approval question is the Approve button, not text:\n%s", all)
	}
	// The recommended option is picked and says so (user 2026-11-22); the
	// answer sent is still the bare option (checked below).
	if c.radios[0].Selected != "v0.1.20 ship now (recommended)" {
		t.Fatalf("the recommended option starts picked: %q", c.radios[0].Selected)
	}
	if os.Getenv("DESK_SHOTS") != "" {
		dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
		os.MkdirAll(dir, 0o755)
		h.ui(func() { h.w.Resize(fyne.NewSize(1280, 1700)); h.d.chat.scrollBottom() })
		h.ui(func() { shot(t, h.w, dir, "plan-overlay.png") })
	}
	var want string
	h.ui(func() {
		c.radios[1].SetSelected("No")
		c.checks[0].SetSelected([]string{"a: Plan-first", "c: Pair a phone"})
		c.entries[0].SetText("Welcome to Quill Desktop")
		s := h.d.sessions[editsID]
		want = planGoAnswers(planItemQuestions(s, plan), h.d.chat.planAns[plan])
	})
	for _, w := range []string{"→ v0.1.20 ship now;", "→ no;", "→ a: Plan-first, c: Pair a phone;", "→ \"Welcome to Quill Desktop\"."} {
		if !strings.Contains(want, w) {
			t.Errorf("Approve's message lacks %q:\n%s", w, want)
		}
	}
	h.ui(func() { h.d.chat.btnChanged = time.Time{}; approve.OnTapped() })
	h.eventually("Approve sends the answers", func() bool {
		for _, it := range h.d.sessions[editsID].conv.Items {
			if it.Kind == chat.User && it.Text == want {
				return true
			}
		}
		return false
	})
	// Approved: the reply is plain again and the card shows its state.
	h.ui(func() {
		c = findPlanControls(h.d.chat.views[reply.Seq].holder)
		if len(c.radios)+len(c.checks)+len(c.entries) != 0 || !h.d.chat.views[plan.Seq].holder.Visible() {
			t.Errorf("after approval the reply has no controls (%d) and the card shows", len(c.radios)+len(c.checks)+len(c.entries))
		}
	})
}

// The toggle shows the reply as written plus today's card with the parsed
// questions; back again, the picks are kept.
func TestPlanOverlayToggle(t *testing.T) {
	h := newHarness(t)
	h.ui(func() { h.d.open(editsID) })
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	var plan, reply *chat.Item
	h.ui(func() {
		s := h.d.sessions[editsID]
		reply = &chat.Item{Kind: chat.Assistant, InPlan: true, Text: planFormText}
		s.conv.Items = append(s.conv.Items, reply)
		plan = s.conv.AddPlan()
		h.d.chat.show(s)
		c := findPlanControls(h.d.chat.views[reply.Seq].holder)
		c.radios[1].SetSelected("No")
		c.entries[0].SetText("Draft title")
		c.buttons["Show as written"].OnTapped()

		r := findPlanControls(h.d.chat.views[reply.Seq].holder)
		if len(r.radios)+len(r.checks)+len(r.entries) != 0 || r.buttons["Answer in place"] == nil {
			t.Fatalf("plain view: the reply has no controls and a way back: %v", r.buttons)
		}
		if !strings.Contains(strings.Join(r.texts, "\n"), "Do you approve steps 1–2?") {
			t.Error("plain view shows the reply as written")
		}
		if !h.d.chat.views[plan.Seq].holder.Visible() {
			t.Fatal("plain view shows the card")
		}
		card := findPlanControls(h.d.chat.views[plan.Seq].holder)
		if len(card.radios) != 2 || len(card.entries) != 1 || card.radios[1].Selected != "No" || card.entries[0].Text != "Draft title" || card.buttons["Approve plan & send answers"] == nil {
			t.Fatalf("the card shows the parsed questions with the same answers: %d radios, %d entries", len(card.radios), len(card.entries))
		}
		card.radios[0].SetSelected("Wait for Quill Android's card")
	})
	if os.Getenv("DESK_SHOTS") != "" {
		dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
		os.MkdirAll(dir, 0o755)
		h.ui(func() { h.w.Resize(fyne.NewSize(1280, 1700)); h.d.chat.scrollBottom() })
		h.ui(func() { shot(t, h.w, dir, "plan-overlay-plain.png") })
	}
	h.ui(func() {
		findPlanControls(h.d.chat.views[reply.Seq].holder).buttons["Answer in place"].OnTapped()
		if h.d.chat.views[plan.Seq].holder.Visible() {
			t.Fatal("back in the overlay the card is gone")
		}
		c := findPlanControls(h.d.chat.views[reply.Seq].holder)
		if len(c.radios) != 2 || c.radios[0].Selected != "Wait for Quill Android's card" || c.radios[1].Selected != "No" || c.entries[0].Text != "Draft title" {
			t.Fatalf("picks survive the toggle: %d radios", len(c.radios))
		}
	})
}

// Without numbered questions, or with the plan text elsewhere, the plan keeps
// today's card.
func TestPlanOverlayFallback(t *testing.T) {
	h := newHarness(t)
	h.ui(func() { h.d.open(editsID) })
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	for _, tc := range []struct{ name, plan, last string }{
		{"unnumbered", "Plan:\n1. Bundle kmem.\n\nShould rule 21 stay as it is? And what should the title say?\n\n**Do you approve step 1?**", ""},
		{"plan elsewhere", planFormText, "Running the checks first."},
	} {
		h.ui(func() {
			s := h.d.sessions[editsID]
			s.conv.AddUser("next")
			s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, InPlan: true, Text: tc.plan})
			reply := s.conv.Items[len(s.conv.Items)-1]
			if tc.last != "" {
				reply = &chat.Item{Kind: chat.Assistant, Text: tc.last}
				s.conv.Items = append(s.conv.Items, reply)
			}
			plan := s.conv.AddPlan()
			h.d.chat.show(s)
			if len(planItemQuestions(s, plan)) == 0 {
				t.Fatalf("%s: the plan has questions", tc.name)
			}
			r := findPlanControls(h.d.chat.views[reply.Seq].holder)
			card := findPlanControls(h.d.chat.views[plan.Seq].holder)
			if len(r.radios)+len(r.entries) != 0 || r.buttons["Show as written"] != nil || card.buttons["Approve plan & send answers"] == nil {
				t.Errorf("%s: today's card, nothing in the reply (%d controls, buttons %v)", tc.name, len(r.radios)+len(r.entries), r.buttons)
			}
		})
	}
}
