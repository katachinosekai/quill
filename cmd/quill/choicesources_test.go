package main

import (
	"reflect"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

const srcLinks = "\n\nSources:\n- [A review](https://example.com/a)\n- [B review](https://example.com/b)\n- https://example.com/c"

func choiceLabels(t *testing.T, h *harness, text string) []string {
	var out []string
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		msg := &chat.Item{Seq: 995, Kind: chat.Assistant, Text: text}
		s.conv.Items = append(s.conv.Items, msg)
		h.d.chat.show(s)
		if o, _ := h.d.chat.renderChoiceReply(msg); o != nil {
			for _, b := range swapButtons(o) {
				out = append(out, b.Text)
			}
		}
	})
	return out
}

func TestChoiceButtonsBeforeSources(t *testing.T) {
	h := newHarness(t)
	q := "...measure the old ones when you open the card.\n\n**Have you finished the bot check? Yes or no?**"
	if got := choiceLabels(t, h, q+srcLinks); len(got) < 2 || got[0] != "Yes" || got[1] != "No" {
		t.Fatalf("buttons %v", got)
	}
	if got := choiceLabels(t, h, q+"\n\nSources:"); len(got) < 2 {
		t.Fatalf("heading without links: %v", got)
	}
	if got := choiceLabels(t, h, q+"\n\nThat is all I found."); len(got) != 0 {
		t.Fatalf("non-link last line: %v", got)
	}
	if got := choiceLabels(t, h, "1. Ship it or hold?\n2. Merge now or later?"+srcLinks); len(got) < 4 {
		t.Fatalf("numbered questions: %v", got)
	}
}

func TestParseChoicesFromTextBeforeSources(t *testing.T) {
	body, _ := chat.SplitSources("**Yes or no?**" + srcLinks)
	_, opts, ok := parseChoices(body[len(body)-len("**Yes or no?**"):])
	if !ok || !reflect.DeepEqual(opts, []string{"Yes", "No"}) {
		t.Fatalf("%v %v", opts, ok)
	}
}
