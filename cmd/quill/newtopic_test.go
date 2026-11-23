package main

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// cardWidgets finds the new-topic entry and its Open button on the card.
func cardWidgets(h *harness) (*widget.Entry, *widget.Button) {
	var e *widget.Entry
	var b *widget.Button
	deepWalk(h.d.chat.box, func(o fyne.CanvasObject) {
		switch x := o.(type) {
		case *widget.Entry:
			if strings.HasPrefix(x.PlaceHolder, "New topic") {
				e = x
			}
		case *widget.Button:
			if x.Text == "Open" {
				b = x
			}
		}
	})
	return e, b
}

func TestNewTopicFromCardButton(t *testing.T) {
	for _, name := range []string{"Garden Planner", "BETA"} {
		h := newHarness(t)
		topicKmem(t, h)
		var s *deskSession
		h.ui(func() {
			h.d.newSessionDialog()
			s = h.d.current
		})
		h.eventually("card", func() bool {
			return strings.Contains(strings.Join(cardButtons(h), "|"), "Beta")
		})
		h.ui(func() {
			e, b := cardWidgets(h)
			if e == nil || b == nil {
				t.Fatal("card has no new-topic field or Open button")
			}
			e.SetText(name)
			test.Tap(b)
			if s.pickPending {
				t.Fatalf("%s: tab still on the card after Open", name)
			}
		})
		h.eventually("prompt sent", func() bool { return strings.Contains(sentText(s), "coordinator; kmem name") })
	}
}

func TestNewTopicGateProbe(t *testing.T) {
	h := newHarness(t)
	topicKmem(t, h)
	var s *deskSession
	h.ui(func() {
		h.d.planFirst = true
		h.d.newSessionDialog()
		s = h.d.current
		h.d.pickNewTopic(s, "Garden Planner")
	})
	h.eventually("prompt", func() bool { return strings.Contains(sentText(s), "coordinator; kmem name") })
	h.ui(func() {
		for _, c := range []string{
			`kmem --as garden-planner brief`, `kmem --as garden-planner rules`,
			`kmem --as garden-planner task list`,
			`kmem --as garden-planner context "Garden Planner coordinator: claimed tasks, open tasks, status, next steps" --topic garden-planner`,
			`kmem --as garden-planner context --topic garden-planner`,
			`kmem brief`, `kmem task list`,
			`kmem --as garden-planner brief && kmem --as garden-planner task list`,
		} {
			t.Logf("%v  %s", h.d.kmemResumeAllowed(s, bashReq(c)), c)
		}
		t.Logf("resumeAs=%q", s.resumeAs)
	})
}

func TestNewTopicKmem19Probe(t *testing.T) {
	h := newHarness(t)
	topicKmem(t, h)
	base := h.d.kmemRun
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		switch {
		case len(args) > 0 && args[0] == "version":
			return `{"version": "1.9.2", "api": 1, "features": ["sessions"]}`, nil
		case len(args) > 1 && args[0] == "sessions":
			return `{"sessions": [], "api": 1}`, nil
		}
		return base(prog, args...)
	}
	var s *deskSession
	h.ui(func() {
		h.d.newSessionDialog()
		s = h.d.current
	})
	h.eventually("card", func() bool { return strings.Contains(strings.Join(cardButtons(h), "|"), "Beta") })
	h.ui(func() {
		e, b := cardWidgets(h)
		e.SetText("Garden Planner")
		test.Tap(b)
		t.Logf("pending=%v kmem=%q", s.pickPending, h.d.org.Kmem[s.id])
	})
	h.eventually("prompt", func() bool { return strings.Contains(sentText(s), "coordinator; kmem name") })
}
