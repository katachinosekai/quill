package chat

import (
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/protocol"
)

func applyLine(t *testing.T, c *Conversation, line string) {
	t.Helper()
	ev, err := protocol.ParseLine([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	c.Apply(ev)
}

func agentByID(c *Conversation, id string) (AgentUse, bool) {
	for _, a := range c.Agents() {
		if a.ID == id {
			return a, true
		}
	}
	return AgentUse{}, false
}

// The event shapes are the CLI's (2.1.x stream-json); ids and text are
// placeholders.
func TestAgentTokensFromTaskEvents(t *testing.T) {
	c := New()
	// A background agent: the call, its launch result, task_started, progress.
	applyLine(t, c, `{"type":"assistant","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"toolu_A","name":"Agent","input":{"description":"part A","subagent_type":"Explore","prompt":"x","run_in_background":true}}]},"parent_tool_use_id":null}`)
	if a, ok := agentByID(c, "toolu_A"); !ok || a.Name != "part A" || a.Type != "Explore" || !a.Running() {
		t.Fatalf("the live Agent call is listed: %+v", c.Agents())
	}
	rev := c.AgentsRev()
	applyLine(t, c, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_A","type":"tool_result","content":[{"type":"text","text":"Async agent launched successfully.\nagentId: agentA1 (internal ID)"}]}]},"parent_tool_use_id":null,"tool_use_result":{"isAsync":true,"status":"async_launched","agentId":"agentA1"}}`)
	if a, ok := agentByID(c, "agentA1"); !ok || !a.Running() || a.ToolID != "toolu_A" {
		t.Fatalf("the launch result names it and it keeps running: %+v", c.Agents())
	}
	applyLine(t, c, `{"type":"system","subtype":"task_started","task_id":"agentA1","tool_use_id":"toolu_A","description":"part A","subagent_type":"Explore","task_type":"local_agent","is_backgrounded":true}`)
	applyLine(t, c, `{"type":"system","subtype":"task_progress","task_id":"agentA1","tool_use_id":"toolu_A","description":"part A","usage":{"total_tokens":12000,"tool_uses":3,"duration_ms":5000},"last_tool_name":"Read"}`)
	a, _ := agentByID(c, "agentA1")
	if a.Tokens != 12000 || a.ToolUses != 3 || a.LastTool != "Read" || len(c.Agents()) != 1 || c.AgentsRev() == rev {
		t.Fatalf("progress counts tokens: %+v", c.Agents())
	}
	applyLine(t, c, `{"type":"system","subtype":"task_progress","task_id":"agentA1","usage":{"total_tokens":41000,"tool_uses":9}}`)
	if a, _ := agentByID(c, "agentA1"); a.Tokens != 41000 {
		t.Fatalf("tokens grow: %+v", a)
	}
	// A shell task is no agent.
	applyLine(t, c, `{"type":"system","subtype":"task_started","task_id":"b1","tool_use_id":"toolu_B","description":"npm test","task_type":"local_bash"}`)
	if _, ok := agentByID(c, "b1"); ok {
		t.Fatal("background shells are not subagents")
	}
	if c.RunningAgents() != 1 {
		t.Fatalf("running: %d", c.RunningAgents())
	}
	applyLine(t, c, `{"type":"system","subtype":"task_notification","task_id":"agentA1","tool_use_id":"toolu_A","status":"completed","summary":"done","usage":{"total_tokens":52000,"tool_uses":11,"duration_ms":60000}}`)
	a, _ = agentByID(c, "agentA1")
	if a.Running() || a.Status != "completed" || a.Tokens != 52000 || c.RunningAgents() != 0 {
		t.Fatalf("the notification ends it with its total: %+v", a)
	}
	// Messaged again, it works again.
	applyLine(t, c, `{"type":"system","subtype":"task_progress","task_id":"agentA1","usage":{"total_tokens":53000}}`)
	if a, _ := agentByID(c, "agentA1"); !a.Running() || a.Tokens != 53000 {
		t.Fatalf("resumed: %+v", a)
	}
}

func TestAgentForegroundAndNotificationText(t *testing.T) {
	c := New()
	// A foreground agent known only from task_started ends with its result.
	applyLine(t, c, `{"type":"assistant","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"toolu_F","name":"Agent","input":{"description":"look around","prompt":"x"}}]},"parent_tool_use_id":null}`)
	applyLine(t, c, `{"type":"system","subtype":"task_started","task_id":"agentF","tool_use_id":"toolu_F","description":"look around","subagent_type":"general-purpose","task_type":"local_agent"}`)
	applyLine(t, c, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_F","type":"tool_result","content":[{"type":"text","text":"Found it.\nagentId: agentF\n<usage>subagent_tokens: 4512\ntool_uses: 2\nduration_ms: 6264</usage>"}]}]},"parent_tool_use_id":null,"tool_use_result":{"status":"completed","totalTokens":4512}}`)
	a, ok := agentByID(c, "agentF")
	if !ok || a.Running() || a.Tokens != 4512 || a.Type != "general-purpose" {
		t.Fatalf("foreground result: %+v", c.Agents())
	}
	// A background agent first seen in task_started ends by the
	// <task-notification> text with its usage.
	applyLine(t, c, `{"type":"system","subtype":"task_started","task_id":"agentG","tool_use_id":"toolu_G","description":"build","subagent_type":"general-purpose","task_type":"local_agent","is_backgrounded":true}`)
	applyLine(t, c, `{"type":"user","message":{"role":"user","content":"<task-notification>\n<task-id>agentG</task-id>\n<tool-use-id>toolu_G</tool-use-id>\n<output-file>/tmp/x/agentG.output</output-file>\n<status>completed</status>\n<summary>Agent \"build\" finished</summary>\n<usage><subagent_tokens>34822</subagent_tokens><tool_uses>5</tool_uses><duration_ms>13806</duration_ms></usage>\n</task-notification>"},"parent_tool_use_id":null}`)
	a, _ = agentByID(c, "agentG")
	if a.Running() || a.Tokens != 34822 || a.Log != "/tmp/x/agentG.output" {
		t.Fatalf("notification text: %+v", a)
	}
}

func TestAgentsReplayAndStop(t *testing.T) {
	c := New()
	// A transcript's Agent call (timestamped) is history, not a live agent.
	applyLine(t, c, `{"type":"assistant","timestamp":"2026-11-20T12:00:00Z","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"toolu_old","name":"Agent","input":{"description":"old"}}]}}`)
	if len(c.Agents()) != 0 {
		t.Fatal("replayed calls are not listed")
	}
	applyLine(t, c, `{"type":"system","subtype":"task_started","task_id":"agentS","tool_use_id":"toolu_S","description":"stop me","subagent_type":"Explore","task_type":"local_agent"}`)
	applyLine(t, c, `{"type":"system","subtype":"task_started","task_id":"agentT","tool_use_id":"toolu_T","description":"keep","subagent_type":"Explore","task_type":"local_agent"}`)
	applyLine(t, c, `{"type":"assistant","message":{"id":"m2","role":"assistant","content":[{"type":"tool_use","id":"toolu_stop","name":"TaskStop","input":{"task_id":"agentS"}}]}}`)
	applyLine(t, c, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_stop","type":"tool_result","content":"stopped"}]}}`)
	if a, _ := agentByID(c, "agentS"); a.Running() || a.Status != "stopped" {
		t.Fatalf("TaskStop ends it: %+v", a)
	}
	applyLine(t, c, `{"type":"system","subtype":"task_updated","task_id":"agentT","patch":{"status":"killed"}}`)
	if c.RunningAgents() != 0 {
		t.Fatal("task_updated ends it")
	}
	applyLine(t, c, `{"type":"system","subtype":"task_started","task_id":"agentU","description":"u","task_type":"local_agent"}`)
	c.ClearBackground()
	if c.RunningAgents() != 0 {
		t.Fatal("a restarted process ends its agents")
	}
	// Finished agents drop out after an hour.
	defer func(f func() time.Time) { agentNow = f }(agentNow)
	agentNow = func() time.Time { return time.Now().Add(2 * time.Hour) }
	applyLine(t, c, `{"type":"system","subtype":"task_started","task_id":"agentV","description":"v","task_type":"local_agent"}`)
	if len(c.Agents()) != 1 {
		t.Fatalf("old finished agents pruned: %+v", c.Agents())
	}
}

func TestAgentCallDescriptionAndModel(t *testing.T) {
	c := New()
	applyLine(t, c, `{"type":"assistant","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"toolu_D","name":"Agent","input":{"description":"Restore plan card overlay (both apps)","subagent_type":"general-purpose","model":"opus","prompt":"x"}}]},"parent_tool_use_id":null}`)
	g := c.Agents()
	if len(g) != 1 || g[0].Name != "Restore plan card overlay (both apps)" || g[0].Type != "general-purpose" || g[0].Model != "opus" {
		t.Fatalf("agent from the call: %+v", g)
	}
}
