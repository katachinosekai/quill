package main

import (
	"strings"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

// The phone API's change check runs over up to 400 recent items per session
// on every scan.
func BenchmarkItemSig400(b *testing.B) {
	items := make([]*chat.Item, 400)
	for i := range items {
		items[i] = &chat.Item{Kind: chat.Assistant, Text: strings.Repeat("a reply line with some words. ", 160)} // ~5 KB
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, it := range items {
			itemSig(it)
		}
	}
}

// The tracker's real cost per scan: 400 long items, the newest growing.
func BenchmarkItemTrackUpdate400(b *testing.B) {
	s := &deskSession{conv: chat.New()}
	for i := 0; i < 400; i++ {
		it := s.conv.AddNotice(strings.Repeat("a reply line with some words. ", 160))
		it.Kind = chat.Assistant
	}
	t := &itemTrack{}
	t.update(s)
	last := s.conv.Items[len(s.conv.Items)-1]
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		last.Text += "x"
		t.update(s)
	}
}
