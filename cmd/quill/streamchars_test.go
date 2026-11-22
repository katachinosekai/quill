package main

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/katachinosekai/quill/internal/chat"
)

// Streaming reveals character by character (user 2026-11-22: not whole
// words): mid-stream the revealed text may end inside a word, but never
// inside a multi-byte character.
func TestStreamRevealsCharacters(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.app.Preferences().SetBool(prefAnimateText, true)
		h.d.open(editsID)
	})
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	text := strings.Repeat("wörtchen größere émoji😀s ", 40)
	var it *chat.Item
	h.ui(func() {
		s := h.d.sessions[editsID]
		it = s.conv.AddNotice("")
		it.Kind, it.Streaming = chat.Assistant, true
		h.d.chat.show(s)
		it.Text = text
		h.d.chat.update([]*chat.Item{it})
	})
	midWord := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var pos int
		h.ui(func() {
			if iv := h.d.chat.views[it.Seq]; iv != nil && iv.st != nil {
				pos = iv.st.pos
			}
		})
		if pos > 0 && pos < len(text) {
			if !utf8.RuneStart(text[pos]) {
				t.Fatalf("reveal stopped inside a character at byte %d", pos)
			}
			if text[pos] != ' ' && text[pos-1] != ' ' {
				midWord = true
			}
		}
		if pos >= len(text) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !midWord {
		t.Fatal("the reveal only ever stopped at word ends")
	}
}
