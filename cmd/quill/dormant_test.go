package main

import (
	"os"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// cardIconButtons: the card's icon-only hide buttons, in list order.
func cardIconButtons(h *harness) []*widget.Button {
	var out []*widget.Button
	deepWalk(h.d.chat.box, func(o fyne.CanvasObject) {
		if b, ok := o.(*widget.Button); ok && b.Text == "" && b.Icon != nil {
			out = append(out, b)
		}
	})
	return out
}

func cardAccordion(h *harness) *widget.Accordion {
	var a *widget.Accordion
	deepWalk(h.d.chat.box, func(o fyne.CanvasObject) {
		if x, ok := o.(*widget.Accordion); ok {
			a = x
		}
	})
	return a
}

func openDormantCard(t *testing.T, h *harness) *deskSession {
	var s *deskSession
	h.ui(func() {
		h.d.newSessionDialog()
		s = h.d.current
	})
	h.eventually("card", func() bool { return strings.Contains(strings.Join(cardButtons(h), "|"), "Beta") })
	return s
}

func TestDormantHideFoldAndShowAgain(t *testing.T) {
	h := newHarness(t)
	topicKmem(t, h)
	openDormantCard(t, h)
	if cardAccordion(h) != nil {
		t.Fatal("no fold while nothing is hidden")
	}
	h.ui(func() {
		hide := cardIconButtons(h)
		if len(hide) != 2 {
			t.Fatalf("one hide button per topic, got %d", len(hide))
		}
		test.Tap(hide[0]) // Alpha
		if !h.d.isDormant("alpha") || h.d.isDormant("beta") {
			t.Fatalf("dormant: %v", h.d.org.Dormant)
		}
		acc := cardAccordion(h)
		if acc == nil || acc.Items[0].Title != "Dormant (1)" || acc.Items[0].Open {
			t.Fatalf("collapsed Dormant (1) fold expected: %+v", acc)
		}
		acc.Open(0)
		if got := strings.Join(cardButtons(h), "|"); !strings.Contains(got, "Show again") {
			t.Fatalf("fold rows: %s", got)
		}
		test.Tap(cardIconButtons(h)[0]) // Beta
		if cardAccordion(h).Items[0].Title != "Dormant (2)" {
			t.Fatal("fold count follows")
		}
		for _, b := range cardButtons(h) { // main list is empty now
			if strings.HasPrefix(b, "No kmem topics") {
				t.Fatal("hidden topics are not 'no topics'")
			}
		}
		for _, o := range []string{"Show again"} {
			var show *widget.Button
			deepWalk(h.d.chat.box, func(x fyne.CanvasObject) {
				if b, ok := x.(*widget.Button); ok && b.Text == o && show == nil {
					show = b
				}
			})
			test.Tap(show)
		}
		if len(h.d.org.Dormant) != 1 || cardAccordion(h).Items[0].Title != "Dormant (1)" {
			t.Fatalf("show again: %v", h.d.org.Dormant)
		}
	})
}

func TestDormantPersistsAcrossOrgReload(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.setDormant("alpha", true)
		h.d.setDormant("alpha", true) // idempotent
		h.d.org = sideOrg{}
		h.d.loadOrg()
		if len(h.d.org.Dormant) != 1 || !h.d.isDormant("alpha") {
			t.Fatalf("reloaded: %v", h.d.org.Dormant)
		}
	})
}

func TestDormantUnhiddenByEveryOpenPath(t *testing.T) {
	paths := map[string]func(h *harness, s *deskSession){
		"fold button": func(h *harness, s *deskSession) { h.d.pickTopic(s, topicInfo{Slug: "alpha", Title: "Alpha"}, false) },
		"typed name":  func(h *harness, s *deskSession) { h.d.pickNewTopic(s, "alpha") },
		"openTopic":   func(h *harness, s *deskSession) { h.d.openTopic("alpha") },
		"resume-all": func(h *harness, s *deskSession) {
			h.d.app.Preferences().SetStringList(topicsAtQuitKey, []string{"alpha"})
			h.d.reopenTopicsAtQuit()
		},
		"kmem window": func(h *harness, s *deskSession) { h.d.resumeHandoffNamed("alpha") },
		"live switch": func(h *harness, s *deskSession) {
			o := h.d.createSession(h.proj, "default", "default")
			h.d.org.Kmem[o.id] = "alpha"
			h.d.tabs = append(h.d.tabs, o.id)
			h.d.openTopic("alpha")
		},
	}
	for name, run := range paths {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			topicKmem(t, h)
			s := openDormantCard(t, h)
			h.ui(func() {
				h.d.setDormant("alpha", true)
				h.d.setDormant("beta", true)
				run(h, s)
				if h.d.isDormant("alpha") {
					t.Fatalf("%s did not un-hide alpha", name)
				}
				if !h.d.isDormant("beta") {
					t.Fatal("other topics stay hidden")
				}
			})
		})
	}
}

func TestPhoneTopicsDormantFlag(t *testing.T) {
	h, a := topicPhone(t, true)
	h.ui(func() { h.d.setDormant("beta", true) })
	code, m, _ := a.do("GET", "/phone/topics", "", nil)
	if code != 200 {
		t.Fatalf("topics: %d %v", code, m)
	}
	be, al := topicByslug(m, "beta"), topicByslug(m, "alpha")
	if be == nil || be["dormant"] != true {
		t.Fatalf("beta listed and dormant: %v", m["topics"])
	}
	if al == nil || al["dormant"] != nil {
		t.Fatalf("alpha has no dormant flag: %v", al)
	}
}

func TestDormantCardShot(t *testing.T) {
	if os.Getenv("DESK_SHOTS") == "" {
		t.Skip("set DESK_SHOTS=1 to render the card with a Dormant fold")
	}
	dir := "/tmp/quill-dormant-shots"
	os.MkdirAll(dir, 0o755)
	h := newHarness(t)
	topicKmem(t, h)
	openDormantCard(t, h)
	h.ui(func() {
		test.Tap(cardIconButtons(h)[1])
		cardAccordion(h).Open(0)
	})
	h.ui(func() { shot(t, h.w, dir, "topic-card-dormant.png") })
}

func TestDormantCommandVerbs(t *testing.T) {
	h := newHarness(t)
	topicKmem(t, h)
	openDormantCard(t, h)
	path := t.TempDir() + "/q.sock"
	h.ui(func() { h.d.listenInstance(path) })
	if !sendCommand(path, "hide-topic alpha,beta") {
		t.Fatal("hide-topic refused")
	}
	h.eventually("both hidden and card refreshed", func() bool {
		a := cardAccordion(h)
		return h.d.isDormant("alpha") && h.d.isDormant("beta") && a != nil && a.Items[0].Title == "Dormant (2)"
	})
	if !sendCommand(path, "show-topic alpha") {
		t.Fatal("show-topic refused")
	}
	h.eventually("alpha shown again", func() bool { return !h.d.isDormant("alpha") && h.d.isDormant("beta") })
	if sendCommand(path, "hide-topic bad/name") || sendCommand(path, "hide-topic a;b") {
		t.Fatal("a bad slug is refused")
	}
	if sendCommand(t.TempDir()+"/none.sock", "hide-topic alpha") {
		t.Fatal("no Quill Desktop running: not ok")
	}
}
