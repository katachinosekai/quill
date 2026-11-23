package main

import (
	"strings"
	"testing"
)

func TestParseChoicesParallelOptions(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"**Old kmem window: replaced with Pop out later (recommended), with Pop out now, or keep both?**",
			[]string{"Replaced with Pop out later", "With Pop out now", "Keep both"}},
		{"Settings control: a quill settings command over the local socket with a token (recommended), an MCP tool, or the operator proposes and you click Apply?",
			[]string{"A quill settings command over the local socket with a token", "An MCP tool", "The operator proposes and you click Apply"}},
		{"Hidden windows: closed with the X (recommended), hidden with a hotkey, or hidden with a tray click?",
			[]string{"Closed with the X", "Hidden with a hotkey", "Hidden with a tray click"}},
		// an elaboration still joins its option
		{"Which? All of them, with single-action buttons getting one reply (recommended), or just the first?",
			[]string{"All of them, with single-action buttons getting one reply", "Just the first"}},
	}
	for _, c := range cases {
		_, opts, ok := parseChoices(c.line)
		if !ok || strings.Join(opts, "|") != strings.Join(c.want, "|") {
			t.Errorf("%q\n got %q (ok %v)\nwant %q", c.line, opts, ok, c.want)
			continue
		}
		if len(c.want) == 3 {
			_, qs := numberedChoiceQs("Two.\n\n11. " + strings.Trim(c.line, "*") + "\n12. Colour: red or blue?")
			if len(qs) != 2 || len(qs[0].opts) != 3 {
				t.Fatalf("numbered: %+v", qs)
			}
			for i, o := range qs[0].opts {
				got := multiChoiceReply(qs[:1], map[int]int{0: i}, nil)
				if !strings.HasSuffix(got, ": "+o+".") || strings.Count(got, "option:") != 1 {
					t.Errorf("pick %d sends %q", i, got)
				}
			}
		}
	}
}
