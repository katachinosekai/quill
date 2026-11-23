package main

import "testing"

func TestRevealStepSpeeds(t *testing.T) {
	// Normal equals the original formula at every gap.
	for gap := 1; gap < 2000; gap++ {
		want := min(max(revealMinRate, gap/revealCatchUp), revealMaxStep)
		if got := revealStep("normal", gap); got != want {
			t.Fatalf("normal gap %d: %d want %d", gap, got, want)
		}
		if got := revealStep("bogus", gap); got != want {
			t.Fatalf("unknown speed must be normal, gap %d: %d", gap, got)
		}
	}
	for _, gap := range []int{5, 100, 500, 5000} {
		s, n, f := revealStep("slow", gap), revealStep("normal", gap), revealStep("fast", gap)
		if !(s < n && n < f) {
			t.Fatalf("gap %d: slow %d normal %d fast %d not increasing", gap, s, n, f)
		}
		if revealStep("instant", gap) != gap {
			t.Fatalf("instant must reveal all %d", gap)
		}
	}
}

func TestSpeedNames(t *testing.T) {
	for _, n := range animSpeedNames {
		if speedName(speedKey(n)) != n {
			t.Fatalf("%s does not round-trip", n)
		}
	}
	if speedName("") != "Normal" || speedName("zzz") != "Normal" {
		t.Fatal("unknown must be Normal")
	}
}
