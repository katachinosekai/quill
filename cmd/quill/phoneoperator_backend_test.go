package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestPhoneOperatorSelectedBackend(t *testing.T) {
	p := quillPhoneSetup(t)
	h := p.h
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_CODEX_BIN", self)
	t.Setenv("QUILL_HANDOFF_FAKE", "1")
	t.Setenv("QUILL_TEST_OPERATOR_KMEM_BRIEF", "1")
	h.d.cfg.CodexRoot = t.TempDir()
	h.ui(func() { h.d.app.Preferences().SetBool("phoneControl", true) })

	for _, tc := range []struct {
		name, pref, items, command, key string
		claude, codex                   *assistRole
	}{
		{"quill", "quillAgentCodex", "/phone/quill/operator/items", "/phone/quill/operator/", "operator", quillRole, codexQuillRole},
		{"kmem", "kmemAgentCodex", "/phone/kmem/assistant/items", "/phone/kmem/assistant/", "assistant", kmemRole, codexKmemRole},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var original *deskSession
			h.ui(func() {
				d := h.d
				as := d.assistFor(tc.claude)
				original = d.addSessionWithBackend(h.proj, "default", "default", false, false)
				d.markHiddenAs(original, tc.claude.hidden)
				original.conv.AddUser("fixture Claude history " + tc.name)
				as.s = original
			})
			code, before, _ := p.a.do("GET", tc.items, "", nil)
			if code != 200 || !strings.Contains(fmt.Sprint(before["items"]), "fixture Claude history "+tc.name) {
				t.Fatalf("Claude items: %d %v", code, before)
			}
			h.ui(func() { h.d.app.Preferences().SetBool(tc.pref, true) })
			code, result, _ := p.a.do("POST", tc.command+"start", tc.name+"-fixture-start", map[string]any{})
			if code != 200 {
				t.Fatalf("Codex start: %d %v", code, result)
			}
			h.eventually("selected Codex operator is running", func() bool {
				a := h.d.assistFor(tc.codex)
				return a.s != nil && a.s.running() && !a.s.conv.Busy && !a.s.proc.Busy()
			})
			query := fmt.Sprintf("?epoch=%.0f&since=%.0f", before["epoch"], before["rev"])
			code, after, _ := p.a.do("GET", tc.items+query, "", nil)
			model, _ := after[tc.key].(map[string]any)
			if code != 200 || after["reset"] != true || after["epoch"] == before["epoch"] ||
				model["epoch"] != after["epoch"] || model["state"] != "live" || !hasAction(model, "send") ||
				hasAction(model, "stop") || strings.Contains(fmt.Sprint(after["items"]), "fixture Claude history") {
				t.Fatalf("Codex items after backend switch: %d %v", code, after)
			}
			text := "fixture phone message " + tc.name
			code, result, _ = p.a.do("POST", tc.command+"send", tc.name+"-fixture-send", map[string]any{"text": text})
			if code != 200 {
				t.Fatalf("Codex send: %d %v", code, result)
			}
			code, after, _ = p.a.do("GET", tc.items, "", nil)
			if code != 200 || !strings.Contains(fmt.Sprint(after["items"]), text) {
				t.Fatalf("phone message missing from Codex history: %d %v", code, after)
			}
			h.ui(func() {
				h.d.app.Preferences().SetBool(tc.pref, false)
				for _, it := range original.conv.Items {
					if it.Text == text {
						t.Fatal("phone message also reached the other backend")
					}
				}
			})
		})
	}
}
