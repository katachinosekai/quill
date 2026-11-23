package main

import (
	"reflect"
	"strings"
	"testing"
)

const stepsMsg = "Plan:\n\n" +
	"1. Back up `/etc/fstab` to `/etc/fstab.bak-DATE`.\n" +
	"2. Add `UUID=PLACEHOLDER /mnt/data btrfs noatime 0 0`, then run `sudo findmnt --verify`.\n" +
	"3. Create the swap subvolume at `/mnt/data/swap`.\n" +
	"4. Turn it on with `sudo swapon --priority 10 /mnt/data/swap/swapfile`.\n" +
	"5. I check with `swapon --show`, then record it in notes.\n"

func qNums(qs []numChoiceQ) (r []string) {
	for _, q := range qs {
		r = append(r, q.num)
	}
	return
}

// Steps without a "?" are message text, not question sections.
func TestNumberedStepsAreNotQuestions(t *testing.T) {
	// One question after steps: fewer than two sections, so no multi card;
	// the single-choice path takes the last line.
	one := stepsMsg + "1. Where should the swap file go? On disk A (fast), or on disk B (simple) (recommended)?"
	if body, qs := numberedChoiceQs(one); qs != nil {
		t.Fatalf("one question: body %q qs %+v", body, qs)
	}
	// Two questions after steps: only they become sections, steps in body.
	two := stepsMsg + "1. Where should it go? On disk A, or on disk B?\n2. Size 8 GB or 16 GB?"
	body, qs := numberedChoiceQs(two)
	if !reflect.DeepEqual(qNums(qs), []string{"1", "2"}) {
		t.Fatalf("two questions: %+v", qs)
	}
	for _, step := range []string{"Back up", "I check with"} {
		if !strings.Contains(body, step) {
			t.Fatalf("body lost step %q: %q", step, body)
		}
	}
	// Steps mixed in between and after: still only the questions.
	mixed := "Intro.\n\n1. Blue or red?\n2. Then run `x`.\n3. Now or later?\n4. Finally verify it."
	body, qs = numberedChoiceQs(mixed)
	if !reflect.DeepEqual(qNums(qs), []string{"1", "3"}) || !strings.Contains(body, "Then run") || !strings.Contains(body, "Finally verify") {
		t.Fatalf("mixed: body %q qs %+v", body, qs)
	}
	// A closing block of only questions keeps every section, with or
	// without options.
	only := "Intro.\n\n1. Blue or red?\n2. What should it be called?\n3. Now or later?"
	body, qs = numberedChoiceQs(only)
	if !reflect.DeepEqual(qNums(qs), []string{"1", "2", "3"}) || body != "Intro.\n" {
		t.Fatalf("only questions: body %q qs %+v", body, qs)
	}
}
