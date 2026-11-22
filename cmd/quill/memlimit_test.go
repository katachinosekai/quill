package main

import (
	"runtime/debug"
	"testing"
)

func TestAdjustMemoryLimit(t *testing.T) {
	defer debug.SetMemoryLimit(memorySoftLimit)
	const mb = 1 << 20
	if got := adjustMemoryLimit(100*mb, memorySoftLimit); got != memorySoftLimit {
		t.Fatalf("a small heap keeps the limit: %d", got/mb)
	}
	if got := adjustMemoryLimit(1400*mb, memorySoftLimit); got != 2100*mb {
		t.Fatalf("a heap over the limit raises it to 1.5x: %d MB", got/mb)
	}
	if got := adjustMemoryLimit(200*mb, 2100*mb); got != memorySoftLimit {
		t.Fatalf("a shrunk heap lowers it back: %d MB", got/mb)
	}
}
