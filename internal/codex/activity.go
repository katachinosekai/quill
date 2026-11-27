package codex

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/katachinosekai/quill/internal/protocol"
)

type activityContent struct {
	Type string
	Text string
}

type activityItem struct {
	Type, ID, Text, Command, Cwd, Status string
	Tool, Server, Namespace, Prompt      string
	SenderThreadID, AgentThreadID        string
	AgentPath, Kind                      string
	ReceiverThreadIDs                    []string
	AgentsStates                         map[string]struct{ Status string }
	Query, AggregatedOutput              string
	Changes, Arguments                   json.RawMessage
	ExitCode                             *int
	Success                              *bool
	CommandActions                       []struct{ Type, Path string }
	ContentItems                         []activityContent
	Result                               *struct {
		Content []activityContent
		IsError bool
	}
	Error *struct{ Message string }
}

func (b *bridge) activity(it activityItem, completed bool) error {
	if it.ID == "" {
		return nil
	}
	var name, output string
	var input any
	failed := it.Status == "failed" || it.Status == "declined"
	switch it.Type {
	case "commandExecution":
		name = it.commandLabel()
		params := map[string]any{"command": it.Command, "cwd": it.Cwd, "status": it.Status}
		if it.ExitCode != nil {
			params["exitCode"] = *it.ExitCode
			failed = failed || *it.ExitCode != 0
		}
		input, output = params, it.AggregatedOutput
	case "fileChange":
		name = "File changes"
		changes := it.Changes
		if !hasFileChanges(changes) {
			changes = b.fileChanges[it.ID]
		}
		input = map[string]any{"changes": changes, "status": it.Status}
		if completed {
			delete(b.fileChanges, it.ID)
		}
	case "mcpToolCall", "dynamicToolCall":
		if it.Tool == "" {
			return nil
		}
		name, input, output = it.toolDetails()
		failed = failed || it.Error != nil || it.Success != nil && !*it.Success || it.Result != nil && it.Result.IsError
	case "collabAgentToolCall":
		if it.SenderThreadID != "" && it.SenderThreadID != b.threadID {
			return nil
		}
		if err := b.trackWorkers(it, completed); err != nil {
			return err
		}
		name = "Agent: " + it.Tool
		input = map[string]string{"action": it.Tool, "status": it.Status}
	case "webSearch":
		name = "Web search"
		input = map[string]string{"query": it.Query}
	default:
		return nil
	}
	params, err := json.Marshal(input)
	if err != nil {
		return err
	}
	if err := b.message("assistant", it.ID, []protocol.Block{{Type: "tool_use", ID: it.ID, Name: name, Input: params}}); err != nil {
		return err
	}
	if completed {
		delete(b.activeItems, it.ID)
		if it.Status == "declined" && strings.TrimSpace(output) == "" {
			output = "Declined"
		}
		if err := b.activityResult(it.ID, output, failed); err != nil {
			return err
		}
	} else {
		if b.activeItems == nil {
			b.activeItems = make(map[string]bool)
		}
		b.activeItems[it.ID] = it.Type == "collabAgentToolCall" && it.Tool == "wait"
	}
	return b.syncAgentWait()
}

// subAgentActivity is a lifecycle item on the parent's native thread. App
// Server can announce the same item at both item/started and item/completed.
func (b *bridge) subAgentActivity(it activityItem) error {
	if it.ID == "" || it.AgentThreadID == "" || it.AgentThreadID == b.threadID {
		return nil
	}
	if _, seen := b.workerActivityIDs[it.ID]; seen {
		return nil
	}
	wasActive := len(b.workers) != 0
	var err error
	switch it.Kind {
	case "started":
		err = b.startWorker(it.AgentThreadID, it.AgentPath)
	case "completed":
		err = b.endWorker(it.AgentThreadID, "completed")
	case "interrupted":
		err = b.endWorker(it.AgentThreadID, "stopped")
	default:
		return nil
	}
	if err != nil {
		return err
	}
	if b.workerActivityIDs == nil {
		b.workerActivityIDs = make(map[string]struct{})
	}
	b.workerActivityIDs[it.ID] = struct{}{}
	return b.emitWorkerActivity(wasActive)
}

// Worker state survives the end of the parent turn.
func (b *bridge) trackWorkers(it activityItem, completed bool) error {
	wasActive := len(b.workers) != 0
	action := strings.ReplaceAll(strings.ToLower(it.Tool), "_", "")
	if completed && it.Status == "completed" {
		for _, id := range it.ReceiverThreadIDs {
			var err error
			switch action {
			case "spawnagent", "resumeagent", "followuptask":
				err = b.startWorker(id, it.Prompt)
			case "closeagent":
				err = b.endWorker(id, "stopped")
			}
			if err != nil {
				return err
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(it.AgentsStates)) {
		var err error
		switch it.AgentsStates[id].Status {
		case "pendingInit", "running":
			err = b.startWorker(id, it.Prompt)
		case "completed":
			err = b.endWorker(id, "completed")
		case "interrupted", "shutdown", "notFound":
			err = b.endWorker(id, "stopped")
		case "errored":
			err = b.endWorker(id, "failed")
		}
		if err != nil {
			return err
		}
	}
	return b.emitWorkerActivity(wasActive)
}

func (b *bridge) startWorker(id, prompt string) error {
	if id == "" || id == b.threadID || b.workers[id] {
		return nil
	}
	if b.workers == nil {
		b.workers = make(map[string]bool)
	}
	b.workers[id] = true
	name, _, _ := strings.Cut(strings.TrimSpace(prompt), "\n")
	if name == "" {
		name = "Codex subagent " + id[:min(len(id), 8)]
	} else if chars := []rune(name); len(chars) > 80 {
		name = string(chars[:79]) + "…"
	}
	// Progress creates a new agent or reopens the same agent on resume.
	return b.toQuill.Encode(struct {
		protocol.Event
		TaskType    string `json:"task_type"`
		Description string `json:"description"`
	}{
		Event:       protocol.Event{Type: "system", Subtype: "task_progress", TaskID: id},
		TaskType:    "local_agent",
		Description: name,
	})
}

func (b *bridge) endWorker(id, status string) error {
	if !b.workers[id] {
		return nil
	}
	delete(b.workers, id)
	return b.emit(protocol.Event{Type: "system", Subtype: "task_notification", TaskID: id, Status: status})
}

func (b *bridge) emitWorkerActivity(wasActive bool) error {
	active := len(b.workers) != 0
	if wasActive == active {
		return nil
	}
	subtype := protocol.CodexWorkersEnded
	if active {
		subtype = protocol.CodexWorkersStarted
	}
	return b.emit(protocol.Event{Type: "system", Subtype: subtype})
}

func (b *bridge) syncAgentWait() error {
	waiting := len(b.activeItems) > 0
	for _, isWait := range b.activeItems {
		waiting = waiting && isWait
	}
	if waiting == b.waitingForAgents {
		return nil
	}
	b.waitingForAgents = waiting
	subtype := protocol.CodexAgentWaitEnded
	if waiting {
		subtype = protocol.CodexAgentWaitStarted
	}
	if err := b.emit(protocol.Event{Type: "system", Subtype: subtype}); err != nil {
		return err
	}
	if waiting {
		return b.deliverInput()
	}
	return nil
}

func (it activityItem) commandLabel() string {
	if len(it.CommandActions) == 1 {
		action := it.CommandActions[0]
		if action.Type == "read" && filepath.Base(action.Path) == "SKILL.md" {
			skill := filepath.Base(filepath.Dir(action.Path))
			if skill != "." && skill != string(filepath.Separator) {
				return "Skill: " + skill
			}
			return "Skill"
		}
	}
	return "Command"
}

func (it activityItem) toolDetails() (name string, input any, output string) {
	name = "Tool: " + it.Tool
	if it.Server != "" {
		name = "Tool: " + it.Server + "/" + it.Tool
	} else if it.Namespace != "" {
		name = "Tool: " + it.Namespace + "/" + it.Tool
	}
	if it.Type == "dynamicToolCall" && it.Namespace == "" {
		switch it.Tool {
		case "kmem_read", "kmem_checkpoint", "quill_settings":
			var args struct{ Operation string }
			_ = json.Unmarshal(it.Arguments, &args)
			return name, map[string]string{"operation": args.Operation, "status": it.Status}, ""
		}
	}
	input = map[string]any{"arguments": it.Arguments, "status": it.Status}
	if it.Error != nil {
		return name, input, it.Error.Message
	}
	content := it.ContentItems
	if it.Result != nil {
		content = it.Result.Content
	}
	var out toolOutput
	for _, item := range content {
		if item.Type != "text" && item.Type != "inputText" {
			continue
		}
		if out.Len() > 0 {
			_, _ = out.Write([]byte("\n"))
		}
		_, _ = out.Write([]byte(item.Text))
	}
	return name, input, out.text()
}

func hasFileChanges(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "", "null", "[]":
		return false
	default:
		return true
	}
}

func (b *bridge) activityResult(id, value string, failed bool) error {
	var out toolOutput
	_, _ = out.Write([]byte(value))
	content, err := json.Marshal(out.text())
	if err != nil {
		return err
	}
	return b.message("user", id+"-result", []protocol.Block{{Type: "tool_result", ToolUseID: id, Content: content, IsError: failed}})
}
