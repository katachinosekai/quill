package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

const recQuestion = "1. **Topic names: add all languages (recommended), English letters and numbers only, or leave as is?**"

func TestChoiceRecommendedParsed(t *testing.T) {
	_, opts, ok := parseChoices(recQuestion)
	if !ok || len(opts) != 3 {
		t.Fatalf("opts %v %v", opts, ok)
	}
	if r := choiceRecommended(recQuestion, len(opts)); r != 0 {
		t.Fatalf("recommended %d", r)
	}
	if r := choiceRecommended("Ship now or later?", 2); r != -1 {
		t.Fatalf("no mark gave %d", r)
	}
	// The reply sent stays the plain option.
	if got := choiceReply(0, opts[0]); strings.Contains(got, "recommended") {
		t.Fatalf("reply carries the mark: %q", got)
	}
	_, qs := numberedChoiceQs("Two.\n\n1. Which disk: A (recommended) or B?\n2. Colour: red, green, or blue?")
	if len(qs) != 2 || qs[0].rec != 0 || qs[1].rec != -1 {
		t.Fatalf("numbered recs %+v", qs)
	}
}

type btnState struct {
	text    string
	hasIcon bool
}

func choiceButtons(o fyne.CanvasObject) []btnState {
	var out []btnState
	deepWalk(o, func(x fyne.CanvasObject) {
		if b, ok := x.(*widget.Button); ok {
			out = append(out, btnState{b.Text, b.Icon != nil})
		}
	})
	return out
}

func TestChoiceRecommendedButtons(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		answered   bool
	}{
		{"single-latest", "Done.\n\n" + recQuestion, false},
		{"single-answered", "Done.\n\n" + recQuestion, true},
		{"multi-latest", "Two.\n\n1. Topic names: add all languages (recommended), English only, or leave as is?\n2. Colour: red, green, or blue?", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			var s *deskSession
			h.ui(func() {
				s = h.d.createSession(h.proj, "default", "default")
				h.d.sendTo(s, "hi")
			})
			h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
			h.ui(func() {
				v := h.d.chat
				h.w.Resize(fyne.NewSize(900, 600))
				msg := &chat.Item{Seq: 995, Kind: chat.Assistant, Text: tc.text}
				s.conv.Items = append(s.conv.Items, msg)
				if tc.answered {
					s.conv.Items = append(s.conv.Items, &chat.Item{Seq: 996, Kind: chat.User, Text: choiceReply(2, "Leave as is")})
				}
				v.show(s)
				o, _ := v.renderChoiceReply(msg)
				if o == nil {
					t.Fatal("no overlay")
				}
				got := choiceButtons(o)
				recs, checks := 0, 0
				for _, b := range got {
					if strings.Contains(b.text, "(recommended)") {
						recs++
						if !strings.HasPrefix(b.text, "Add all languages") {
							t.Fatalf("wrong button marked: %q", b.text)
						}
					}
					if b.hasIcon && b.text != "Send answers" {
						checks++
					}
				}
				if recs != 1 {
					t.Fatalf("recommended buttons: %d in %+v", recs, got)
				}
				switch tc.name {
				case "single-answered":
					if checks != 1 {
						t.Fatalf("answered: %d checks in %+v", checks, got)
					}
				default:
					if checks != 0 {
						t.Fatalf("unanswered buttons show checks: %+v", got)
					}
				}
				if dir := os.Getenv("QUILL_SHOT_DIR"); dir != "" {
					if f, err := os.Create(filepath.Join(dir, "choicerec-"+tc.name+".png")); err == nil {
						png.Encode(f, h.w.Canvas().Capture())
						f.Close()
					}
				}
			})
		})
	}
}

// Quill Android shows the recommended button from the item the phone API
// serves for a choice reply.
func TestPhoneItemCarriesChoiceRecommended(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() { s = h.d.createSession(h.proj, "default", "default") })
	it := &chat.Item{Seq: 7, Kind: chat.Assistant, Text: "Done.\n\n" + recQuestion}
	a := &phoneAPI{}
	m := a.item(s, it, 1, it)
	if m["choicesRecommended"] != 0 {
		t.Fatalf("item: %v", m)
	}
	plain := &chat.Item{Seq: 8, Kind: chat.Assistant, Text: "Ship now or later?"}
	if m := a.item(s, plain, 1, plain); m["choices"] == nil || m["choicesRecommended"] != nil {
		t.Fatalf("unmarked item: %v", m)
	}
}
