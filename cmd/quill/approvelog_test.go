package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApprovalFromTranscript(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	write := func(lines ...string) {
		b := []byte{}
		for _, l := range lines {
			b = append(b, l+"\n"...)
		}
		os.WriteFile(p, b, 0o600)
	}
	write(
		`{"type":"user","message":{"role":"user","content":"fix the tabs"},"origin":{"kind":"human"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Plan: 1. edit"}]}}`,
		`{"type":"user","message":{"role":"user","content":"approve"},"origin":{"kind":"human"}}`,
		`{"type":"user","message":{"role":"user","content":"Another Claude session sent a message: yes"},"origin":{"kind":"peer"}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"denied"}]}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"<system-reminder>yes</system-reminder>"}}`,
	)
	if text, ok := lastHumanMessage(p); !ok || text != "approve" {
		t.Fatalf("the last human message, skipping peers, tool results and meta: %q %v", text, ok)
	}
	write(
		`{"type":"user","message":{"role":"user","content":"approve"},"origin":{"kind":"human"}}`,
		`{"type":"user","message":{"role":"user","content":"now do something else"},"origin":{"kind":"human"}}`,
	)
	if text, _ := lastHumanMessage(p); isApproval(text) {
		t.Fatal("a newer request is not an approval")
	}
}
