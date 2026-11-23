package chat

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/katachinosekai/quill/internal/protocol"
)

// AgentUse is one subagent of this session with its tokens so far (kmem task
// t4cb4ee, topic tabs §3.4). The CLI streams them as system events:
//
//   - task_started: task_id, tool_use_id, description, subagent_type,
//     task_type ("local_agent", …; "local_bash" and monitors are not agents)
//   - task_progress: the same plus usage {total_tokens, tool_uses,
//     duration_ms} and last_tool_name, while the agent works
//   - task_notification: task_id, status, usage, when it stops
//   - task_updated: patch {status, …}
//
// A foreground agent's tool_result ends it too (its text carries
// "<usage>subagent_tokens: N", tool_use_result totalTokens), and a
// background agent's "<task-notification>" text carries
// "<usage><subagent_tokens>N</subagent_tokens>…".
type AgentUse struct {
	ID         string // task (agent) id; the Agent call's tool_use_id until the CLI names it
	ToolID     string // the Agent call's tool_use_id
	Name       string // description
	Type       string // subagent_type ("Explore", "general-purpose", …)
	Tokens     int
	ToolUses   int
	LastTool   string
	Status     string // "running", or how it ended ("completed", "failed", "stopped", …)
	Background bool
	Log        string // output file, when the CLI names one
	Started    time.Time
	Done       time.Time
	seq        int
}

func (a AgentUse) Running() bool { return a.Done.IsZero() }

// agentNow is the clock for agent times (tests set it).
var agentNow = time.Now

// agentKeep: how long a finished agent stays in the list.
const agentKeep = time.Hour

var (
	agentTaskTypes = map[string]bool{"local_agent": true, "remote_agent": true, "in_process_teammate": true, "local_workflow": true}
	agentTokensRe  = regexp.MustCompile(`(?:subagent_tokens|total_tokens)>?:?\s*(\d+)`)
	notifyToolID   = regexp.MustCompile(`<tool-use-id>\s*([^<\s]+)\s*</tool-use-id>`)
	notifyOutput   = regexp.MustCompile(`<output-file>\s*([^<\s]+)\s*</output-file>`)
)

// Agents lists this session's subagents, oldest first: the running ones and
// those that finished within the last hour.
func (c *Conversation) Agents() []AgentUse {
	out := make([]AgentUse, 0, len(c.agents))
	for _, a := range c.agents {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

// AgentsRev changes whenever an agent is added or changes, so a view can
// skip work while nothing moved.
func (c *Conversation) AgentsRev() int { return c.agentsRev }

// RunningAgents counts the subagents still working.
func (c *Conversation) RunningAgents() int {
	n := 0
	for _, a := range c.agents {
		if a.Running() {
			n++
		}
	}
	return n
}

func (c *Conversation) agentFor(id, toolID string) *AgentUse {
	if a := c.agents[id]; id != "" && a != nil {
		return a
	}
	if k, ok := c.agentByTool[toolID]; ok && toolID != "" {
		a := c.agents[k]
		if id != "" && k != id && a != nil {
			delete(c.agents, k)
			a.ID = id
			c.agents[id] = a
			c.agentByTool[toolID] = id
		}
		return a
	}
	return nil
}

func (c *Conversation) newAgent(id, toolID string) *AgentUse {
	if c.agents == nil {
		c.agents = map[string]*AgentUse{}
		c.agentByTool = map[string]string{}
	}
	now := agentNow()
	for k, a := range c.agents {
		if !a.Running() && now.Sub(a.Done) > agentKeep {
			delete(c.agents, k)
			delete(c.agentByTool, a.ToolID)
		}
	}
	c.agentSeq++
	if id == "" {
		id = toolID
	}
	a := &AgentUse{ID: id, ToolID: toolID, Status: "running", Started: now, seq: c.agentSeq}
	c.agents[id] = a
	if toolID != "" {
		c.agentByTool[toolID] = id
	}
	return a
}

func (c *Conversation) endAgent(a *AgentUse, status string) {
	if a == nil || !a.Running() {
		return
	}
	if status == "" {
		status = "completed"
	}
	a.Status = status
	a.Done = agentNow()
	c.agentsRev++
}

func (a *AgentUse) setTokens(n int) {
	if n > a.Tokens {
		a.Tokens = n
	}
}

// noteAgentCall records an Agent call the live session makes; the CLI's
// task events then name and count it. Replayed transcript calls (they have
// a timestamp) are not recorded: their agents are long gone.
func (c *Conversation) noteAgentCall(ev *protocol.Event, b protocol.Block) {
	if ev.Timestamp != "" || b.Name != "Agent" && b.Name != "Task" {
		return
	}
	if c.agentFor("", b.ID) != nil {
		return
	}
	var in struct {
		Description string `json:"description"`
		Type        string `json:"subagent_type"`
		Background  bool   `json:"run_in_background"`
	}
	json.Unmarshal(b.Input, &in)
	a := c.newAgent("", b.ID)
	a.Name, a.Type, a.Background = in.Description, in.Type, in.Background
	c.agentsRev++
}

func (c *Conversation) applyTaskEvent(ev *protocol.Event) {
	var e struct {
		TaskID      string `json:"task_id"`
		ToolUseID   string `json:"tool_use_id"`
		Description string `json:"description"`
		Type        string `json:"subagent_type"`
		TaskType    string `json:"task_type"`
		Background  *bool  `json:"is_backgrounded"`
		Status      string `json:"status"`
		OutputFile  string `json:"output_file"`
		LastTool    string `json:"last_tool_name"`
		Usage       *struct {
			Tokens   int `json:"total_tokens"`
			ToolUses int `json:"tool_uses"`
		} `json:"usage"`
		Patch struct {
			Status string `json:"status"`
		} `json:"patch"`
	}
	if json.Unmarshal(ev.Raw, &e) != nil || e.TaskID == "" {
		return
	}
	a := c.agentFor(e.TaskID, e.ToolUseID)
	if a == nil {
		agentish := e.Type != "" || agentTaskTypes[e.TaskType]
		if !agentish || ev.Subtype == "task_notification" || ev.Subtype == "task_updated" {
			return
		}
		a = c.newAgent(e.TaskID, e.ToolUseID)
	}
	if e.Description != "" {
		a.Name = e.Description
	}
	if e.Type != "" {
		a.Type = e.Type
	}
	if e.Background != nil {
		a.Background = *e.Background
	}
	if e.OutputFile != "" {
		a.Log = e.OutputFile
	}
	if e.LastTool != "" {
		a.LastTool = e.LastTool
	}
	if e.Usage != nil {
		a.setTokens(e.Usage.Tokens)
		if e.Usage.ToolUses > a.ToolUses {
			a.ToolUses = e.Usage.ToolUses
		}
	}
	c.agentsRev++
	switch ev.Subtype {
	case "task_progress":
		if !a.Running() { // a finished agent the user messaged again
			a.Done, a.Status = time.Time{}, "running"
		}
	case "task_notification":
		if taskDone[e.Status] {
			c.endAgent(a, e.Status)
		}
	case "task_updated":
		if taskDone[e.Patch.Status] {
			c.endAgent(a, e.Patch.Status)
		}
	}
}

// agentResult: the Agent call's tool_result. A foreground agent ends with
// it; a background launch result names the agent id.
func (c *Conversation) agentResult(ev *protocol.Event, it *Item, single bool) {
	a := c.agentFor("", it.ToolID)
	if a == nil {
		return
	}
	var r struct {
		Status  string `json:"status"`
		AgentID string `json:"agentId"`
		Tokens  int    `json:"totalTokens"`
	}
	if single {
		raw := ev.ToolUseResult
		if len(raw) == 0 {
			raw = ev.ToolUseResultT
		}
		json.Unmarshal(raw, &r)
	}
	if r.AgentID != "" && r.AgentID != a.ID {
		a = c.agentFor(r.AgentID, it.ToolID)
	}
	if r.Status == "async_launched" || a.Background && !it.ResultError && r.Status == "" {
		c.agentsRev++
		return
	}
	a.setTokens(r.Tokens)
	if m := agentTokensRe.FindStringSubmatch(it.Result); m != nil {
		n, _ := strconv.Atoi(m[1])
		a.setTokens(n)
	}
	st := "completed"
	if it.ResultError {
		st = "failed"
	}
	c.endAgent(a, st)
}

// agentNotification: one "<task-notification>" body.
func (c *Conversation) agentNotification(part, id, status string) {
	toolID := ""
	if m := notifyToolID.FindStringSubmatch(part); m != nil {
		toolID = m[1]
	}
	a := c.agentFor(id, toolID)
	if a == nil {
		return
	}
	if m := agentTokensRe.FindStringSubmatch(part); m != nil {
		n, _ := strconv.Atoi(m[1])
		a.setTokens(n)
	}
	if m := notifyOutput.FindStringSubmatch(part); m != nil && a.Log == "" {
		a.Log = m[1]
	}
	c.agentsRev++
	if taskDone[status] {
		c.endAgent(a, status)
	}
}

// stopAgents: the session's process ended or restarted; its agents with it.
func (c *Conversation) stopAgents() {
	for _, a := range c.agents {
		c.endAgent(a, "stopped")
	}
}
