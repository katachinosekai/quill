package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

func TestBridgeStartsAnEphemeralThread(t *testing.T) {
	var server, quill bytes.Buffer
	b := &bridge{
		opts:     Options{SessionID: "quill-id", Cwd: "/project"},
		toServer: json.NewEncoder(&server), toQuill: json.NewEncoder(&quill),
		pending: map[string]func(rpcMessage) error{},
	}
	if err := b.initialize("init"); err != nil {
		t.Fatal(err)
	}
	server.Reset()
	if err := b.receive(rpcMessage{ID: json.RawMessage("1"), Result: json.RawMessage("{}")}); err != nil {
		t.Fatal(err)
	}
	read := json.NewDecoder(&server)
	var initialized rpcMessage
	if err := read.Decode(&initialized); err != nil || initialized.Method != "initialized" {
		t.Fatalf("initialization notification: %+v (%v)", initialized, err)
	}
	var start struct {
		Method string
		Params struct {
			Ephemeral bool
			ThreadID  string
		}
	}
	if err := read.Decode(&start); err != nil || start.Method != "thread/start" || !start.Params.Ephemeral || start.Params.ThreadID != "" {
		t.Fatalf("expected an ephemeral new thread: %+v (%v)", start, err)
	}
}

func TestOperatorKmemBriefIsSentOnTheFirstTurn(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_TEST_KMEM_BRIEF", "1")
	for _, tc := range []struct {
		name    string
		command []string
		topic   string
		context string
	}{
		{"new operator", []string{exe, "-test.run=^TestKmemBriefHelper$", "--"}, "codex-quill", "fixture kmem brief"},
		{"missing launcher", []string{t.TempDir() + "/missing-kmem"}, "codex-quill", "kmem did not return a brief"},
		{"kmem unavailable", nil, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var server bytes.Buffer
			b := &bridge{
				opts:     Options{Cwd: t.TempDir(), KmemCommand: tc.command, KmemTopic: tc.topic},
				toServer: json.NewEncoder(&server),
				pending:  map[string]func(rpcMessage) error{}, threadID: "thread",
			}
			start := func(prompt string) []string {
				t.Helper()
				if err := b.startTurn(prompt); err != nil {
					t.Fatal(err)
				}
				var call struct {
					ID     json.RawMessage
					Params struct{ Input []struct{ Text string } }
				}
				if err := json.Unmarshal(server.Bytes(), &call); err != nil {
					t.Fatal(err)
				}
				server.Reset()
				if err := b.receive(rpcMessage{ID: call.ID, Result: json.RawMessage(`{"turn":{"id":"turn"}}`)}); err != nil {
					t.Fatal(err)
				}
				b.turnID = ""
				var texts []string
				for _, input := range call.Params.Input {
					texts = append(texts, input.Text)
				}
				return texts
			}
			first := start("first prompt")
			if tc.context == "" {
				if !slices.Equal(first, []string{"first prompt"}) {
					t.Fatalf("ordinary tab input: %q", first)
				}
			} else if len(first) != 2 || !strings.Contains(first[0], tc.context) || !strings.HasSuffix(first[1], "first prompt") {
				t.Fatalf("first turn should include the brief status and prompt: %q", first)
			}
			if tc.name == "new operator" &&
				(!strings.Contains(first[0], "fixture topic context") || !strings.Contains(first[0], "fixture checkpoint") ||
					!strings.Contains(first[0], "K6. Fixture coordinator rule") ||
					!strings.Contains(first[0], "12. Fixture local rule") ||
					strings.Contains(first[0], "internal-test-path")) {
				t.Fatalf("missing assigned topic and checkpoint: %q", first)
			}
			if next := start("next prompt"); !slices.Equal(next, []string{"next prompt"}) {
				t.Fatalf("second turn should carry only its prompt: %q", next)
			}

		})
	}
}

func TestKmemBriefHelper(t *testing.T) {
	if os.Getenv("QUILL_TEST_KMEM_BRIEF") != "1" {
		return
	}
	args := os.Args[slices.Index(os.Args, "--")+1:]
	switch {
	case slices.Equal(args, []string{"brief"}):
		fmt.Println("fixture kmem brief")
	case slices.Equal(args, []string{"rules", "--json"}):
		fmt.Fprintln(os.Stderr, "unrelated warning")
		fmt.Println(`{"rules":{"file":"internal-test-path","rules":[{"text":"K6. Fixture coordinator rule","part":"core"},{"text":"12. Fixture local rule","part":"local"}]}}`)
	case slices.Equal(args, []string{"context", "--topic", "codex-quill", "--cap", "6000", "--", "Fix queue --topic unrelated"}):
		fmt.Println("KMEM-BRIEF: exact assigned task")
	case slices.Equal(args, []string{"context", "--topic", "codex-quill"}):
		fmt.Println("fixture topic context")
	case slices.Equal(args, []string{"handoff", "get", "codex-quill"}):
		fmt.Println("fixture checkpoint")
	case slices.Equal(args, []string{"--as", "codex-quill", "handoff", "save", "codex-quill", "--state", "Session checkpoint: kept the test passing"}):
		fmt.Println("saved the assigned topic")
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func TestFollowUpDuringCodexTurn(t *testing.T) {
	for _, outcome := range []string{"accepted", "rejected", "completion before rejection"} {
		t.Run(outcome, func(t *testing.T) {
			var server, quill bytes.Buffer
			b := &bridge{
				toServer: json.NewEncoder(&server), toQuill: json.NewEncoder(&quill),
				pending: map[string]func(rpcMessage) error{}, threadID: "thread", ready: true,
			}
			send := func(text string) {
				t.Helper()
				ev, err := protocol.ParseLine(protocol.UserMessage(text))
				if err != nil {
					t.Fatal(err)
				}
				if err := b.input(ev); err != nil {
					t.Fatal(err)
				}
			}
			call := func(method, turn, text string) json.RawMessage {
				t.Helper()
				var req struct {
					Method string
					ID     json.RawMessage
					Params struct {
						ThreadID, ExpectedTurnID string
						Input                    []struct{ Text string }
					}
				}
				if err := json.NewDecoder(&server).Decode(&req); err != nil {
					t.Fatal(err)
				}
				if req.Method != method || req.Params.ThreadID != "thread" ||
					req.Params.ExpectedTurnID != turn || len(req.Params.Input) != 1 || req.Params.Input[0].Text != text {
					t.Fatalf("unexpected app server request: %+v", req)
				}
				return req.ID
			}
			reply := func(line string) {
				t.Helper()
				var msg rpcMessage
				if err := json.Unmarshal([]byte(line), &msg); err != nil {
					t.Fatal(err)
				}
				if err := b.receive(msg); err != nil {
					t.Fatal(err)
				}
			}
			complete := func(turn string) {
				t.Helper()
				reply(fmt.Sprintf(`{"method":"turn/completed","params":{"threadId":"thread","turn":{"id":%q,"status":"completed"}}}`, turn))
			}
			result := func(subtype string) {
				t.Helper()
				var ev protocol.Event
				if err := json.NewDecoder(&quill).Decode(&ev); err != nil || ev.Type != "result" || ev.Subtype != subtype {
					t.Fatalf("expected result subtype %q, got %+v (%v)", subtype, ev, err)
				}
			}
			send("first")
			start := call("turn/start", "", "first")
			send("follow up")
			if server.Len() != 0 {
				t.Fatal("follow-up must wait for the active turn ID")
			}
			reply(fmt.Sprintf(`{"id":%s,"result":{"turn":{"id":"turn-1"}}}`, start))
			steer := call("turn/steer", "turn-1", "follow up")
			if outcome == "accepted" {
				reply(fmt.Sprintf(`{"id":%s,"result":{"turnId":"turn-1"}}`, steer))
				complete("turn-1")
				result("")
				if server.Len() != 0 {
					t.Fatal("accepted follow-up must not be sent again")
				}
				return
			}
			if outcome == "completion before rejection" {
				complete("turn-1")
				if quill.Len() != 0 {
					t.Fatal("completion must wait for the steering reply")
				}
			}
			reply(fmt.Sprintf(`{"id":%s,"error":{"message":"No active turn"}}`, steer))
			if outcome == "rejected" {
				if server.Len() != 0 {
					t.Fatal("a refused steer must wait until the current turn ends")
				}
				complete("turn-1")
			}
			result(protocol.ResultContinuing)
			next := call("turn/start", "", "follow up")
			reply(fmt.Sprintf(`{"id":%s,"result":{"turn":{"id":"turn-2"}}}`, next))
			complete("turn-2")
			result("")
		})
	}
}

func TestCodexActivityChips(t *testing.T) {
	var quill bytes.Buffer
	b := &bridge{toQuill: json.NewEncoder(&quill), threadID: "thread", turnID: "turn", fileChanges: map[string]json.RawMessage{}}
	c := chat.New()
	notify := func(method, body string) {
		t.Helper()
		if err := b.notification(rpcMessage{Method: method, Params: json.RawMessage(body)}); err != nil {
			t.Fatal(err)
		}
		for quill.Len() != 0 {
			line, _ := quill.ReadBytes('\n')
			ev, err := protocol.ParseLine(line)
			if err != nil {
				t.Fatal(err)
			}
			c.Apply(ev)
		}
	}
	notify("item/started", `{"threadId":"thread","item":{"id":"cmd","type":"commandExecution","command":"cat /project/.agents/skills/docs/SKILL.md","cwd":"/project","status":"inProgress","commandActions":[{"type":"read","path":"/project/.agents/skills/docs/SKILL.md"}]}}`)
	if len(c.Items) != 1 || c.Items[0].ToolName != "Skill: docs" || c.Items[0].HasResult {
		t.Fatalf("pending skill chip: %+v", c.Items)
	}
	notify("item/completed", `{"threadId":"thread","item":{"id":"cmd","type":"commandExecution","command":"cat /project/.agents/skills/docs/SKILL.md","cwd":"/project","status":"failed","exitCode":1,"aggregatedOutput":"couldn't read","commandActions":[{"type":"read","path":"/project/.agents/skills/docs/SKILL.md"}]}}`)
	if it := c.Items[0]; len(c.Items) != 1 || !it.HasResult || !it.ResultError || it.Result != "couldn't read" {
		t.Fatalf("completed command: %+v", it)
	}
	notify("item/started", `{"threadId":"thread","item":{"id":"change","type":"fileChange","status":"inProgress","changes":[]}}`)
	notify("item/fileChange/patchUpdated", `{"threadId":"thread","itemId":"change","changes":[{"path":"main.go","diff":"@@ -1 +1 @@\n-old\n+new"}]}`)
	notify("item/completed", `{"threadId":"thread","item":{"id":"change","type":"fileChange","status":"completed","changes":[]}}`)
	if it := c.Items[1]; it.ToolName != "File changes" || !it.HasResult || !strings.Contains(string(it.Input), "+new") {
		t.Fatalf("file-change details: %+v", it)
	}
	notify("item/completed", `{"threadId":"thread","item":{"id":"mcp","type":"mcpToolCall","server":"project","tool":"lookup","arguments":{"path":"main.go"},"status":"completed","result":{"content":[{"type":"text","text":"found"}]}}}`)
	if it := c.Items[2]; it.ToolName != "Tool: project/lookup" || !it.HasResult || it.Result != "found" {
		t.Fatalf("MCP tool chip: %+v", it)
	}
	notify("item/completed", `{"threadId":"thread","item":{"id":"mem","type":"dynamicToolCall","tool":"kmem_checkpoint","arguments":{"operation":"save","key":"private input"},"status":"completed","success":true,"contentItems":[{"type":"inputText","text":"private output"}]}}`)
	if it := c.Items[3]; it.ToolName != "Tool: kmem_checkpoint" || !it.HasResult || it.Result != "" || strings.Contains(string(it.Input), "private") {
		t.Fatalf("checkpoint chip: %+v", it)
	}
	notify("item/started", `{"threadId":"thread","item":{"id":"pending","type":"commandExecution","command":"echo pending","status":"inProgress"}}`)
	notify("turn/completed", `{"threadId":"thread","turn":{"id":"turn","status":"interrupted"}}`)
	if it := c.Items[4]; len(c.Items) != 5 || !it.HasResult || !it.ResultError {
		t.Fatalf("unfinished command after interrupt: %+v", it)
	}
}

func TestCodexParentTurnNotBlockedByWorker(t *testing.T) {
	var server, quill bytes.Buffer
	b := &bridge{toServer: json.NewEncoder(&server), toQuill: json.NewEncoder(&quill),
		pending: map[string]func(rpcMessage) error{}, threadID: "parent", ready: true}
	send := func(text string) {
		t.Helper()
		ev, err := protocol.ParseLine(protocol.UserMessage(text))
		if err != nil {
			t.Fatal(err)
		}
		if err := b.input(ev); err != nil {
			t.Fatal(err)
		}
	}
	receive := func(method, params string) {
		t.Helper()
		if err := b.receive(rpcMessage{Method: method, Params: json.RawMessage(params)}); err != nil {
			t.Fatal(err)
		}
	}
	read := func(wantType, wantSubtype string) {
		t.Helper()
		line, err := quill.ReadBytes('\n')
		ev, parseErr := protocol.ParseLine(line)
		if err != nil || parseErr != nil || ev.Type != wantType || ev.Subtype != wantSubtype {
			t.Fatalf("wanted %s/%s: %s (%v, %v)", wantType, wantSubtype, line, err, parseErr)
		}
	}
	send("first")
	var req rpcMessage
	line, err := server.ReadBytes('\n')
	if err != nil || json.Unmarshal(line, &req) != nil || req.Method != "turn/start" {
		t.Fatalf("expected turn/start: %s (%v)", line, err)
	}
	receive("turn/started", `{"threadId":"parent","turn":{"id":"parent-turn"}}`)
	read("system", protocol.CodexParentTurnStarted)
	receive("item/started", `{"threadId":"worker","item":{"id":"foreign-wait","type":"collabAgentToolCall","tool":"wait"}}`)
	if quill.Len() != 0 {
		t.Fatal("a worker's own wait is not the parent's wait")
	}
	receive("item/started", `{"threadId":"parent","item":{"id":"wait","type":"collabAgentToolCall","tool":"wait"}}`)
	read("assistant", "")
	read("system", protocol.CodexAgentWaitStarted)
	receive("turn/started", `{"threadId":"parent","turn":{"id":"parent-turn"}}`)
	if quill.Len() != 0 || !b.waitingForAgents {
		t.Fatal("a duplicate parent start cannot reset wait availability")
	}
	receive("item/completed", `{"threadId":"parent","item":{"id":"wait","type":"collabAgentToolCall","tool":"wait","status":"completed"}}`)
	read("assistant", "")
	read("user", "")
	read("system", protocol.CodexAgentWaitEnded)
	receive("turn/completed", `{"threadId":"worker","turn":{"id":"parent-turn","status":"completed"}}`)
	if quill.Len() != 0 || b.turnID != "parent-turn" {
		t.Fatal("worker completion must not finish the parent")
	}
	receive("turn/completed", `{"threadId":"parent","turn":{"id":"parent-turn","status":"completed"}}`)
	if err := b.receive(rpcMessage{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"parent-turn"}}`)}); err != nil {
		t.Fatal(err)
	}
	read("result", "")
	if quill.Len() != 0 || b.turnID != "" || b.starting {
		t.Fatal("an early parent result must finish once without leaving the bridge busy")
	}
	receive("item/started", `{"threadId":"parent","turnId":"parent-turn","item":{"id":"late","type":"commandExecution"}}`)
	if quill.Len() != 0 || len(b.activeItems) != 0 {
		t.Fatal("late activity must not block a subsequent turn's wait")
	}
	send("reachable with worker active")
	line, err = server.ReadBytes('\n')
	if err != nil || json.Unmarshal(line, &req) != nil || req.Method != "turn/start" {
		t.Fatalf("expected a fresh turn while the child still runs: %s (%v)", line, err)
	}
}

func TestCodexTaskBriefUsesAssignedTopic(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_TEST_KMEM_BRIEF", "1")
	var server bytes.Buffer
	b := &bridge{toServer: json.NewEncoder(&server), threadID: "thread", turnID: "turn",
		opts: Options{KmemCommand: []string{exe, "-test.run=^TestKmemBriefHelper$", "--"}, KmemTopic: "codex-quill"}}
	msg := rpcMessage{ID: json.RawMessage("1"), Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","tool":"kmem_read","arguments":{"operation":"context","key":"Fix queue --topic unrelated"}}`)}
	if err := b.dynamicCall(msg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(server.String(), "KMEM-BRIEF: exact assigned task") {
		t.Fatalf("task briefing did not use the bridge's assigned topic: %s", server.String())
	}
}

func TestCodexInputAdmittedJustAfterWaitStaysQueued(t *testing.T) {
	var server, quill bytes.Buffer
	b := &bridge{toServer: json.NewEncoder(&server), toQuill: json.NewEncoder(&quill),
		pending: map[string]func(rpcMessage) error{}, threadID: "thread", turnID: "turn", ready: true,
		opts: Options{SteerOnlyWhileWaiting: true}}
	ev, err := protocol.ParseLine(protocol.UserMessage("late follow-up"))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.input(ev); err != nil {
		t.Fatal(err)
	}
	if server.Len() != 0 || len(b.queued) != 1 {
		t.Fatal("a follow-up arriving as the parent resumes work must remain queued")
	}
	b.activeItems = map[string]bool{"parent wait": true}
	if err := b.syncAgentWait(); err != nil {
		t.Fatal(err)
	}
	var req rpcMessage
	if err := json.Unmarshal(server.Bytes(), &req); err != nil || req.Method != "turn/steer" || len(b.queued) != 0 {
		t.Fatalf("the new wait should enable delivery: %+v (%v)", req, err)
	}
}

func TestCodexSendNowSteersAfterNativeApproval(t *testing.T) {
	var server, quill bytes.Buffer
	b := &bridge{
		toServer: json.NewEncoder(&server), toQuill: json.NewEncoder(&quill),
		pending:   map[string]func(rpcMessage) error{},
		approvals: map[string]approval{"7": {id: json.RawMessage("7")}},
		threadID:  "thread", turnID: "turn", ready: true,
		opts: Options{Steer: true, SteerOnlyWhileWaiting: true, ForceSteer: true},
	}
	ev, err := protocol.ParseLine(protocol.SteerMessage("Please reconsider the approach"))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.input(ev); err != nil {
		t.Fatal(err)
	}
	if server.Len() != 0 || len(b.queued) != 1 {
		t.Fatal("send-now must wait for the native approval")
	}
	if err := b.answer(&protocol.ControlResponseBody{RequestID: "7", Response: json.RawMessage(`{"behavior":"deny"}`)}); err != nil {
		t.Fatal(err)
	}
	var answer, steer rpcMessage
	dec := json.NewDecoder(&server)
	if err := dec.Decode(&answer); err != nil || string(answer.ID) != "7" {
		t.Fatalf("expected approval response first: %+v (%v)", answer, err)
	}
	if err := dec.Decode(&steer); err != nil || steer.Method != "turn/steer" ||
		!strings.Contains(string(steer.Params), "Please reconsider the approach") || len(b.queued) != 0 {
		t.Fatalf("expected explicit steer without waiting for a worker: %+v (%v)", steer, err)
	}
}

func TestCodexWorkerStateOutlivesParentTurn(t *testing.T) {
	var events bytes.Buffer
	b := &bridge{threadID: "parent", toQuill: json.NewEncoder(&events)}
	spawn := activityItem{Type: "collabAgentToolCall", Tool: "spawnAgent", ID: "spawn", Status: "completed",
		SenderThreadID: "parent", ReceiverThreadIDs: []string{"worker"}}
	if err := b.activity(spawn, true); err != nil {
		t.Fatal(err)
	}
	if !b.workers["worker"] || !strings.Contains(events.String(), protocol.CodexWorkersStarted) {
		t.Fatal("the bridge did not announce the running worker")
	}
	if err := b.finish("completed", ""); err != nil {
		t.Fatal(err)
	}
	if !b.workers["worker"] {
		t.Fatal("a parent turn ending cleared a live worker")
	}
	wait := activityItem{Type: "collabAgentToolCall", Tool: "wait", ID: "wait", Status: "completed",
		SenderThreadID: "parent", AgentsStates: map[string]struct{ Status string }{"worker": {Status: "completed"}}}
	if err := b.activity(wait, true); err != nil {
		t.Fatal(err)
	}
	if len(b.workers) != 0 || !strings.Contains(events.String(), protocol.CodexWorkersEnded) {
		t.Fatal("the bridge did not announce that the worker finished")
	}
}

func TestCodexSubAgentActivity(t *testing.T) {
	var events bytes.Buffer
	b := &bridge{threadID: "parent", turnID: "parent-turn", toQuill: json.NewEncoder(&events)}
	conversation := chat.New()
	notify := func(method, thread, item, kind, worker string) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"threadId": thread, "turnId": "parent-turn",
			"item": map[string]string{"type": "subAgentActivity", "id": item, "kind": kind,
				"agentThreadId": worker, "agentPath": "/root/" + worker},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := b.notification(rpcMessage{Method: method, Params: body}); err != nil {
			t.Fatal(err)
		}
		for events.Len() > 0 {
			line, _ := events.ReadBytes('\n')
			ev, err := protocol.ParseLine(line)
			if err != nil {
				t.Fatal(err)
			}
			switch ev.Subtype {
			case "task_progress", "task_notification", protocol.CodexWorkersStarted, protocol.CodexWorkersEnded:
			default:
				t.Fatalf("unexpected lifecycle event %q", ev.Subtype)
			}
			conversation.Apply(ev)
		}
	}
	notify("item/started", "foreign", "a", "started", "first")
	notify("item/completed", "", "b", "started", "second")
	notify("item/completed", "parent", "c", "started", "parent")
	notify("item/completed", "parent", "d", "interacted", "first")
	notify("item/completed", "parent", "e", "interrupted", "unknown")
	if len(conversation.Agents()) != 0 {
		t.Fatal("only a confirmed worker on the owning thread should appear")
	}
	notify("item/started", "parent", "a", "started", "first")
	notify("item/completed", "parent", "b", "started", "second")
	agents := conversation.Agents()
	if len(agents) != 2 || agents[0].ID != "first" || agents[0].Name != "/root/first" || conversation.RunningAgents() != 2 {
		t.Fatalf("expected both named workers: %+v", agents)
	}
	b.turnID = "" // Workers can finish after the parent turn.
	notify("item/completed", "foreign", "x", "interrupted", "first")
	notify("item/completed", "parent", "f", "completed", "first")
	notify("item/completed", "parent", "a", "started", "first") // Late second announcement of a.
	notify("item/completed", "parent", "g", "interacted", "first")
	agents = conversation.Agents()
	if agents[0].Status != "completed" || !agents[1].Running() {
		t.Fatalf("completion or replay changed the wrong workers: %+v", agents)
	}
	notify("item/started", "parent", "h", "started", "first")
	notify("item/completed", "parent", "f", "completed", "first") // Old completion after a fresh start.
	notify("item/completed", "parent", "i", "interrupted", "second")
	agents = conversation.Agents()
	if len(agents) != 2 || !agents[0].Running() || agents[1].Status != "stopped" {
		t.Fatalf("expected one resumed worker and one stopped worker: %+v", agents)
	}
}

func TestCodexIndividualWorkersReachConversation(t *testing.T) {
	var events bytes.Buffer
	b := &bridge{threadID: "parent", turnID: "parent-turn", toQuill: json.NewEncoder(&events)}
	conversation := chat.New()
	notify := func(method, body string) {
		t.Helper()
		if err := b.notification(rpcMessage{Method: method, Params: json.RawMessage(body)}); err != nil {
			t.Fatal(err)
		}
		for events.Len() != 0 {
			line, _ := events.ReadBytes('\n')
			event, err := protocol.ParseLine(line)
			if err != nil {
				t.Fatal(err)
			}
			conversation.Apply(event)
		}
	}
	notify("item/completed", `{"threadId":"parent","turnId":"parent-turn","item":{"id":"spawn","type":"collabAgentToolCall","tool":"spawn_agent","prompt":"First worker","status":"completed","receiverThreadIds":["worker-a"]}}`)
	notify("item/completed", `{"threadId":"parent","turnId":"parent-turn","item":{"id":"spawn-2","type":"collabAgentToolCall","tool":"spawnAgent","prompt":"Second worker","status":"completed","receiverThreadIds":["worker-b"]}}`)
	agents := conversation.Agents()
	if len(agents) != 2 || agents[0].Name != "First worker" || agents[1].ID != "worker-b" || conversation.RunningAgents() != 2 {
		t.Fatalf("expected two separately named active workers, got %+v", agents)
	}
	notify("turn/completed", `{"threadId":"unrelated","turn":{"id":"unrelated-turn","status":"completed"}}`)
	notify("turn/completed", `{"threadId":"worker-a","turn":{"id":"child-turn","status":"completed"}}`)
	agents = conversation.Agents()
	if agents[0].Status != "completed" || agents[0].Running() || !agents[1].Running() {
		t.Fatalf("only the completed worker should stop, got %+v", agents)
	}
	notify("item/completed", `{"threadId":"parent","turnId":"parent-turn","item":{"id":"resume","type":"collabAgentToolCall","tool":"resumeAgent","status":"completed","receiverThreadIds":["worker-a"]}}`)
	agents = conversation.Agents()
	if len(agents) != 2 || agents[0].Name != "First worker" || !agents[0].Running() {
		t.Fatalf("resuming should reopen the existing worker, got %+v", agents)
	}
}
