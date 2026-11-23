package main

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/widget"
	"github.com/rivo/uniseg"
)

// Samples: ZWJ family, flags, e + combining acute, Hindi conjunct, Arabic.
var graphemeSamples = map[string]string{
	"zwj":    "a\U0001F468‍\U0001F469‍\U0001F467b",
	"flags":  "x\U0001F1E9\U0001F1EA\U0001F1EB\U0001F1F7y",
	"accent": "café ok",
	"hindi":  "क्षि है",
	"arabic": "مرحبا بالعالم",
	"mixed":  "hi क्षि \U0001F468‍\U0001F469‍\U0001F467 é \U0001F1E9\U0001F1EA مرحبا",
}

// The reveal stops only on cluster boundaries, one cluster per step.
func TestAdvanceStepsByGrapheme(t *testing.T) {
	for name, text := range graphemeSamples {
		bounds := map[int]bool{0: true}
		off := 0
		g := uniseg.NewGraphemes(text)
		for g.Next() {
			off += len(g.Str())
			bounds[off] = true
		}
		st := &streamView{animated: true, speed: "slow"}
		for i := 0; st.pos < len(text); i++ {
			prev := st.pos
			st.advance(text)
			if !bounds[st.pos] {
				t.Fatalf("%s: stopped inside a letter at byte %d", name, st.pos)
			}
			if i > 10*len(text) {
				t.Fatalf("%s: no progress", name)
			}
			_ = prev
		}
	}
}

// One step logs letters, not runes: a ZWJ family counts once.
func TestAdvanceCountsLetters(t *testing.T) {
	text := "\U0001F468‍\U0001F469‍\U0001F467\U0001F1E9\U0001F1EA"
	st := &streamView{animated: true, speed: "instant"}
	st.advance(text)
	if st.pos != len(text) {
		t.Fatalf("pos %d", st.pos)
	}
	total := 0
	for _, r := range st.reveals {
		total += r.letters
	}
	if total != 2 {
		t.Fatalf("logged %d letters, want 2", total)
	}
}

// Markdown markers take no time and do not split a letter.
func TestAdvanceSkipsMarkersWithGraphemes(t *testing.T) {
	text := "**é**x"
	st := &streamView{animated: true, speed: "slow"}
	for st.pos < len(text) {
		st.advance(text)
		if st.pos == 3 { // between e and its accent
			t.Fatal("split e + accent")
		}
	}
}

func fadedPieces(t *testing.T, text string, counts [textFadeSteps]int) []string {
	t.Helper()
	segs := []widget.RichTextSegment{&widget.TextSegment{Text: text, Style: widget.RichTextStyleInline}}
	segs[0].(*widget.TextSegment).Style.ColorName = "foreground"
	b := fadeBudget{counts}
	var out []string
	for _, s := range fadeSegs(segs, &b) {
		out = append(out, s.(*widget.TextSegment).Text)
	}
	if strings.Join(out, "") != text {
		t.Fatalf("text changed: %q", out)
	}
	return out
}

// The fade cuts between letters only.
func TestFadeCutsWholeLetters(t *testing.T) {
	for name, text := range graphemeSamples {
		n := uniseg.GraphemeClusterCount(text)
		for a := 1; a < n; a++ {
			pieces := fadedPieces(t, text, [textFadeSteps]int{a, 0, 0, 0})
			for _, p := range pieces {
				if strings.HasPrefix(p, "́") || strings.HasPrefix(p, "‍") || strings.HasPrefix(p, "्") {
					t.Fatalf("%s a=%d: piece %q starts mid-letter", name, a, p)
				}
			}
			// every piece is a run of whole clusters of the original
			joined := ""
			for _, p := range pieces {
				if uniseg.GraphemeClusterCount(joined+p) != uniseg.GraphemeClusterCount(joined)+uniseg.GraphemeClusterCount(p) {
					t.Fatalf("%s a=%d: cut inside a cluster at %q", name, a, p)
				}
				joined += p
			}
		}
	}
}

// Shaped scripts are not cut inside a word; Latin still is.
func TestFadeKeepsShapedWordsWhole(t *testing.T) {
	ar := "مرحبا بالعالم"
	p := fadedPieces(t, ar, [textFadeSteps]int{2, 0, 2, 0})
	want := []string{"مرحبا ", "بالعالم"}
	if len(p) != 2 || p[0] != want[0] || p[1] != want[1] {
		t.Fatalf("arabic %q", p)
	}
	hi := "नमस्ते"
	if p := fadedPieces(t, hi, [textFadeSteps]int{2, 0, 0, 0}); len(p) != 1 {
		t.Fatalf("hindi word cut: %q", p)
	}
	if p := fadedPieces(t, "hello world", [textFadeSteps]int{2, 0, 0, 0}); len(p) != 2 || p[1] != "ld" {
		t.Fatalf("latin %q", p)
	}
}
