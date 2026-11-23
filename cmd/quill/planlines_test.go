package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

const stepsPlanText = "The full suite passes on master 25309c0. The gate refused step 3.\n\n" +
	"1. Rebuild Quill Desktop from master `25309c0` and live-reload it, then check that the new process runs the new commit.\n" +
	"2. Add this case to gate bug task t4b7838.\n\n" +
	"**Can I run steps 1–2? Yes (recommended) or no?**"

// startRows: the drawn rows that start with a list number.
func startRows(rows []drawnRow, nums ...string) map[string]float32 {
	out := map[string]float32{}
	for _, r := range rows {
		for _, n := range nums {
			if strings.HasPrefix(strings.TrimSpace(r.text), n) {
				out[n] = r.y
			}
		}
	}
	return out
}

// Numbered steps in a plan card's prose each start on their own line and
// keep their numbers (the card once drew "1. ... 2. ..." as one paragraph).
func TestPlanCardStepsEachOnOwnLine(t *testing.T) {
	for name, text := range map[string]string{
		"exact":     stepsPlanText,
		"questions": "Plan:\n1. Edit the first file.\n2. Build it.\n\nShould rule 21 stay as it is?\n\n3. Then run the check.\n4. Ship it.\n\n**Can I run steps 1–4? Yes (recommended) or no?**",
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			s, plan := planCardSession(t, h, text)
			h.ui(func() {
				o := h.d.chat.renderPlanReply(planReply(s.conv, plan))
				if o == nil {
					t.Fatal("nothing drawn")
				}
				rows := drawnRows(t, o, 1400)
				if dir := os.Getenv("QUILL_SHOT_DIR"); dir != "" {
					h.w.SetContent(container.NewVScroll(o))
					h.w.Resize(fyne.NewSize(900, 900))
					if f, err := os.Create(filepath.Join(dir, "planlines-"+name+".png")); err == nil {
						png.Encode(f, h.w.Canvas().Capture())
						f.Close()
					}
				}
				want := []string{"1.", "2."}
				if name == "questions" {
					want = []string{"1.", "2.", "3.", "4."}
				}
				got := startRows(rows, want...)
				seen := map[float32]bool{}
				for _, n := range want {
					y, ok := got[n]
					if !ok {
						t.Fatalf("no row starts with %q: %+v", n, rows)
					}
					if seen[y] {
						t.Fatalf("%q shares a line with another step: %+v", n, rows)
					}
					seen[y] = true
				}
				for _, r := range rows {
					if strings.Contains(r.text, "commit.") && strings.Contains(r.text, "2.") {
						t.Fatalf("steps joined in one line: %q", r.text)
					}
				}
			})
		})
	}
}

// The choice overlay and the plan card share keepNumbers: a list from 1 stays
// a list (one row per item), a piece that continues at 3 gets rows of its own.
func TestKeepNumbersRowsPerItem(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		for text, n := range map[string]int{
			"1. First step here.\n2. Second step here.": 2,
			"3. Third step here.\n4. Fourth step here.": 2,
			"- bullet one here\n- bullet two here":      2,
		} {
			rows := drawnRows(t, msgText(h.d.chat.markdownBlocks(keepNumbers(strings.Split(text, "\n")))), 1400)
			if len(rows) != n {
				t.Errorf("%q: %d rows, want %d: %+v", text, len(rows), n, rows)
			}
		}
	})
}
