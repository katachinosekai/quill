package chat

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/katachinosekai/quill/internal/protocol"
)

func replay(t *testing.T, c *Conversation, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		ev, err := protocol.ParseLine(sc.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		c.Apply(ev)
	}
}

func kinds(c *Conversation) string {
	var b strings.Builder
	for _, it := range c.Items {
		b.WriteString([]string{"U", "A", "T", "P", "N", "E"}[it.Kind])
	}
	return b.String()
}

func TestStreamRun1(t *testing.T) {
	c := New()
	c.AddUser("Use the Write tool")
	replay(t, c, "../protocol/testdata/run1.jsonl")
	c.AddUser("Write the numbers")
	if c.SessionID == "" || c.Model == "" {
		t.Fatalf("init not applied: %q %q", c.SessionID, c.Model)
	}
	if got := kinds(c); got != "UTAAN" && got != "UTAUAN" {
		t.Logf("kinds %s", got)
	}
	var tool, text *Item
	for _, it := range c.Items {
		if it.Kind == Tool && tool == nil {
			tool = it
		}
		if it.Kind == Assistant && text == nil {
			text = it
		}
	}
	if tool == nil || tool.ToolName != "Write" || !tool.HasResult || !tool.ResultError || tool.Summary != "/tmp/ws/hi.txt" {
		t.Fatalf("tool %+v", tool)
	}
	if len(tool.Diff) != 1 || tool.Diff[0].Op != '+' || tool.Diff[0].Text != "hi" {
		t.Fatalf("write diff %+v", tool.Diff)
	}
	if text == nil || text.Streaming || !strings.Contains(text.Text, "hi.txt") {
		t.Fatalf("text %+v", text)
	}
	for _, it := range c.Items {
		if it.Streaming {
			t.Errorf("item still streaming: %+v", it)
		}
	}
	if c.Stats.CostUSD <= 0 || c.Stats.ContextMax != 1000000 || c.Stats.ContextUsed == 0 {
		t.Errorf("stats %+v", c.Stats)
	}
	last := c.Items[len(c.Items)-2]
	if last.Kind != Notice || last.Text != "Interrupted" {
		t.Errorf("expected interrupt notice, got %+v (kinds %s)", last, kinds(c))
	}
}

func TestStreamingDeltasAccumulate(t *testing.T) {
	c := New()
	f, _ := os.Open("../protocol/testdata/run1.jsonl")
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	sawPartial := false
	for sc.Scan() {
		ev, _ := protocol.ParseLine(sc.Bytes())
		for _, it := range c.Apply(ev) {
			if it.Kind == Assistant && it.Streaming && it.Text != "" {
				sawPartial = true
			}
		}
	}
	if !sawPartial {
		t.Fatal("no partial streaming text seen")
	}
}

func TestPermissionRun2(t *testing.T) {
	c := New()
	replay(t, c, "../protocol/testdata/run2.jsonl")
	var perm *Item
	for _, it := range c.Items {
		if it.Kind == Permission {
			perm = it
		}
	}
	if perm == nil || perm.ToolName != "Write" || perm.RequestID == "" || len(perm.Diff) != 1 {
		t.Fatalf("perm %+v", perm)
	}
	if perm.PermState != PermCancelled {
		t.Fatalf("unanswered permission should be cancelled at result, got %v", perm.PermState)
	}
	if c.Permission(perm.RequestID) != perm {
		t.Fatal("lookup")
	}
}

func TestLineDiff(t *testing.T) {
	d := LineDiff("a\nb\nc\n", "a\nB\nc\nd\n")
	var b strings.Builder
	for _, l := range d {
		b.WriteByte(l.Op)
		b.WriteString(l.Text)
		b.WriteByte('|')
	}
	if b.String() != " a|-b|+B| c|+d|" {
		t.Fatal(b.String())
	}
	if a, r := DiffStats(d); a != 2 || r != 1 {
		t.Fatal(a, r)
	}
}

func TestCleanUserText(t *testing.T) {
	if s, ok := CleanUserText("<command-name>/model</command-name><command-args>haiku</command-args>"); !ok || s != "/model haiku" {
		t.Fatal(s, ok)
	}
	if _, ok := CleanUserText("<local-command-stdout></local-command-stdout>"); ok {
		t.Fatal("stdout should be hidden")
	}
	if _, ok := CleanUserText("<system-reminder>x</system-reminder>"); ok {
		t.Fatal("reminder should be hidden")
	}
}

func TestThinkingItems(t *testing.T) {
	c := New()
	ev := func(s string) {
		e, err := protocol.ParseLine([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		c.Apply(e)
	}
	ev(`{"type":"stream_event","api_message_id":"m1","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`)
	if len(c.Items) != 0 {
		t.Fatal("empty thinking should not add an item")
	}
	ev(`{"type":"stream_event","api_message_id":"m1","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm "}}}`)
	ev(`{"type":"stream_event","api_message_id":"m1","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"ok"}}}`)
	if len(c.Items) != 1 || c.Items[0].Kind != Thinking || c.Items[0].Text != "hmm ok" || !c.Items[0].Streaming {
		t.Fatalf("streamed thinking %+v", c.Items)
	}
	ev(`{"type":"stream_event","api_message_id":"m1","event":{"type":"content_block_stop","index":0}}`)
	ev(`{"type":"assistant","message":{"id":"m1","role":"assistant","content":[{"type":"thinking","thinking":"hmm ok"},{"type":"text","text":"hi"}]}}`)
	ev(`{"type":"assistant","message":{"id":"m2","role":"assistant","content":[{"type":"thinking","thinking":""}]}}`)
	if len(c.Items) != 2 || c.Items[0].Streaming || c.Items[1].Kind != Assistant {
		t.Fatalf("items %+v", c.Items)
	}
}

func TestBackgroundTasksCountAsWork(t *testing.T) {
	c := New()
	tool := func(id, name, input string) {
		c.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant", Content: []byte(`[{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}]`)}})
	}
	result := func(id, text string, isErr bool) {
		e := "false"
		if isErr {
			e = "true"
		}
		b, _ := json.Marshal(text)
		c.Apply(&protocol.Event{Type: "user", Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"tool_result","tool_use_id":"` + id + `","is_error":` + e + `,"content":` + string(b) + `}]`)}})
	}
	notify := func(task, status string, meta bool) {
		text := "<task-notification>\n<task-id>" + task + "</task-id>\n<status>" + status + "</status>\n<summary>x</summary>\n</task-notification>"
		b, _ := json.Marshal(text)
		c.Apply(&protocol.Event{Type: "user", IsMeta: meta, Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"text","text":` + string(b) + `}]`)}})
	}
	tool("t1", "Bash", `{"command":"./run-test.sh","run_in_background":true}`)
	result("t1", "Command running in background with ID: b4qamtotj. Output is being written to: /tmp/x/b4qamtotj.output", false)
	tool("t2", "Bash", `{"command":"go test ./..."}`)
	result("t2", "Command did not complete within its 300s timeout and was moved to the background (ID: be90lakpp). Output is being written to: /tmp/x", false)
	tool("t3", "Agent", `{"prompt":"review","description":"review"}`)
	result("t3", "Review done.\nagentId: a1b2c3d4e5 (use SendMessage to continue)", false)
	tool("t4", "Bash", `{"command":"sleep 1","run_in_background":true}`)
	result("t4", "denied", true)
	if c.Background() != 2 {
		t.Fatalf("background shells and timed-out commands count, finished foreground agents and failed calls don't: %d", c.Background())
	}
	if ts := c.BackgroundTasks(); ts[0].ID != "b4qamtotj" || ts[0].What != "./run-test.sh" || ts[0].Log != "/tmp/x/b4qamtotj.output" || ts[1].ID != "be90lakpp" {
		t.Fatalf("tasks keep what they run and their log, in start order: %+v", ts)
	}
	notify("b4qamtotj", "running", false)
	if c.Background() != 2 {
		t.Fatal("a progress notification doesn't end the task")
	}
	notify("b4qamtotj", "completed", true)
	if c.Background() != 1 {
		t.Fatal("a completion notification ends the task, even as a meta message")
	}
	for _, it := range c.Items {
		if it.Kind == User {
			t.Fatalf("task notifications stay hidden: %q", it.Text)
		}
	}
	tool("t5", "TaskStop", `{"task_id":"be90lakpp"}`)
	result("t5", "stopped", false)
	if c.Background() != 0 {
		t.Fatal("TaskStop ends the task")
	}
	tool("t6", "Monitor", `{"command":"tail -f log"}`)
	result("t6", "Monitor started (task ID: m9x8y7z6)", false)
	if c.Background() != 1 {
		t.Fatal("a running Monitor counts")
	}
	c.ClearBackground()
	if c.Background() != 0 {
		t.Fatal("a new process starts with no background tasks")
	}
}

func TestTaskNotificationFromAttachment(t *testing.T) {
	c := New()
	c.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant", Content: []byte(`[{"type":"tool_use","id":"q1","name":"Bash","input":{"command":"./t.sh","run_in_background":true}}]`)}})
	c.Apply(&protocol.Event{Type: "user", Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"tool_result","tool_use_id":"q1","content":"Command running in background with ID: b2b3pwaaq. Output is being written to: /tmp/o"}]`)}})
	if c.Background() != 1 {
		t.Fatal("started")
	}
	raw := []byte(`{"parentUuid":"x","isSidechain":false,"attachment":{"type":"queued_command","prompt":"<task-notification>\\n<task-id>b2b3pwaaq</task-id>\\n<status>completed</status>\\n</task-notification>"},"type":"attachment"}`)
	ev, err := protocol.ParseLine(raw)
	if err != nil {
		t.Fatal(err)
	}
	c.Apply(ev)
	if c.Background() != 0 {
		t.Fatal("a completion delivered as an attachment (queued_command) ends the task")
	}
}

func TestBackgroundLaunchTextInsideOutputIsNotATask(t *testing.T) {
	c := New()
	tool := func(id, name, input, result string) {
		c.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant", Content: []byte(`[{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}]`)}})
		b, _ := json.Marshal(result)
		c.Apply(&protocol.Event{Type: "user", Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"tool_result","tool_use_id":"` + id + `","content":` + string(b) + `}]`)}})
	}
	tool("g1", "Bash", `{"command":"grep -o 'Command running' x.jsonl","description":"Check the other qwen 3 transcript"}`,
		"Command running in background with ID: bib4khwb4\n<task-id>b8tgo5n5d</task-id>\nmoved to the background (ID: bya8m3rta)")
	tool("g2", "Bash", `{"command":"cat log"}`, "line one\nCommand running in background with ID: zzzz1234 printed by a script")
	tool("a1", "Agent", `{"prompt":"review","description":"review"}`, "Done. It ran in the background before. agentId: a1b2c3d4e5")
	if c.Background() != 0 {
		t.Fatalf("launch text inside a command's output or a foreground agent's answer is not a background task: %+v", c.BackgroundTasks())
	}
	tool("b1", "Bash", `{"command":"./t.sh","run_in_background":true}`, "Command running in background with ID: real12345. Output is being written to: /tmp/o")
	tool("b2", "Bash", `{"command":"go test ./..."}`, "Command did not complete within its 300s timeout and was moved to the background (ID: real67890). Output is being written to: /tmp/p")
	tool("a2", "Agent", `{"prompt":"x","run_in_background":true}`, "Async agent launched. agentId: agent12345")
	if c.Background() != 3 {
		t.Fatalf("real launches still count: %+v", c.BackgroundTasks())
	}
}

func TestTaskEndsWhenItsOutputReportsDone(t *testing.T) {
	c := New()
	tool := func(id, name, input, result string) {
		c.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant", Content: []byte(`[{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}]`)}})
		b, _ := json.Marshal(result)
		c.Apply(&protocol.Event{Type: "user", Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"tool_result","tool_use_id":"` + id + `","content":` + string(b) + `}]`)}})
	}
	tool("b1", "Bash", `{"command":"./t.sh","run_in_background":true}`, "Command running in background with ID: taskabc12. Output is being written to: /tmp/o")
	tool("o1", "TaskOutput", `{"task_id":"taskabc12","block":false}`, "<status>running</status>\npartial output")
	if c.Background() != 1 {
		t.Fatal("a running status keeps the task")
	}
	tool("o2", "TaskOutput", `{"task_id":"taskabc12","block":true}`, "<retrieval_status>success</retrieval_status>\n<status>completed</status>\n<exit_code>0</exit_code>")
	if c.Background() != 0 {
		t.Fatal("reading a finished task's output ends it even without a notification")
	}
}

func TestAwaitsReply(t *testing.T) {
	c := New()
	c.AddUser("go")
	if c.AwaitsReply() {
		t.Fatal("no reply yet")
	}
	it := c.add(&Item{Kind: Assistant, Text: "Done. **Want me to push?**"})
	c.add(&Item{Kind: Notice, Text: "Background task finished"})
	if !c.AwaitsReply() {
		t.Fatal("a last reply ending in a question waits for the user, past later notices")
	}
	it.Streaming = true
	if c.AwaitsReply() {
		t.Fatal("not while the text is still streaming")
	}
	it.Streaming = false
	c.add(&Item{Kind: Assistant, Text: "Pushed."})
	if c.AwaitsReply() {
		t.Fatal("a later plain reply ends it")
	}
	c.add(&Item{Kind: Assistant, Text: "Next?"})
	c.AddUser("yes")
	if c.AwaitsReply() {
		t.Fatal("answered")
	}
}

func TestMonitorIsNotWork(t *testing.T) {
	c := New()
	c.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant", Content: []byte(`[{"type":"tool_use","id":"m1","name":"Monitor","input":{"description":"watch the log"}}]`)}})
	c.Apply(&protocol.Event{Type: "user", Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"tool_result","tool_use_id":"m1","content":"Monitor started (task id: monabc123)"}]`)}})
	if c.Background() != 1 || c.Working() != 0 {
		t.Fatalf("a Monitor is listed but is not work: bg=%d working=%d", c.Background(), c.Working())
	}
}

func TestPeerMessagesDontAnswerTheQuestion(t *testing.T) {
	c := New()
	c.AddUser("build it")
	c.add(&Item{Kind: Assistant, Text: "Built. **Should I ship 1.1?**"})
	c.AddUser("<cross-session-message from=\"x\">lock released</cross-session-message>")
	c.add(&Item{Kind: Assistant, Text: "Noted, thanks."})
	if !c.AwaitsReply() {
		t.Fatal("a peer message and Claude's reply to it don't answer the user's question")
	}
	c.AddUser("yes ship it")
	c.add(&Item{Kind: Assistant, Text: "Shipping."})
	if c.AwaitsReply() {
		t.Fatal("the user's own reply does")
	}
}
