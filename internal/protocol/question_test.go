package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnswerQuestions(t *testing.T) {
	in := json.RawMessage(`{"questions":[{"question":"Which?","header":"Pick","multiSelect":false,"options":[{"label":"A","description":"first"},{"label":"B"}]}]}`)
	qs := ParseQuestions(in)
	if len(qs) != 1 || len(qs[0].Options) != 2 || qs[0].Options[0].Description != "first" {
		t.Fatalf("parse: %+v", qs)
	}
	line := string(AnswerQuestions("r1", &ControlRequest{Input: in}, map[string]string{"Which?": "B"}))
	for _, want := range []string{`"behavior":"allow"`, `"answers":{"Which?":"B"}`, `"questions":[`, `"request_id":"r1"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("missing %s in %s", want, line)
		}
	}
}
