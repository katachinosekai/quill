package main

import "testing"

func TestRevealRateSpeeds(t *testing.T) {
	// Every speed is its fraction of the old Normal formula at every gap;
	// unknown means normal.
	for gap := 1; gap < 2000; gap++ {
		old := float64(min(max(revealMinRate, gap/revealCatchUp), revealMaxStep))
		for speed, scale := range map[string]float64{"fast": 0.5, "normal": 0.25, "slow": 0.125, "bogus": 0.25} {
			if got := revealRate(speed, gap); got != old*scale {
				t.Fatalf("%s gap %d: %v want %v", speed, gap, got, old*scale)
			}
		}
		if revealRate("instant", gap) != float64(gap) {
			t.Fatalf("instant must reveal all %d", gap)
		}
	}
	for _, gap := range []int{5, 100, 500, 5000} {
		s, n, f := revealRate("slow", gap), revealRate("normal", gap), revealRate("fast", gap)
		if !(s < n && n < f) {
			t.Fatalf("gap %d: slow %v normal %v fast %v not increasing", gap, s, n, f)
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
