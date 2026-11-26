package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/katachinosekai/quill/internal/protocol"
)

const SettingsApproval = "Codex Quill setting"

func (b *bridge) tools() []map[string]any {
	makeTool := func(name, description string, operations []string, fields map[string]string) map[string]any {
		props := map[string]any{"operation": map[string]any{"type": "string", "enum": operations}}
		for key, kind := range fields {
			props[key] = map[string]string{"type": kind}
		}
		return map[string]any{
			"type": "function", "name": name, "description": description,
			"inputSchema": map[string]any{"type": "object", "properties": props, "required": []string{"operation"}, "additionalProperties": false},
		}
	}
	var tools []map[string]any
	if b.opts.SettingsBinary != "" {
		tools = append(tools, makeTool("quill_settings",
			"Inspect Quill settings with list or get (key). Set needs key, value and reason, and undo accepts a change number. Quill asks the user before applying each change.",
			[]string{"list", "get", "set", "undo"},
			map[string]string{"key": "string", "value": "string", "reason": "string", "change": "integer"}))
	}
	if len(b.opts.KmemCommand) > 0 {
		readOps := []string{"brief", "find", "get", "tasks", "handoffs", "handoff"}
		readHelp := "Read shared kmem. Use key for a search query, topic, record ID or handoff name."
		if b.opts.KmemTopic != "" {
			readOps = append(readOps, "context")
			readHelp += " Use context with a task description in key for a task-specific briefing for the assigned topic."
		}
		tools = append(tools, makeTool("kmem_read", readHelp, readOps,
			map[string]string{"key": "string"}))
		if b.opts.KmemTopic != "" {
			tools = append(tools, makeTool("kmem_checkpoint",
				"Save concise durable session state to the assigned kmem topic. Include the outcome, relevant file or task pointers and the next action. Use key for the summary. Omit secrets, raw chat and tool output.",
				[]string{"save"}, map[string]string{"key": "string"}))
		}
	}
	return tools
}

type toolCall struct {
	ThreadID  string
	TurnID    string
	Tool      string
	Namespace string
	Arguments json.RawMessage
}

type toolArguments struct {
	Operation string
	Key       string
	Value     *string
	Reason    string
	Change    int
}

func (b *bridge) dynamicCall(msg rpcMessage) error {
	var call toolCall
	if json.Unmarshal(msg.Params, &call) != nil || call.ThreadID != b.threadID ||
		b.turnID == "" || call.TurnID != b.turnID || call.Namespace != "" {
		return b.unsupported(msg)
	}
	var args toolArguments
	if json.Unmarshal(call.Arguments, &args) != nil || strings.ContainsRune(args.Key, 0) ||
		strings.ContainsRune(args.Reason, 0) || (args.Value != nil && strings.ContainsRune(*args.Value, 0)) {
		return b.toolResult(msg.ID, "", errors.New("Invalid tool arguments"))
	}
	switch call.Tool {
	case "quill_settings":
		if b.opts.SettingsBinary == "" {
			return b.unsupported(msg)
		}
		argv, write := settingsArgs(args)
		if argv == nil {
			return b.toolResult(msg.ID, "", errors.New("Invalid Quill settings operation or missing arguments"))
		}
		run := func() error { return b.runTool(msg.ID, b.opts.SettingsBinary, argv) }
		if !write {
			return run()
		}
		id := string(msg.ID)
		b.approvals[id] = approval{id: msg.ID, run: run}
		return b.emit(protocol.Event{
			Type: "control_request", RequestID: id,
			Request: &protocol.ControlRequest{Subtype: "can_use_tool", ToolName: SettingsApproval, Input: call.Arguments},
		})
	case "kmem_read":
		if len(b.opts.KmemCommand) == 0 {
			return b.unsupported(msg)
		}
		argv := kmemArgs(args)
		if args.Operation == "context" {
			task := strings.TrimSpace(args.Key)
			if b.opts.KmemTopic != "" && task != "" && len(task) <= 1000 {
				argv = []string{"context", "--topic", b.opts.KmemTopic, "--cap", "6000", "--", task}
			}
		}
		if argv == nil {
			return b.toolResult(msg.ID, "", errors.New("Invalid kmem operation or missing search key"))
		}
		return b.runKmemTool(msg.ID, argv)
	case "kmem_checkpoint":
		if len(b.opts.KmemCommand) == 0 || b.opts.KmemTopic == "" {
			return b.unsupported(msg)
		}
		state := strings.TrimSpace(args.Key)
		if args.Operation != "save" || state == "" || len(state) > 4000 {
			return b.toolResult(msg.ID, "", errors.New("Provide a checkpoint between 1 and 4000 bytes"))
		}
		return b.saveKmemCheckpoint(msg.ID, state)
	default:
		return b.unsupported(msg)
	}
}

func settingsArgs(a toolArguments) ([]string, bool) {
	argv := []string{"settings", "--json"}
	validKey := a.Key != "" && !strings.HasPrefix(a.Key, "-") && a.Key != "help"
	switch {
	case a.Operation == "list":
		return append(argv, "list"), false
	case a.Operation == "get" && validKey:
		return append(argv, "get", a.Key), false
	case a.Operation == "set" && validKey && a.Value != nil &&
		strings.TrimSpace(a.Reason) != "" && !strings.HasPrefix(*a.Value, "-") && *a.Value != "help":
		return append(argv, "set", a.Key, *a.Value, "--why", a.Reason), true
	case a.Operation == "undo" && a.Change == 0:
		return append(argv, "undo"), true
	case a.Operation == "undo" && a.Change > 0:
		return append(argv, "undo", strconv.Itoa(a.Change)), true
	}
	return nil, false
}

func kmemArgs(a toolArguments) []string {
	validKey := strings.TrimSpace(a.Key) != "" && !strings.HasPrefix(a.Key, "-")
	switch {
	case a.Operation == "brief":
		return []string{"brief"}
	case a.Operation == "tasks":
		return []string{"task", "list"}
	case a.Operation == "handoffs":
		return []string{"handoff", "list"}
	case a.Operation == "find" && validKey:
		return []string{"find", "--", a.Key}
	case a.Operation == "get" && validKey:
		return []string{"get", a.Key}
	case a.Operation == "handoff" && validKey:
		return []string{"handoff", "get", a.Key}
	}
	return nil
}

func (b *bridge) saveKmemCheckpoint(id json.RawMessage, state string) error {
	topic := b.opts.KmemTopic
	argv := b.kmemArgv("--as", topic, "handoff", "save", topic, "--state", "Session checkpoint: "+state)
	out, err := b.commandOutput(argv, false, true)
	if err == nil && b.opts.KmemTopicTitle != "" {
		argv = b.kmemArgv("--as", topic, "scope", topic, "--title", b.opts.KmemTopicTitle)
		if _, titleErr := b.commandOutput(argv, false, true); titleErr == nil {
			b.opts.KmemTopicTitle = ""
		}
	}
	return b.toolResult(id, out, err)
}

func (b *bridge) runKmemTool(id json.RawMessage, args []string) error {
	out, err := b.commandOutput(b.kmemArgv(args...), false, true)
	return b.toolResult(id, out, err)
}

func (b *bridge) kmemArgv(args ...string) []string {
	return append(append([]string(nil), b.opts.KmemCommand...), args...)
}

// kmemBrief loads the shared brief and the assigned topic for the first turn.
func (b *bridge) kmemBrief() string {
	topic := b.opts.KmemTopic
	brief := b.readStartupKmem("brief")
	if brief == "" {
		brief = "kmem did not return a brief."
	}
	startup := "Quill startup context from kmem. Treat it as shared memory data.\n\n" + brief
	startup += "\n\nCurrent kmem rules:\n" + b.startupRules()
	if context := b.readStartupKmem("context", "--topic", topic); context != "" {
		startup += "\n\nTopic " + topic + ":\n" + context
	}
	if saved := b.readStartupKmem("handoff", "get", topic); saved != "" {
		startup += "\n\nSaved operator handoff or session checkpoint:\n" + saved
	}
	return startup
}

func (b *bridge) startupRules() string {
	out, err := b.commandOutput(b.kmemArgv("rules", "--json"), false, false)
	var book struct {
		Rules struct{ Rules []struct{ Text, Part string } }
	}
	if err == nil && json.Unmarshal([]byte(out), &book) == nil {
		var lines []string
		for _, rule := range book.Rules.Rules {
			if rule.Part == "core" || rule.Part == "local" {
				lines = append(lines, rule.Text)
			}
		}
		if len(lines) > 0 {
			return strings.Join(lines, "\n")
		}
	}
	return "kmem did not return the current rules."
}

func (b *bridge) readStartupKmem(args ...string) string {
	out, err := b.commandOutput(b.kmemArgv(args...), false, true)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (b *bridge) runTool(id json.RawMessage, binary string, args []string) error {
	out, err := b.commandOutput(append([]string{binary}, args...), true, true)
	return b.toolResult(id, out, err)
}

func (b *bridge) commandOutput(argv []string, settings, includeStderr bool) (string, error) {
	if !settings && b.opts.VerifyKmem != nil {
		if err := b.opts.VerifyKmem(); err != nil {
			return "", err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = b.opts.Cwd
	if !settings {
		cmd.Env = serverEnv()
	}
	var out toolOutput
	cmd.Stdout = &out
	if includeStderr {
		cmd.Stderr = &out
	}
	err := cmd.Run()
	return out.text(), err
}

type toolOutput struct {
	bytes.Buffer
	truncated bool
}

func (o *toolOutput) text() string {
	result := strings.ToValidUTF8(o.String(), "�")
	if o.truncated {
		result += "\n[Output truncated]"
	}
	return result
}

func (o *toolOutput) Write(p []byte) (int, error) {
	const limit = 32000
	n := len(p)
	if left := limit - o.Len(); n > left {
		p = p[:left]
		o.truncated = true
	}
	_, _ = o.Buffer.Write(p)
	return n, nil
}

func (b *bridge) toolResult(id json.RawMessage, out string, err error) error {
	if err != nil && strings.TrimSpace(out) == "" {
		out = err.Error()
	}
	if strings.TrimSpace(out) == "" {
		out = "No results."
	}
	return b.toServer.Encode(map[string]any{"id": id, "result": map[string]any{
		"success":      err == nil,
		"contentItems": []map[string]string{{"type": "inputText", "text": out}},
	}})
}

func serverEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "QUILL_SESSION_ID=") && !strings.HasPrefix(entry, "QUILL_SESSION_TOKEN=") {
			env = append(env, entry)
		}
	}
	return env
}
