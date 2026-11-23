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
		"Should it ship as v0.1.18 (recommended), or replace the v0.1.17 files?":             "Ship as v0.1.18*|Replace the v0.1.17 files|+",
		"Do you approve steps 1–3, or should I change something first?":                      "",
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
			if a.DontRun {
				parts = append(parts, "+")
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

// planUI: what the user sees for a plan: its reply with the controls in place,
// or the card when the overlay can't be drawn.
func (v *chatView) planUI(plan *chat.Item) fyne.CanvasObject {
	if v.s != nil && !v.planPlain[plan] {
		if r := planReply(v.s.conv, plan); r != nil {
			if o := v.renderPlanReply(r); o != nil {
				return o
			}
		}
	}
	return v.renderPlan(plan)
}

func buttonLabels(o fyne.CanvasObject) []string {
	var out []string
	for _, b := range swapButtons(o) {
		if b.Text == "Show as written" {
			continue
		}
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
		if got := buttonLabels(h.d.chat.planUI(plan)); !reflect.DeepEqual(got, []string{"Yes (recommended)", "No"}) {
			t.Fatalf("the question's own buttons: %q", got)
		}
	})
	h2 := newHarness(t)
	_, plan2 := planCardSession(t, h2, "1. Edit x.\n2. Build.\n\n**Do you approve steps 1–2?**")
	h2.ui(func() {
		if got := buttonLabels(h2.d.chat.planUI(plan2)); !reflect.DeepEqual(got, []string{"Approve plan & run all", "Don't run"}) {
			t.Fatalf("no options: today's Approve card: %q", got)
		}
	})
}

func TestApprovalYesApprovesPlan(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, yesNoPlan)
	h.ui(func() {
		tapLabel(t, h, h.d.chat.planUI(plan), "Yes (recommended)")
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
		tapLabel(t, h, h.d.chat.planUI(plan), "No")
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
				if !strings.HasPrefix(got, planGoMessage+" Answers: 1 → Red; 2 → a: logs, c: tests; 3 → \"v2\".") {
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
		qs := planItemQuestions(s, plan)
		v.planAnswers(plan, qs)[qs[2].ID] = planAnswer{Choice: -1, Text: "v2"}
		tapLabel(t, h, v.renderPlanReply(reply), "No")
		tapLabel(t, h, v.renderPlanReply(reply), "Send answers")
		if plan.PermState != chat.PermDenied || s.runAll {
			t.Fatalf("Send with No is Don't run: %v %v", plan.PermState, s.runAll)
		}
		// f4684ce: the other answers still go to Claude, in one reply.
		got := lastUserText(s)
		if !strings.HasPrefix(got, planStopText+" Answers: 1 → no answer; 2 → no answer; 3 → \"v2\".") {
			t.Fatalf("the answers go along: %q", got)
		}
		if approvesPlan(s, got) || plan.PermState != chat.PermDenied {
			t.Fatal("the stop reply is no approval")
		}
		if st := ownCall(h, s, "n2", "make"); st == chat.PermAutoAllowed {
			t.Fatal("nothing runs after No")
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
	if code, m, _ := a.do("POST", base+"/answer", key(), map[string]any{"requestId": pid, "action": "approvePlan", "approvalChoice": 0, "planAnswers": map[string]any{"1": 0, "3": "v2"}, "text": "keep it small"}); code != 200 {
		t.Fatalf("approvalChoice Yes: %d %v", code, m)
	}
	h.ui(func() {
		if plan.PermState != chat.PermAllowed || !s.runAll {
			t.Fatalf("the phone's Yes approves: %v %v", plan.PermState, s.runAll)
		}
		if got := lastUserText(s); !strings.HasPrefix(got, planGoMessage) || !strings.Contains(got, "1 → Use blue;") || !strings.Contains(got, "3 → \"v2\".") || !strings.HasSuffix(got, " Also: keep it small") {
			t.Fatalf("with the answers and the changes: %q", got)
		}
	})
	h.eventually("the approval's turn ends", func() bool { return !s.inTurn && !s.conv.Busy })

	// No with answers: refused, and the answers go along (f4684ce).
	plan3, req3 := addPlan(mixedPlan)
	pid3, _ := req3["id"].(string)
	if code, m, _ := a.do("POST", base+"/answer", key(), map[string]any{"requestId": pid3, "action": "approvePlan", "approvalChoice": 1, "planAnswers": map[string]any{"3": "v2"}}); code != 200 {
		t.Fatalf("approvalChoice No with answers: %d %v", code, m)
	}
	h.ui(func() {
		if got := lastUserText(s); plan3.PermState != chat.PermDenied || s.runAll || !strings.HasPrefix(got, planStopText) || !strings.Contains(got, "→ \"v2\".") {
			t.Fatalf("the phone's No: %v %v %q", plan3.PermState, s.runAll, got)
		}
	})
	h.eventually("that turn ends", func() bool { return !s.inTurn && !s.conv.Busy })

	// A pick without a stop option: request.approval matches request.choices
	// and says the card has its own Don't run.
	_, req4 := addPlan(shipPlan)
	ap4, _ := req4["approval"].(map[string]any)
	if !reflect.DeepEqual(ap4["options"], req4["choices"]) || ap4["dontRun"] != true || ap4["recommended"] != req4["recommended"] {
		t.Fatalf("approval and choices agree: %v", req4)
	}
	h.ui(func() {
		for _, it := range s.conv.PendingPermissions() {
			if it.Kind == chat.Plan {
				it.PermState = chat.PermCancelled
			}
		}
	})

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

// directPlan: a running, idle plan-first session with a plan card over text
// (made as the turn's end makes it, whatever the question asks).
func directPlan(t *testing.T, h *harness, text string) (*deskSession, *chat.Item) {
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
		s.conv.MarkPlanText()
		plan = s.conv.AddPlan()
		h.d.chat.show(s)
		h.d.chat.btnChanged = time.Time{}
	})
	return s, plan
}

const shipPlan = "Plan:\n1. Edit x.\n2. Build.\n\n**Should it ship as v0.1.18 (recommended), or replace the v0.1.17 files?**"

// f4684ce (user 2026-11-22): a pick between things to approve gets the new
// buttons too, with a "Don't run" of its own; a pick approves with it.
func TestApprovalPicksWithoutStop(t *testing.T) {
	h := newHarness(t)
	s, plan := directPlan(t, h, shipPlan)
	h.ui(func() {
		o := h.d.chat.planUI(plan)
		if got := buttonLabels(o); !reflect.DeepEqual(got, []string{"Ship as v0.1.18 (recommended)", "Replace the v0.1.17 files", "Don't run"}) {
			t.Fatalf("buttons %q", got)
		}
		tapLabel(t, h, o, "Replace the v0.1.17 files")
		if plan.PermState != chat.PermAllowed || !s.runAll || lastUserText(s) != planGoWith("Replace the v0.1.17 files") {
			t.Fatalf("the pick approves with it: %v %v %q", plan.PermState, s.runAll, lastUserText(s))
		}
		if st := ownCall(h, s, "s1", "make"); st != chat.PermAutoAllowed {
			t.Fatalf("approved: %v", st)
		}
	})
	h2 := newHarness(t)
	s2, plan2 := directPlan(t, h2, shipPlan)
	h2.ui(func() {
		n := len(s2.conv.Items)
		tapLabel(t, h2, h2.d.chat.planUI(plan2), "Don't run")
		if plan2.PermState != chat.PermDenied || s2.runAll || len(s2.conv.Items) != n {
			t.Fatalf("Don't run, nothing sent without questions: %v %v %d", plan2.PermState, s2.runAll, len(s2.conv.Items)-n)
		}
	})
}

// The same pick with other questions: picked and changeable, one Send; the
// card's "Don't run" sends the answers with the refusal.
func TestApprovalPicksWithoutStopMixed(t *testing.T) {
	text := "Plan:\n1. Edit x.\n2. Build.\n\n1. Use blue or red?\n2. What should the tag say?\n\n**Should it ship as v0.1.18 (recommended), or replace the v0.1.17 files?**"
	h := newHarness(t)
	s, plan := directPlan(t, h, text)
	h.ui(func() {
		v := h.d.chat
		v.planPlain = map[*chat.Item]bool{plan: true}
		qs := planItemQuestions(s, plan)
		if len(qs) != 2 {
			t.Fatalf("questions %+v", qs)
		}
		ans := v.planAnswers(plan, qs)
		ans[qs[1].ID] = planAnswer{Choice: -1, Text: "v2"}
		o := v.planUI(plan)
		if got := buttonLabels(o); !reflect.DeepEqual(got, []string{"Ship as v0.1.18 (recommended)", "Replace the v0.1.17 files", "Don't run", "Send answers"}) {
			t.Fatalf("buttons %q", got)
		}
		tapLabel(t, h, o, "Replace the v0.1.17 files")
		tapLabel(t, h, v.planUI(plan), "Ship as v0.1.18 (recommended)") // changed
		if plan.PermState != chat.PermPending {
			t.Fatal("picks wait for Send")
		}
		tapLabel(t, h, v.planUI(plan), "Send answers")
		got := lastUserText(s)
		if plan.PermState != chat.PermAllowed || !strings.HasPrefix(got, planGoWith("Ship as v0.1.18")+" Answers: 1 → no answer; 2 → \"v2\".") {
			t.Fatalf("approved with the pick and the answers: %v %q", plan.PermState, got)
		}
	})
	h2 := newHarness(t)
	s2, plan2 := directPlan(t, h2, text)
	h2.ui(func() {
		v := h2.d.chat
		v.planPlain = map[*chat.Item]bool{plan2: true}
		qs := planItemQuestions(s2, plan2)
		v.planAnswers(plan2, qs)[qs[1].ID] = planAnswer{Choice: -1, Text: "v2"}
		tapLabel(t, h2, v.planUI(plan2), "Don't run")
		got := lastUserText(s2)
		if plan2.PermState != chat.PermDenied || s2.runAll || !strings.HasPrefix(got, planStopText+" Answers: ") || !strings.Contains(got, "→ \"v2\".") {
			t.Fatalf("Don't run sends the answers: %v %v %q", plan2.PermState, s2.runAll, got)
		}
	})
}

func TestPlanGoChanges(t *testing.T) {
	if got := planGoChanges(planGoWith(""), "  "); got != planGoWith("") {
		t.Fatalf("no text, no change: %q", got)
	}
	got := planGoChanges(planGoWith("Yes"), " use blue ")
	if !strings.HasSuffix(got, " Also: use blue") || !isPlanGo(got) {
		t.Fatalf("%q", got)
	}
}

func TestPhonePlanApproveWithChanges(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, yesNoPlan)
	h.ui(func() {
		h.d.answerPlanApproval(s, plan, planItemApproval(s, plan), 0, nil, nil, "also add a test")
		if plan.PermState != chat.PermAllowed || !s.runAll {
			t.Fatalf("still approves: %v %v", plan.PermState, s.runAll)
		}
		if got := lastUserText(s); !isPlanGo(got) || !strings.HasSuffix(got, " Also: also add a test") {
			t.Fatalf("%q", got)
		}
	})
	h2 := newHarness(t)
	s2, plan2 := planCardSession(t, h2, yesNoPlan)
	h2.ui(func() {
		h2.d.answerPlanWith(s2, plan2, true, "", "rename it")
		if got := lastUserText(s2); plan2.PermState != chat.PermAllowed || !strings.HasSuffix(got, " Also: rename it") {
			t.Fatalf("%v %q", plan2.PermState, got)
		}
	})
}

func TestNearApproval(t *testing.T) {
	for _, y := range []string{"Appeoved", "yes", "ok go", "Aproved", "appeoved", "approvd please", "go ahead", "Yeah, do it"} {
		if !nearApproval(y) {
			t.Errorf("%q should look like an approval attempt", y)
		}
	}
	for _, n := range []string{"improve the plan", "what about step 2", "go check the tests", "No", "add a test", "", "above all keep it small"} {
		if nearApproval(n) {
			t.Errorf("%q should not", n)
		}
	}
}

func TestTypedNearMissGetsNotice(t *testing.T) {
	count := func(s *deskSession) int {
		n := 0
		for _, it := range s.conv.Items {
			if it.Kind == chat.Notice && it.Text == notCountedNotice {
				n++
			}
		}
		return n
	}
	h := newHarness(t)
	s, plan := planCardSession(t, h, yesNoPlan)
	h.ui(func() {
		h.d.sendTo(s, "Appeoved")
		if count(s) != 1 {
			for _, it := range s.conv.Items {
				t.Logf("%v %q %v", it.Kind, it.Text, it.PermState)
			}
			t.Fatalf("notice expected, plan %v pending %v", plan.PermState, hasPendingPlan(s))
		}
		if s.runAll || plan.PermState == chat.PermAllowed {
			t.Fatal("a typo never approves")
		}
		if got := lastUserText(s); got != "Appeoved" {
			t.Fatalf("the message is still sent: %q", got)
		}
	})
	h2 := newHarness(t)
	s2, _ := planCardSession(t, h2, yesNoPlan)
	h2.ui(func() {
		h2.d.sendTo(s2, "yes")
		if count(s2) != 0 {
			t.Fatal("a counted yes gets no notice")
		}
	})
	h3 := newHarness(t)
	var s3 *deskSession
	h3.ui(func() {
		s3 = h3.d.createSession(h3.proj, "default", "default")
		h3.d.planFirst = true
		h3.d.sendTo(s3, "Appeoved")
		if count(s3) != 0 {
			t.Fatal("no notice without a pending plan")
		}
	})
}
