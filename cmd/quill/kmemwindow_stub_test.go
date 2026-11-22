package main

import (
	"path/filepath"
	"testing"
)

func TestKmemWindowStubs(t *testing.T) {
	if !isKmemWindowDir(kmemWindowDir()) || !isKmemWindowDir(kmemWindowDir()+"/") || isKmemWindowDir(filepath.Dir(kmemWindowDir())) || isKmemWindowDir("") {
		t.Fatal("isKmemWindowDir matches only the assistant's folder")
	}
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		if isHiddenSession(s) {
			t.Fatal("a normal session isn't hidden")
		}
		s.hidden = true
		if !isHiddenSession(s) {
			t.Fatal("the flag hides it")
		}
		var w *kmemWindow
		if w.session() != nil {
			t.Fatal("nil-safe")
		}
		other := h.d.createSession(h.proj, "default", "default")
		h.d.open(s.id)
		if h.d.viewFor(s) != h.d.chat {
			t.Fatal("the current session shows in the main chat")
		}
		if h.d.viewFor(other) != nil {
			t.Fatal("a session shown nowhere has no view")
		}
		h.d.kmemWin = &kmemWindow{s: other, view: newChatViewWith(h.d, func() float32 { return 600 }, func() float32 { return 0 })}
		if h.d.viewFor(other) != h.d.kmemWin.view || h.d.kmemWin.session() != other {
			t.Fatal("the kmem window's session shows in the window")
		}
		h.d.kmemWin = nil
	})
}
