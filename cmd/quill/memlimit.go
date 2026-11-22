package main

import (
	"context"
	"runtime/debug"
	"runtime/metrics"
	"time"
)

// The soft memory limit makes Go collect sooner, but when the live heap is
// already above it the collector runs without pause and burns a core
// (2026-11-22: 1.4 GB live against 512 MB used 113 % CPU at idle). The
// watcher raises the limit to 1.5 x the live heap whenever the live heap
// passes 85 % of it, and lowers it back toward memorySoftLimit later.
func watchMemoryLimit(ctx context.Context) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	limit := int64(memorySoftLimit)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			limit = adjustMemoryLimit(liveHeap(), limit)
		}
	}
}

func adjustMemoryLimit(live, limit int64) int64 {
	want := max(int64(memorySoftLimit), live*3/2)
	switch {
	case live > limit*85/100:
		debug.SetMemoryLimit(want)
		return want
	case limit > memorySoftLimit && want < limit*2/3:
		debug.SetMemoryLimit(want)
		return want
	}
	return limit
}

func liveHeap() int64 {
	s := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return int64(s[0].Value.Uint64())
}
