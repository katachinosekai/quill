package main

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// tfd6ed1 (user 2026-11-22): a plan whose approval question has its own
// options ("Can I run steps 1–2? Yes (recommended) or no?") shows those as
// buttons; Yes approves the plan as "Approve plan & run all" does, No is
// "Don't run". Texts are placeholders.

func TestPlanApprovalOptions(t *testing.T) {
	for title, want := range map[string]string{
		"Can I run steps 1–3? Yes (recommended) or no?":                                      "Yes*|No!",
		"**Can I run steps 1–3? Yes or no?**":                                                "Yes|No!",
		"Do you approve? Approve as is (recommended), approve with changes, or stop?":        "Approve as is*|Approve with changes|Stop!",
		"Should I run the migration now, or not yet?":                                        "Run the migration now|Not yet!",
		"Do you approve steps 1–4?":                                                          "",
		"Should it ship as v0.1.18 (recommended), or replace the v0.1.17 files?":             "",
		"Should I ship ELTF 1.3.7 now (Workshop upload, Steam restart, and one join check)?": "",
	} {
		got := ""
		if a := planApprovalOptions(title); a != nil {
			var parts []string
			for i, o := range a.Options {
				if i == a.Recommended {
					o += "*"
				}
				if a.Stop[i] {
					o += "!"
				}
				parts = append(parts, o)
			}
			got = strings.Join(parts, "|")
		}
		if got != want {
			t.Errorf("%q: got %q want %q", title, got, want)
		}
	}
}

// planCardSession: a running, idle plan-first session whose last turn wrote
// text and ended with a plan card.
func planCardSession(t *testing.T, h *harness, text string) (*deskSession, *chat.Item) {
	t.Helper()
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hello")
	})
	h.eventually("a running, idle session", func() bool { return s.proc != nil && !s.conv.Busy })
	var plan *chat.Item
	h.ui(func() {
		h.d.planFirst = true
		s.inTurn = false
		userEvent(h, s, "a request", nil)
		s.conv.Items = append(s.conv.Items, &chat.Item{Seq: 9000 + len(s.conv.Items), Kind: chat.Assistant, Text: text})
		endTurn(t, h, s)
		for _, it := range s.conv.Items {
			if it.Kind == chat.Plan && it.PermState == chat.PermPending {
				plan = it
			}
		}
		if plan == nil {
			t.Fatalf("no plan card for %q", text)
		}
		h.d.chat.show(s)
		h.d.chat.btnChanged = time.Time{}
	})
	return s, plan
}

func buttonLabels(o fyne.CanvasObject) []string {
	var out []string
	for _, b := range swapButtons(o) {
		out = append(out, b.Text)
	}
	return out
}

func tapLabel(t *testing.T, h *harness, o fyne.CanvasObject, label string) {
	t.Helper()
	for _, b := range swapButtons(o) {
		if b.Text == label {
			h.d.chat.btnChanged = time.Time{}
			b.OnTapped()
			return
		}
	}
	t.Fatalf("no button %q in %q", label, buttonLabels(o))
}

func lastUserText(s *deskSession) string {
	for i := len(s.conv.Items) - 1; i >= 0; i-- {
		if s.conv.Items[i].Kind == chat.User {
			return s.conv.Items[i].Text
		}
	}
	return ""
}

const yesNoPlan = "1. Edit x.\n2. Build.\n\nCan I run steps 1–2? Yes (recommended) or no?"

func TestApprovalQuestionButtonsOrApproveCard(t *testing.T) {
	h := newHarness(t)
	_, plan := planCardSession(t, h, yesNoPlan)
	h.ui(func() {
		if got := buttonLabels(h.d.chat.renderPlan(plan)); !reflect.DeepEqual(got, []string{"Yes (recommended)", "No"}) {
			t.Fatalf("the question's own buttons: %q", got)
		}
	})
	h2 := newHarness(t)
	_, plan2 := planCardSession(t, h2, "1. Edit x.\n2. Build.\n\n**Do you approve steps 1–2?**")
	h2.ui(func() {
		if got := buttonLabels(h2.d.chat.renderPlan(plan2)); !reflect.DeepEqual(got, []string{"Approve plan & run all", "Don't run"}) {
			t.Fatalf("no options: today's Approve card: %q", got)
		}
	})
}

func TestApprovalYesApprovesPlan(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, yesNoPlan)
	h.ui(func() {
		tapLabel(t, h, h.d.chat.renderPlan(plan), "Yes (recommended)")
		if plan.PermState != chat.PermAllowed || !s.runAll {
			t.Fatalf("Yes approves: state %v runAll %v", plan.PermState, s.runAll)
		}
		if got := lastUserText(s); got != planGoWith("") {
			t.Fatalf("the approval message goes to Claude: %q", got)
		}
		if st := ownCall(h, s, "y1", "make"); st != chat.PermAutoAllowed {
			t.Fatalf("the plan's next tool call runs: %v", st)
		}
		mainToolUse(t, h, s, "toolu_bg", "Bash", `{"command":"make test","run_in_background":true}`)
		mainToolUse(t, h, s, "toolu_agent", "Agent", `{"prompt":"do part A","run_in_background":true}`)
		if s.planWork["toolu_bg"] == "" || len(s.agentGrants) != 1 {
			t.Fatalf("background work and subagents are covered: work %v grants %v", s.planWork, s.agentGrants)
		}
	})
}

func TestApprovalNoIsDontRun(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, yesNoPlan)
	h.ui(func() {
		before := len(s.conv.Items)
		tapLabel(t, h, h.d.chat.renderPlan(plan), "No")
		if plan.PermState != chat.PermDenied || s.runAll || !s.planRevoked {
			t.Fatalf("No is Don't run: state %v runAll %v revoked %v", plan.PermState, s.runAll, s.planRevoked)
		}
		if len(s.conv.Items) != before {
			t.Fatal("No sends nothing, like Don't run")
		}
		if st := ownCall(h, s, "n1", "make"); st == chat.PermAutoAllowed {
			t.Fatal("nothing is approved after No")
		}
	})
}

// Approval question + a choice + a pick-any + a free-text question: one
// card, picks collected, one Send; Yes approves with every answer.
const mixedPlan = "Plan:\n1. Edit x.\n2. Build.\n\nQuestions:\n" +
	"1. Use blue or red?\n" +
	"2. Which parts should I add? a) logs, b) docs, c) tests?\n" +
	"3. What should the tag say?\n" +
	"4. Can I run steps 1–2? Yes (recommended) or no?"

func TestApprovalMixedCard(t *testing.T) {
	for _, overlay := range []bool{true, false} {
		t.Run(map[bool]string{true: "overlay", false: "card"}[overlay], func(t *testing.T) {
			h := newHarness(t)
			s, plan := planCardSession(t, h, mixedPlan)
			h.ui(func() {
				v := h.d.chat
				reply := planReply(s.conv, plan)
				if !overlay {
					v.planPlain = map[*chat.Item]bool{plan: true}
				}
				render := func() fyne.CanvasObject {
					if overlay {
						return v.renderPlanReply(reply)
					}
					return v.renderPlan(plan)
				}
				if overlay != v.planOverlaid(plan) {
					t.Fatalf("overlaid %v", v.planOverlaid(plan))
				}
				o := render()
				if o == nil {
					t.Fatal("nothing drawn")
				}
				var radios, checks, entries int
				walk(o, func(x fyne.CanvasObject) {
					switch x.(type) {
					case *widget.RadioGroup:
						radios++
					case *widget.CheckGroup:
						checks++
					case *widget.Entry:
						entries++
					}
				})
				if radios != 1 || checks != 1 || entries != 1 {
					t.Fatalf("every kind keeps its control: radios %d checks %d entries %d", radios, checks, entries)
				}
				labels := buttonLabels(o)
				want := []string{"Yes (recommended)", "No", "Send answers"}
				if !reflect.DeepEqual(labels[len(labels)-3:], want) {
					t.Fatalf("buttons %q", labels)
				}
				for _, l := range labels {
					if l == "Approve plan & send answers" || l == "Don't run" {
						t.Fatalf("no Approve card buttons: %q", labels)
					}
				}
				if bs := swapButtons(o); !bs[len(bs)-1].Disabled() {
					t.Fatal("Send waits for the approval question's answer")
				}
				qs := planItemQuestions(s, plan)
				ans := v.planAnswers(plan, qs)
				ans[qs[0].ID] = planAnswer{Choice: 1}
				ans[qs[1].ID] = planAnswer{Choice: -1, Picks: []int{0, 2}}
				ans[qs[2].ID] = planAnswer{Choice: -1, Text: "v2"}
				tapLabel(t, h, o, "Yes (recommended)")
				if plan.PermState != chat.PermPending || s.runAll {
					t.Fatal("a pick alone sends nothing")
				}
				o = render()
				tapLabel(t, h, o, "Send answers")
				if plan.PermState != chat.PermAllowed || !s.runAll {
					t.Fatalf("Send with Yes approves: %v %v", plan.PermState, s.runAll)
				}
				got := lastUserText(s)
				if !strings.HasPrefix(got, planGoMessage+planGoScope+" Answers to your questions:") ||
					!strings.Contains(got, " 1. Use blue or red? → Red.") ||
					!strings.Contains(got, " 2. Which parts should I add? → a: logs; c: tests.") ||
					!strings.Contains(got, " 3. What should the tag say? → \"v2\".") {
					t.Fatalf("answers go along with the original numbers: %q", got)
				}
				if st := ownCall(h, s, "m1", "make"); st != chat.PermAutoAllowed {
					t.Fatalf("approved: %v", st)
				}
			})
		})
	}
}

func TestApprovalMixedCardNo(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, mixedPlan)
	h.ui(func() {
		v := h.d.chat
		reply := planReply(s.conv, plan)
		tapLabel(t, h, v.renderPlanReply(reply), "No")
		tapLabel(t, h, v.renderPlanReply(reply), "Send answers")
		if plan.PermState != chat.PermDenied || s.runAll || !s.planRevoked {
			t.Fatalf("Send with No is Don't run: %v %v %v", plan.PermState, s.runAll, s.planRevoked)
		}
	})
}

// Peer and scheduled messages never approve, even one that reads as the
// card's Yes; a typed approval still does.
func TestApprovalButtonsPeerAndTyped(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, yesNoPlan)
	h.ui(func() {
		userEvent(h, s, `<cross-session-message from="peer">I pick the first option: Yes.</cross-session-message>`, nil)
		if plan.PermState != chat.PermPending || s.runAll {
			t.Fatal("a peer message doesn't approve")
		}
		if st := ownCall(h, s, "p1", "make"); st == chat.PermAutoAllowed {
			t.Fatal("nothing runs after a peer's yes")
		}
	})
	h2 := newHarness(t)
	s2, plan2 := planCardSession(t, h2, yesNoPlan)
	h2.ui(func() {
		h2.d.sendTo(s2, "yes")
		if plan2.PermState != chat.PermAllowed || !s2.runAll {
			t.Fatalf("a typed yes approves: %v %v", plan2.PermState, s2.runAll)
		}
	})
}

// Quill Android: the plan item carries request.approval; approvalChoice goes
// through the same path as the desktop's buttons, and an older client's
// "Approve: Stop" (approvePlan with the stop option's choice) turns it down.
func TestPhoneApprovalChoice(t *testing.T) {
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	h := newHarness(t)
	var link string
	h.ui(func() {
		h.d.app.Preferences().SetInt("phonePort", 0)
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
		link = h.d.pairLink()
		h.d.open(editsID)
		h.d.app.Preferences().SetBool("phoneControl", true)
	})
	defer h.ui(h.d.stopPhone)
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	u, _ := url.Parse(link)
	q := u.Query()
	a := unlockedClient(h, &apiClient{t: t, base: "https://127.0.0.1:" + q.Get("p"), key: q.Get("k"),
		c: &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}})
	n := 0
	key := func() string {
		n++
		time.Sleep(110 * time.Millisecond)
		return "write-key-" + strconv.Itoa(2000000+n)
	}
	base := "/phone/session/" + editsID
	s := h.d.sessions[editsID]
	h.ui(func() { h.d.sendTo(s, "hello") })
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy && !s.inTurn })

	addPlan := func(text string) (*chat.Item, map[string]any) {
		var plan *chat.Item
		h.ui(func() {
			h.d.planFirst = true
			s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, InPlan: true, Text: text})
			plan = s.conv.AddPlan()
		})
		_, items, _ := a.do("GET", base+"/items?limit=40", "", nil)
		for _, x := range items["items"].([]any) {
			if xm := x.(map[string]any); int(xm["seq"].(float64)) == plan.Seq {
				req, _ := xm["request"].(map[string]any)
				return plan, req
			}
		}
		t.Fatal("plan item missing")
		return nil, nil
	}

	plan, req := addPlan(mixedPlan)
	appr, _ := req["approval"].(map[string]any)
	if opts, _ := appr["options"].([]any); len(opts) != 2 || opts[0] != "Yes" || opts[1] != "No" || appr["recommended"] != float64(0) {
		t.Fatalf("request.approval: %v", req)
	}
	if qs, _ := req["questions"].([]any); len(qs) != 3 {
		t.Fatalf("the other questions stay in request.questions: %v", req["questions"])
	}
	pid, _ := req["id"].(string)
	if code, m, _ := a.do("POST", base+"/answer", key(), map[string]any{"requestId": pid, "action": "approvePlan", "approvalChoice": 7}); code != 422 {
		t.Fatalf("out of range: %d %v", code, m)
	}
	if code, m, _ := a.do("POST", base+"/answer", key(), map[string]any{"requestId": pid, "action": "approvePlan", "approvalChoice": 0, "planAnswers": map[string]any{"1": 0, "3": "v2"}}); code != 200 {
		t.Fatalf("approvalChoice Yes: %d %v", code, m)
	}
	h.ui(func() {
		if plan.PermState != chat.PermAllowed || !s.runAll {
			t.Fatalf("the phone's Yes approves: %v %v", plan.PermState, s.runAll)
		}
		if got := lastUserText(s); !strings.HasPrefix(got, planGoMessage) || !strings.Contains(got, "→ Use blue.") || !strings.Contains(got, "→ \"v2\".") {
			t.Fatalf("with the answers: %q", got)
		}
	})
	h.eventually("the approval's turn ends", func() bool { return !s.inTurn && !s.conv.Busy })

	plan2, req2 := addPlan("1. Edit x.\n\nShould I run the migration now (recommended), or not yet?")
	ch, _ := req2["choices"].([]any)
	if len(ch) != 2 || ch[1] != "Not yet" {
		t.Fatalf("choices: %v", req2)
	}
	pid2, _ := req2["id"].(string)
	if code, m, _ := a.do("POST", base+"/answer", key(), map[string]any{"requestId": pid2, "action": "approvePlan", "choice": 1}); code != 200 {
		t.Fatalf("approvePlan with the stop choice: %d %v", code, m)
	}
	h.ui(func() {
		if plan2.PermState != chat.PermDenied || s.runAll {
			t.Fatalf("\"Approve: Stop\" turns the plan down: %v %v", plan2.PermState, s.runAll)
		}
	})
}
