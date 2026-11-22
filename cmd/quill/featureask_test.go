package main

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// radioWith: the radio group in the top dialog that offers label.
func radioWith(h *harness, label string) *widget.RadioGroup {
	var r *widget.RadioGroup
	if top := h.w.Canvas().Overlays().Top(); top != nil {
		deepWalk(top, func(o fyne.CanvasObject) {
			if g, ok := o.(*widget.RadioGroup); ok && contains(g.Options, label) {
				r = g
			}
		})
	}
	return r
}

func tapDialogButton(t *testing.T, h *harness, text string) {
	t.Helper()
	var btn *widget.Button
	deepWalk(h.w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		if b, ok := o.(*widget.Button); ok && b.Text == text {
			btn = b
		}
	})
	if btn == nil {
		t.Fatalf("the dialog has a %q button", text)
	}
	test.Tap(btn)
}

func hasLabel(o fyne.CanvasObject, text string) bool {
	found := false
	deepWalk(o, func(o fyne.CanvasObject) {
		if l, ok := o.(*widget.Label); ok && strings.Contains(l.Text, text) {
			found = true
		}
	})
	return found
}

func closeOverlays(h *harness) {
	for h.w.Canvas().Overlays().Top() != nil {
		h.w.Canvas().Overlays().Remove(h.w.Canvas().Overlays().Top())
	}
}

// withQuestions swaps the registry and the defaults for the test.
func withQuestions(t *testing.T, qs ...featureQuestion) {
	old, oldDef := featureQuestions, prefDefaults
	featureQuestions, prefDefaults = qs, map[string]bool{prefCoordinator: true}
	t.Cleanup(func() { featureQuestions, prefDefaults = old, oldDef })
}

func testQuestion(id string, kind featureKind) featureQuestion {
	return featureQuestion{ID: id, Kind: kind, Title: "Title " + id, Question: "Question " + id + "?", Explain: "Explain " + id,
		Choices: []featureChoice{{"Keep " + id, true}, {"Off " + id, false}}, Pref: "pref_" + id, Since: "v0.0.1"}
}

func TestWelcomeAsksCoordinator(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KMEM_CONFIG", "")
	h := newHarness(t)
	for _, pick := range []string{welcomeCoordinate, welcomeOneSession} {
		h.ui(func() {
			p := h.d.app.Preferences()
			p.SetBool(prefWelcomeSeen, false)
			p.RemoveValue(prefCoordinator)
			p.RemoveValue(prefCoordinatorAsked)
			h.d.showWelcome(nil)
			r := radioWith(h, welcomeCoordinate)
			if r == nil || r.Selected != welcomeCoordinate {
				t.Fatalf("the Welcome asks how Claude should work, coordinate preselected: %v", r)
			}
			if top := h.w.Canvas().Overlays().Top(); !hasLabel(top, "4. How should Claude work?") || !hasLabel(top, coordinatorExplain) || !hasLabel(top, coordinatorOnline) || !hasLabel(top, "8. Reach Quill") {
				t.Fatal("the Welcome step has its title, the exact explanation, and the steps after it move down")
			}
			r.SetSelected(pick)
			tapDialogButton(t, h, "Done")
			if on := prefBool(p, prefCoordinator); on != (pick == welcomeCoordinate) || !p.Bool(prefCoordinatorAsked) || !p.Bool(prefCoordinator+"Set") {
				t.Fatalf("%s: coordinator %v, asked %v", pick, on, p.Bool(prefCoordinatorAsked))
			}
			if len(h.d.dueFeatureQuestions()) != 0 {
				t.Fatal("after the Welcome nothing is due")
			}
			closeOverlays(h)
		})
	}
}

func TestWelcomeMarksFeatureQuestionsAnswered(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KMEM_CONFIG", "")
	h := newHarness(t)
	withQuestions(t, testQuestion("a", featureNew), testQuestion("b", featureChanged), testQuestion("n", featureNotice))
	h.ui(func() {
		h.d.applyWelcome(welcomeChoices{Lang: "en", Features: map[string]int{"a": 1}})
		p := h.d.app.Preferences()
		if !p.Bool("aAsked") || !p.Bool("bAsked") || !p.Bool("nAsked") || p.Bool("pref_a") || !p.Bool("pref_aSet") || len(h.d.dueFeatureQuestions()) != 0 {
			t.Fatal("the Welcome answers its questions and marks every entry answered")
		}
	})
}

func TestFeatureQuestionsOnce(t *testing.T) {
	h := newHarness(t)
	withQuestions(t, testQuestion("old", featureNew), testQuestion("mid", featureNew), testQuestion("gone", featureNew), testQuestion("new", featureNew))
	featureQuestions[2].Retired = true
	h.ui(func() {
		p := h.d.app.Preferences()
		p.SetBool(prefWelcomeSeen, true)
		p.SetBool("midAsked", true)
		p.SetBool("futureAsked", true) // answered in a newer build
		var ids []string
		for _, q := range h.d.dueFeatureQuestions() {
			ids = append(ids, q.ID)
		}
		if strings.Join(ids, ",") != "old,new" {
			t.Fatalf("unanswered, unretired entries oldest first (skipped versions together): %v", ids)
		}
		h.d.askFeatureQuestions()
		top := h.w.Canvas().Overlays().Top()
		if top == nil || !hasLabel(top, "New: Title old") || !hasLabel(top, "New: Title new") || hasLabel(top, "Title mid") || hasLabel(top, "Title gone") {
			t.Fatal("one dialog with only the due entries, tagged")
		}
		if r := radioWith(h, "Keep old"); r == nil || r.Selected != "Keep old" {
			t.Fatal("the recommended choice is preselected")
		}
		tapDialogButton(t, h, "Later")
		if p.Bool("oldAsked") || p.Bool("newAsked") || p.Bool("pref_oldSet") {
			t.Fatal("Later applies nothing")
		}
		h.d.askFeatureQuestions()
		radioWith(h, "Keep new").SetSelected("Off new")
		tapDialogButton(t, h, "Save")
		if !p.Bool("oldAsked") || !p.Bool("newAsked") || !p.BoolWithFallback("pref_old", false) || p.BoolWithFallback("pref_new", true) {
			t.Fatal("Save records both answers")
		}
		h.d.askFeatureQuestions()
		if h.w.Canvas().Overlays().Top() != nil || len(h.d.dueFeatureQuestions()) != 0 {
			t.Fatal("nothing is asked once all are answered")
		}
		h.d.markFeatureQuestionsAnswered()
		if !p.Bool("futureAsked") {
			t.Fatal("an older build never clears answers it doesn't know")
		}
	})
}

func TestFeatureQuestionsChangedRemovedNotice(t *testing.T) {
	h := newHarness(t)
	ch, rm := testQuestion("chg", featureChanged), testQuestion("rm", featureRemoved)
	ch.Explain, rm.Explain = "Old behaviour changes; your setting stays until you pick.", "This goes away; your setting is turned off."
	withQuestions(t, ch, rm)
	h.ui(func() {
		p := h.d.app.Preferences()
		p.SetBool(prefWelcomeSeen, true)
		h.d.askFeatureQuestions()
		top := h.w.Canvas().Overlays().Top()
		if !hasLabel(top, "Changed: Title chg") || !hasLabel(top, "Removed: Title rm") || !hasLabel(top, ch.Explain) || !hasLabel(top, rm.Explain) {
			t.Fatal("changed and removed entries say what happens, tagged")
		}
		radioWith(h, "Keep rm").SetSelected("Off rm")
		tapDialogButton(t, h, "Save")
		if !p.Bool("chgAsked") || !p.Bool("rmAsked") || !p.BoolWithFallback("pref_chg", false) || p.BoolWithFallback("pref_rm", true) {
			t.Fatal("both answers saved")
		}
	})
	withQuestions(t, featureQuestion{ID: "sec", Kind: featureNotice, Title: "Safer pairing", Explain: "Pairing now needs a code."})
	h.ui(func() {
		h.d.askFeatureQuestions()
		top := h.w.Canvas().Overlays().Top()
		if !hasLabel(top, "Notice: Safer pairing") || radioWith(h, "") != nil {
			t.Fatal("a notice shows without choices")
		}
		tapDialogButton(t, h, "OK")
		if !h.d.app.Preferences().Bool("secAsked") || len(h.d.dueFeatureQuestions()) != 0 {
			t.Fatal("a notice is shown once")
		}
	})
}

func TestChangedDefaultsKeepUserPrefs(t *testing.T) {
	h := newHarness(t)
	withQuestions(t)
	h.ui(func() {
		p := h.d.app.Preferences()
		prefDefaults["x"], prefDefaults["y"], prefDefaults["legacy"] = true, true, true
		setUserPref(p, "y", true)
		p.SetBool("legacy", true) // stored before the markers existed
		prefDefaults["x"], prefDefaults["y"], prefDefaults["legacy"] = false, false, false
		if prefBool(p, "x") || !prefBool(p, "y") || !prefBool(p, "legacy") {
			t.Fatal("a new default applies to prefs the user never set; set prefs are kept")
		}
	})
}

func TestFeatureAskWaitsUntilReady(t *testing.T) {
	h := newHarness(t)
	withQuestions(t, testQuestion("a", featureNew))
	old := featureIdle
	featureIdle = 300 * time.Millisecond
	t.Cleanup(func() { featureIdle = old })
	h.ui(func() {
		h.d.app.Preferences().SetBool(prefWelcomeSeen, true)
		closeOverlays(h)
		h.d.hidden = true // started hidden in the tray
		h.d.askFeatureQuestionsWhenReady()
	})
	time.Sleep(2500 * time.Millisecond)
	h.ui(func() {
		if h.w.Canvas().Overlays().Top() != nil {
			t.Fatal("a hidden start defers the questions")
		}
		h.d.restore = &restoreState{}
		if h.d.featureAskReady() {
			t.Fatal("not during a restore")
		}
		h.d.restore, h.d.hidden, h.d.lastTyped = nil, false, time.Now()
		if h.d.featureAskReady() {
			t.Fatal("not while the user is typing")
		}
	})
	deadline := time.Now().Add(8 * time.Second)
	for {
		var shown bool
		h.ui(func() { shown = radioWith(h, "Keep a") != nil })
		if shown {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shown once the window is visible and idle")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestPhoneFeatureQuestions(t *testing.T) {
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	h := newHarness(t)
	withQuestions(t, testQuestion("a", featureNew), featureQuestion{ID: "n", Kind: featureNotice, Title: "N", Explain: "Notice."})
	var link string
	h.ui(func() {
		h.d.app.Preferences().SetBool(prefWelcomeSeen, true)
		h.d.app.Preferences().SetInt("phonePort", 0)
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
		link = h.d.pairLink()
		h.d.askFeatureQuestions() // the desktop dialog is open meanwhile
	})
	defer h.ui(h.d.stopPhone)
	u, _ := url.Parse(link)
	q := u.Query()
	a := unlockedClient(h, &apiClient{t: t, base: "https://127.0.0.1:" + q.Get("p"), key: q.Get("k"),
		c: &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}})
	n := 0
	key := func() string {
		n++
		time.Sleep(110 * time.Millisecond)
		return "question-key-" + strconv.Itoa(1000000+n)
	}
	_, m, _ := a.do("GET", "/phone/settings/questions", "", nil)
	qs, _ := m["questions"].([]any)
	if len(qs) != 2 {
		t.Fatalf("both questions listed: %v", m)
	}
	first := qs[0].(map[string]any)
	if first["id"] != "a" || first["kind"] != "New" || first["title"] != "Title a" || first["text"] != "Explain a" || first["recommended"] != float64(0) || len(first["choices"].([]any)) != 2 {
		t.Fatalf("question shape: %v", first)
	}
	if code, m, _ := a.do("POST", "/phone/settings/answer", key(), map[string]any{"id": "a", "choice": 1}); code != 403 || errCode(m) != "control_off" {
		t.Fatalf("answering needs control: %d %v", code, m)
	}
	h.ui(func() { h.d.app.Preferences().SetBool("phoneControl", true) })
	if code, m, _ := a.do("POST", "/phone/settings/answer", key(), map[string]any{"id": "a", "choice": 5}); code != 404 {
		t.Fatalf("bad choice: %d %v", code, m)
	}
	code, m, _ := a.do("POST", "/phone/settings/answer", key(), map[string]any{"id": "a", "choice": 1})
	if qs, _ := m["questions"].([]any); code != 200 || len(qs) != 1 {
		t.Fatalf("answer: %d %v", code, m)
	}
	h.ui(func() {
		p := h.d.app.Preferences()
		if !p.Bool("aAsked") || p.BoolWithFallback("pref_a", true) {
			t.Fatal("a phone answer is saved on the desktop")
		}
		if radioWith(h, "Keep a") != nil || !hasLabel(h.w.Canvas().Overlays().Top(), "Notice.") {
			t.Fatal("the desktop dialog drops the question answered on the phone")
		}
		tapDialogButton(t, h, "OK")
	})
	_, m, _ = a.do("GET", "/phone/settings/questions", "", nil)
	if qs, _ := m["questions"].([]any); len(qs) != 0 {
		t.Fatalf("answered on the desktop: gone on the phone: %v", m)
	}
}

func TestCoordinatorSettingsCaptionAndHint(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		if !hasLabel(h.d.setGeneral, coordinatorExplain+"\n"+coordinatorOnline) || h.d.coordChk.Text != "Coordinate with subagents" {
			t.Fatal("Settings → General explains the checkbox with the exact text")
		}
		h.d.coordChk.SetChecked(false)
		if !h.d.app.Preferences().Bool(prefCoordinator + "Set") {
			t.Fatal("the checkbox marks the pref as the user's")
		}
	})
	const want = "Claude splits your requests into tasks and runs each in its own background subagent while it stays free to talk with you. Each task costs about the same tokens as running it in a session of its own, plus a little for briefing and review. Because tasks run side by side, more work gets done per hour, and token use grows at the same rate as the work, not faster."
	if coordinatorExplain != want || featureQuestions[0].ID != "coordinator" || featureQuestions[0].Explain != want {
		t.Fatal("the coordinator entry is first, with the user-approved wording")
	}
	if !strings.Contains(coordinatorHint, "Long-running work (test suites, merges, builds, release steps) goes to background subagents too.") {
		t.Fatal("the coordinator hint sends long-running work to subagents")
	}
	if !strings.Contains(coordinatorHint, "Use a subagent when a task is independent and takes more than a few minutes; do small changes, or ones that depend on details from the conversation, directly.") {
		t.Fatal("the coordinator hint says when to use a subagent")
	}
	if n := len(sessionPrompt(true, true, true)); n > 3000 {
		t.Fatalf("the appended prompt stays short: %d characters", n)
	}
}

func TestCoordinatorWelcomeShot(t *testing.T) {
	if os.Getenv("DESK_SHOTS") == "" {
		t.Skip("set DESK_SHOTS=1")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KMEM_CONFIG", "")
	dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
	os.MkdirAll(dir, 0o755)
	h := newHarness(t)
	h.ui(func() { h.w.Resize(fyne.NewSize(1280, 860)); h.d.showWelcome(nil) })
	h.ui(func() {
		deepWalk(h.w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
			if s, ok := o.(*container.Scroll); ok {
				s.Offset.Y = 330
				s.Refresh()
			}
		})
	})
	h.ui(func() { shot(t, h.w, dir, "welcome-coordinator.png") })
	h.ui(func() {
		closeOverlays(h)
		h.d.app.Preferences().SetBool(prefWelcomeSeen, true)
		h.d.app.Preferences().RemoveValue(prefCoordinatorAsked)
		h.d.askFeatureQuestions()
	})
	h.ui(func() { shot(t, h.w, dir, "coordinator-ask.png") })
}
