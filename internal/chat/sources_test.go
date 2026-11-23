package chat

import "testing"

const srcReply = "...measure the old ones when you open the card.\n\n**Have you finished the bot check? Yes or no?**\n\nSources:\n- [OC3D – XFX RX 7900 XTX Merc 310 review](https://example.com/a)\n- [Overclockers – XFX MERC 310 RX 7900 XT review](https://example.com/b)\n- [overclock.net – Merc Owners Club (p.12)](https://example.com/c)"

func TestSplitSources(t *testing.T) {
	body, src := SplitSources(srcReply)
	if body != "...measure the old ones when you open the card.\n\n**Have you finished the bot check? Yes or no?**" || src == "" {
		t.Fatalf("body %q src %q", body, src)
	}
	for _, text := range []string{
		"Question?\n\nNo sources here, just a sentence after.",
		"Question?\n\n**Sources:**",              // heading, no links: still a block
		"Sources:\n- [a](https://example.com/a)", // nothing but the block
		"Done.",
	} {
		b, s := SplitSources(text)
		if text == "Question?\n\n**Sources:**" {
			if b != "Question?" || s == "" {
				t.Fatalf("%q: %q %q", text, b, s)
			}
			continue
		}
		if s != "" || b != text {
			t.Fatalf("%q split as %q / %q", text, b, s)
		}
	}
	b, _ := SplitSources("1. Ship it?\n2. Wait?\n\nReferences\n1. [a](https://example.com/a)\n2. https://example.com/b")
	if b != "1. Ship it?\n2. Wait?" {
		t.Fatalf("numbered: %q", b)
	}
}

func TestAskingAfterSources(t *testing.T) {
	conv := func(text string) *Conversation {
		return &Conversation{Items: []*Item{{Kind: Assistant, Text: text}}}
	}
	if !conv("Plan above.\n\n**Do you approve steps 1-3?**\n\nSources:\n- [a](https://example.com/a)").AsksApproval() {
		t.Fatal("approval question before sources")
	}
	if !conv(srcReply).AsksQuestion() || !conv(srcReply).AwaitsReply() {
		t.Fatal("question before sources")
	}
	tail := conv("Is it ready?\n\nNote that it is not.")
	if tail.AsksQuestion() || tail.AwaitsReply() {
		t.Fatal("a closing sentence that is no link block keeps the old behavior")
	}
}
