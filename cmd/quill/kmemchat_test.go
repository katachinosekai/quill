package main

import (
	"encoding/json"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

const kmemChatReply = "Two things before I write these.\n\n" +
	"1. **How should \"Plan approval scope\" be stored? Pinned fact now, rule at the migration (recommended), shared rule now, or not at all?**\n\n" +
	"2. **When should I start t1b6536? Write it now and release it as 1.10.0 after the 1.9.1 rollout (recommended), wait until after the rollout, or release it as soon as it's ready?**"

// The kmem assistant's reply ending in two numbered option questions: the
// items JSON the phone gets (text, plan card, questions).
func TestKmemChatItemsJSON(t *testing.T) {
	qs := planQuestions(kmemChatReply)
	if len(qs) != 2 {
		t.Fatalf("questions: %+v", qs)
	}
	for i, q := range qs {
		if q.Kind != "single" || len(q.Options) < 3 {
			t.Errorf("question %d: %+v", i, q)
		}
	}
	p := kmemAssistPhoneSetup(t)
	h := p.h
	h.ui(func() { h.d.kmemAssistBoot() })
	s := p.idle()
	h.ui(func() {
		h.d.planFirst = true
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, InPlan: true, Text: kmemChatReply})
		s.conv.AddPlan()
	})
	body := p.items("?limit=20")
	var gotText, gotPlan bool
	for _, x := range body["items"].([]any) {
		m := x.(map[string]any)
		if m["text"] == kmemChatReply {
			gotText = true
		}
		if m["kind"] == "plan" {
			req := m["request"].(map[string]any)
			gotPlan = len(req["questions"].([]any)) == 2 && req["approval"] != nil
		}
	}
	if !gotText || !gotPlan {
		b, _ := json.MarshalIndent(body, "", " ")
		t.Fatalf("reply text %v, plan card with questions %v: %s", gotText, gotPlan, b)
	}
	// The unset setting is Online Claude (default), not "New sessions".
	if as := assistOf(body); as["modelSource"] != "Online Claude (default)" {
		t.Fatalf("modelSource: %v", as["modelSource"])
	}
}
