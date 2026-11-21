package main

import (
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

func TestRemoteBlankStuck(t *testing.T) {
	conv := func(items ...*chat.Item) *chat.Conversation {
		c := chat.New()
		c.Items = items
		return c
	}
	cases := []struct {
		name   string
		c      *chat.Conversation
		status string
		want   bool
	}{
		{"finished reply", conv(&chat.Item{Kind: chat.User, Text: "hi"}, &chat.Item{Kind: chat.Assistant, Text: "hello"}), "idle", false},
		{"still streaming in a running session", conv(&chat.Item{Kind: chat.Assistant, Streaming: true}), "running", false},
		{"empty, no longer streaming", conv(&chat.Item{Kind: chat.Assistant, Text: " \n"}), "running", true},
		{"streaming but the session stopped", conv(&chat.Item{Kind: chat.Assistant, Streaming: true}), "idle", true},
		{"empty user message", conv(&chat.Item{Kind: chat.User}), "idle", true},
		{"empty tool or notice items don't count", conv(&chat.Item{Kind: chat.Tool}, &chat.Item{Kind: chat.Notice}), "idle", false},
	}
	for _, c := range cases {
		if got := blankStuck(c.c, c.status); got != c.want {
			t.Errorf("%s: blankStuck = %v, want %v", c.name, got, c.want)
		}
	}
}
