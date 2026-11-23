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
			if strings.HasPrefix(x.PlaceHolder, "Search topics") {
				e = x
			}
		case *widget.Button:
			if strings.HasPrefix(x.Text, "New topic:") || strings.HasPrefix(x.Text, "Open topic:") {
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
		browseAll(h)
		h.eventually("card", func() bool {
			return strings.Contains(strings.Join(cardButtons(h), "|"), "Beta")
		})
		h.ui(func() {
			e, b := cardWidgets(h)
			if e == nil {
				t.Fatal("card has no new-topic field or Open button")
			}
			e.SetText(name)
			_, b = cardWidgets(h)
			test.Tap(b)
			if s.pickPending {
				t.Fatalf("%s: tab still on the card after Open", name)
			}
		})
		h.eventually("prompt sent", func() bool { return strings.Contains(sentText(s), "coordinator; kmem name") })
	}
}

func TestNewTopicFromCardWithSessionsFeature(t *testing.T) {
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
	browseAll(h)
	h.eventually("card", func() bool { return strings.Contains(strings.Join(cardButtons(h), "|"), "Beta") })
	h.ui(func() {
		e, b := cardWidgets(h)
		e.SetText("Garden Planner")
		_, b = cardWidgets(h)
		test.Tap(b)
		if s.pickPending || h.d.org.Kmem[s.id] != "garden-planner" || h.d.org.Titles[s.id] != "Garden Planner" {
			t.Fatalf("not opened: %v %q", s.pickPending, h.d.org.Kmem[s.id])
		}
	})
	h.eventually("prompt", func() bool { return strings.Contains(sentText(s), "coordinator; kmem name") })
}

// A refused pick (the name "kmem") posts its notice but leaves the card, so
// the user can type another name; before the fix the notice replaced the card
// and the tab was stuck unpicked.
func TestRefusedNewTopicKeepsCard(t *testing.T) {
	h := newHarness(t)
	topicKmem(t, h)
	var s *deskSession
	h.ui(func() {
		h.d.newSessionDialog()
		s = h.d.current
	})
	browseAll(h)
	h.eventually("card", func() bool { return strings.Contains(strings.Join(cardButtons(h), "|"), "Beta") })
	h.ui(func() {
		e, b := cardWidgets(h)
		e.SetText("kmem")
		_, b = cardWidgets(h)
		test.Tap(b)
		if !s.pickPending || !strings.Contains(sentText(s), "kmem window's topic") {
			t.Fatalf("refused with a notice, tab still pending: %v %q", s.pickPending, sentText(s))
		}
		e, b = cardWidgets(h)
		if e == nil {
			t.Fatal("the card is gone after the refusal: the tab is stuck")
		}
		e.SetText("Garden Planner")
		_, b = cardWidgets(h)
		test.Tap(b)
		if s.pickPending || h.d.org.Kmem[s.id] != "garden-planner" {
			t.Fatal("a second try opens the topic")
		}
	})
}
