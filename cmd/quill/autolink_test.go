package main

import (
	"testing"

	"fyne.io/fyne/v2/widget"
)

func TestAutolinks(t *testing.T) {
	cases := map[string]string{
		"Open https://login.tailscale.com/a/gb77 now.": "Open [https://login.tailscale.com/a/gb77](https://login.tailscale.com/a/gb77) now.",
		"see **https://x.io/a**":                       "see **[https://x.io/a](https://x.io/a)**",
		"file:///tmp/a.png, ok":                        "[file:///tmp/a.png](file:///tmp/a.png), ok",
		"run `curl https://x.io` first":                "run `curl https://x.io` first",
		"[docs](https://x.io/d) and <https://x.io/e>":  "[docs](https://x.io/d) and <https://x.io/e>",
		"```\nhttps://x.io/code\n```":                  "```\nhttps://x.io/code\n```",
		"no links here":                                "no links here",
		"(https://x.io/p)":                             "([https://x.io/p](https://x.io/p))",
	}
	for in, want := range cases {
		if got := autolinks(in); got != want {
			t.Errorf("%q\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestAutolinkRendersHyperlink(t *testing.T) {
	rt := widget.NewRichTextFromMarkdown(autolinks("sign in at https://login.tailscale.com/a/x please"))
	var walk func([]widget.RichTextSegment) bool
	walk = func(segs []widget.RichTextSegment) bool {
		for _, s := range segs {
			if h, ok := s.(*widget.HyperlinkSegment); ok && h.URL != nil && h.URL.String() == "https://login.tailscale.com/a/x" {
				return true
			}
			if p, ok := s.(*widget.ParagraphSegment); ok && walk(p.Texts) {
				return true
			}
		}
		return false
	}
	if !walk(rt.Segments) {
		t.Fatalf("no hyperlink segment: %#v", rt.Segments)
	}
}
