package codex

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/katachinosekai/quill/internal/protocol"
)

func TestSettingsWriteWaitsForOneExplicitApproval(t *testing.T) {
	var server, quill bytes.Buffer
	b := &bridge{
		opts:     Options{SettingsBinary: "quill"},
		toServer: json.NewEncoder(&server), toQuill: json.NewEncoder(&quill),
		approvals: map[string]approval{}, threadID: "thread", turnID: "turn",
	}
	request := rpcMessage{ID: json.RawMessage(`7`), Params: json.RawMessage(`{
		"threadId":"thread","turnId":"turn","tool":"quill_settings",
		"arguments":{"operation":"set","key":"layout","value":"vertical","reason":"requested"}
	}`)}
	if err := b.dynamicCall(request); err != nil {
		t.Fatal(err)
	}
	if server.Len() != 0 {
		t.Fatal("the write should wait for Quill to reply")
	}
	event, err := protocol.ParseLine(quill.Bytes())
	if err != nil || event.RequestID != "7" || event.Request.ToolName != SettingsApproval {
		t.Fatalf("missing approval request: %s (%v)", &quill, err)
	}
	runs := 0
	pending := b.approvals["7"]
	pending.run = func() error { runs++; return nil }
	b.approvals["7"] = pending
	allow := &protocol.ControlResponseBody{RequestID: "7", Response: json.RawMessage(`{"behavior":"allow"}`)}
	for range 2 {
		if err := b.answer(allow); err != nil {
			t.Fatal(err)
		}
	}
	if runs != 1 {
		t.Fatalf("the approved operation ran %d times", runs)
	}
}

func TestDedicatedToolsRestrictKmemAndStripSettingsIdentity(t *testing.T) {
	t.Setenv("QUILL_SESSION_ID", "private-id")
	t.Setenv("QUILL_SESSION_TOKEN", "private-token")
	for _, entry := range serverEnv() {
		if strings.HasPrefix(entry, "QUILL_SESSION_ID=") || strings.HasPrefix(entry, "QUILL_SESSION_TOKEN=") {
			t.Fatal("Quill's settings credentials must stay out of the app server environment")
		}
	}
	if !slices.Equal(kmemArgs(toolArguments{Operation: "find", Key: "search text"}), []string{"find", "--", "search text"}) {
		t.Fatal("kmem searches must pass the query as data")
	}
	for _, args := range []toolArguments{{Operation: "claim", Key: "task"}, {Operation: "handoff", Key: "--as=quill"}} {
		if kmemArgs(args) != nil {
			t.Errorf("unexpectedly permitted kmem arguments: %+v", args)
		}
	}
}

func TestKmemCheckpointUsesTheAssignedTopic(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_TEST_KMEM_BRIEF", "1")
	var server bytes.Buffer
	b := &bridge{
		opts: Options{
			Cwd: t.TempDir(), KmemTopic: "codex-quill",
			KmemCommand: []string{exe, "-test.run=^TestKmemBriefHelper$", "--"},
		},
		toServer: json.NewEncoder(&server), threadID: "thread", turnID: "turn",
	}
	request := rpcMessage{ID: json.RawMessage(`7`), Params: json.RawMessage(`{
		"threadId":"thread","turnId":"turn","tool":"kmem_checkpoint",
		"arguments":{"operation":"save","key":"kept the test passing"}
	}`)}
	if err := b.dynamicCall(request); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(server.String(), "saved the assigned topic") || !strings.Contains(server.String(), `"success":true`) {
		t.Fatalf("checkpoint did not save through kmem: %s", &server)
	}
}
