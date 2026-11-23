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

// The phone tracker keeps a hash per item: an edit that keeps the length
// (and an edit in an old item) still shows up as a change.
func TestItemTrackDetectsSameLengthEdit(t *testing.T) {
	s := &deskSession{conv: chat.New()}
	for i := 0; i < 40; i++ {
		it := s.conv.AddNotice(strings.Repeat("x", 50))
		it.Kind = chat.Assistant
	}
	tr := &itemTrack{}
	tr.update(s)
	rev := tr.rev
	tr.update(s)
	if tr.rev != rev {
		t.Fatal("no edit, no change")
	}
	recent := s.conv.Items[len(s.conv.Items)-1]
	recent.Text = strings.Repeat("y", 50)
	tr.update(s)
	if tr.rev == rev {
		t.Fatal("same-length edit of a recent item went unnoticed")
	}
	rev = tr.rev
	old := s.conv.Items[0]
	old.Text = strings.Repeat("y", 50)
	old.Streaming = true // flags go through the cheap check
	tr.update(s)
	if tr.rev == rev {
		t.Fatal("flag change of an old item went unnoticed")
	}
	if a, b := itemSigTag(recent), itemSigTag(&chat.Item{Kind: chat.Assistant, Text: strings.Repeat("z", 50)}); a == b {
		t.Fatal("different text, same tag")
	}
}
