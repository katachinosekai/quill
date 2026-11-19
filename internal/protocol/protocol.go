package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type Message struct {
	ID      string          `json:"id,omitempty"`
	Role    string          `json:"role"`
	Model   string          `json:"model,omitempty"`
	Content json.RawMessage `json:"content"`
	Usage   *Usage          `json:"usage,omitempty"`
}

type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func (u Usage) Context() int {
	return u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
}

func (m Message) Blocks() []Block {
	return ParseContent(m.Content)
}

func ParseContent(raw json.RawMessage) []Block {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if raw[0] == '"' {
		if json.Unmarshal(raw, &s) == nil {
			return []Block{{Type: "text", Text: s}}
		}
		return nil
	}
	var bs []Block
	if json.Unmarshal(raw, &bs) != nil {
		return nil
	}
	return bs
}

func ContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var parts []string
	for _, b := range ParseContent(raw) {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "image":
			parts = append(parts, "[image]")
		case "tool_reference":
			parts = append(parts, "[tool reference]")
		}
	}
	return strings.Join(parts, "\n")
}

type StreamDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
}

type StreamEvent struct {
	Type         string       `json:"type"`
	Index        int          `json:"index"`
	ContentBlock *Block       `json:"content_block,omitempty"`
	Delta        *StreamDelta `json:"delta,omitempty"`
	Message      *Message     `json:"message,omitempty"`
}

type ModelUsage struct {
	ContextWindow int     `json:"contextWindow"`
	CostUSD       float64 `json:"costUSD"`
}

type ControlRequest struct {
	Subtype               string            `json:"subtype"`
	ToolName              string            `json:"tool_name,omitempty"`
	DisplayName           string            `json:"display_name,omitempty"`
	Input                 json.RawMessage   `json:"input,omitempty"`
	Description           string            `json:"description,omitempty"`
	DecisionReason        string            `json:"decision_reason,omitempty"`
	BlockedPath           string            `json:"blocked_path,omitempty"`
	ToolUseID             string            `json:"tool_use_id,omitempty"`
	PermissionSuggestions []json.RawMessage `json:"permission_suggestions,omitempty"`
}

type ControlResponseBody struct {
	Subtype   string          `json:"subtype"`
	RequestID string          `json:"request_id"`
	Response  json.RawMessage `json:"response,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type Event struct {
	Type            string                `json:"type"`
	Subtype         string                `json:"subtype,omitempty"`
	SessionID       string                `json:"session_id,omitempty"`
	UUID            string                `json:"uuid,omitempty"`
	ParentToolUseID *string               `json:"parent_tool_use_id,omitempty"`
	MessageRaw      json.RawMessage       `json:"message,omitempty"`
	Message         *Message              `json:"-"`
	MessageText     string                `json:"-"`
	Event           *StreamEvent          `json:"event,omitempty"`
	APIMessageID    string                `json:"api_message_id,omitempty"`
	ToolUseResult   json.RawMessage       `json:"tool_use_result,omitempty"`
	RequestID       string                `json:"request_id,omitempty"`
	Request         *ControlRequest       `json:"request,omitempty"`
	Response        *ControlResponseBody  `json:"response,omitempty"`
	Cwd             string                `json:"cwd,omitempty"`
	Model           string                `json:"model,omitempty"`
	PermissionMode  string                `json:"permissionMode,omitempty"`
	Status          string                `json:"status,omitempty"`
	State           string                `json:"state,omitempty"`
	IsError         bool                  `json:"is_error,omitempty"`
	Result          string                `json:"result,omitempty"`
	Suggestion      string                `json:"suggestion,omitempty"`
	RateLimit       *RateLimit            `json:"rate_limit_info,omitempty"`
	TerminalReason  string                `json:"terminal_reason,omitempty"`
	TotalCostUSD    float64               `json:"total_cost_usd,omitempty"`
	NumTurns        int                   `json:"num_turns,omitempty"`
	DurationMS      int                   `json:"duration_ms,omitempty"`
	Usage           *Usage                `json:"usage,omitempty"`
	ModelUsage      map[string]ModelUsage `json:"modelUsage,omitempty"`
	Errors          []string              `json:"errors,omitempty"`
	ToolName        string                `json:"tool_name,omitempty"`
	ToolUseID       string                `json:"tool_use_id,omitempty"`
	EstimatedTokens int                   `json:"estimated_tokens,omitempty"`
	IsSidechain     bool                  `json:"isSidechain,omitempty"`
	IsMeta          bool                  `json:"isMeta,omitempty"`
	Timestamp       string                `json:"timestamp,omitempty"`
	ToolUseResultT  json.RawMessage       `json:"toolUseResult,omitempty"`
	AiTitle         string                `json:"aiTitle,omitempty"`
	CustomTitle     string                `json:"customTitle,omitempty"`
	Summary         string                `json:"summary,omitempty"`
	Raw             json.RawMessage       `json:"-"`
}

func ParseLine(line []byte) (*Event, error) {
	var ev Event
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, err
	}
	if ev.Type == "" {
		return nil, fmt.Errorf("protocol: line without type")
	}
	ev.Raw = append(json.RawMessage(nil), line...)
	if len(ev.MessageRaw) > 0 {
		switch ev.MessageRaw[0] {
		case '{':
			var m Message
			if json.Unmarshal(ev.MessageRaw, &m) == nil {
				ev.Message = &m
			}
		case '"':
			json.Unmarshal(ev.MessageRaw, &ev.MessageText)
		}
	}
	return &ev, nil
}

func (e *Event) IsSubagent() bool {
	return e.ParentToolUseID != nil && *e.ParentToolUseID != ""
}

func (e *Event) LastContext() (used, window int) {
	if e.Usage != nil {
		var it struct {
			Usage struct {
				Iterations []Usage `json:"iterations"`
			} `json:"usage"`
		}
		if json.Unmarshal(e.Raw, &it) == nil && len(it.Usage.Iterations) > 0 {
			used = it.Usage.Iterations[len(it.Usage.Iterations)-1].Context()
		}
	}
	for _, mu := range e.ModelUsage {
		if mu.ContextWindow > window {
			window = mu.ContextWindow
		}
	}
	return used, window
}

func line(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}

func UserMessage(text string) []byte {
	return line(map[string]any{
		"type":               "user",
		"message":            map[string]any{"role": "user", "content": text},
		"parent_tool_use_id": nil,
		"session_id":         "",
	})
}

func ControlRequestLine(id string, req map[string]any) []byte {
	return line(map[string]any{"type": "control_request", "request_id": id, "request": req})
}

func Initialize(id string) []byte {
	return ControlRequestLine(id, map[string]any{"subtype": "initialize"})
}

func Interrupt(id string) []byte {
	return ControlRequestLine(id, map[string]any{"subtype": "interrupt"})
}

func SetPermissionMode(id, mode string) []byte {
	return ControlRequestLine(id, map[string]any{"subtype": "set_permission_mode", "mode": mode})
}

func SetModel(id, model string) []byte {
	var m any = model
	if model == "" || model == "default" {
		m = nil
	}
	return ControlRequestLine(id, map[string]any{"subtype": "set_model", "model": m})
}

func RemoteControl(id string, enabled bool) []byte {
	return ControlRequestLine(id, map[string]any{"subtype": "remote_control", "enabled": enabled})
}

type Decision int

const (
	Allow Decision = iota
	AllowAlways
	Deny
)

func PermissionResponse(requestID string, d Decision, req *ControlRequest, denyMessage string) []byte {
	var body map[string]any
	switch d {
	case Allow, AllowAlways:
		input := json.RawMessage(`{}`)
		if req != nil && len(req.Input) > 0 {
			input = req.Input
		}
		body = map[string]any{"behavior": "allow", "updatedInput": input}
		if d == AllowAlways && req != nil {
			body["updatedPermissions"] = alwaysRules(req)
		}
	default:
		if denyMessage == "" {
			denyMessage = "The user denied this tool use."
		}
		body = map[string]any{"behavior": "deny", "message": denyMessage}
	}
	return ControlResponseLine(requestID, body)
}

func alwaysRules(req *ControlRequest) []any {
	var out []any
	for _, s := range req.PermissionSuggestions {
		var m map[string]any
		if json.Unmarshal(s, &m) == nil {
			m["destination"] = "session"
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		out = append(out, map[string]any{
			"type":        "addRules",
			"rules":       []any{map[string]any{"toolName": req.ToolName}},
			"behavior":    "allow",
			"destination": "session",
		})
	}
	return out
}

func ControlResponseLine(requestID string, body any) []byte {
	return line(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response":   body,
		},
	})
}

func ControlErrorLine(requestID, msg string) []byte {
	return line(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "error",
			"request_id": requestID,
			"error":      msg,
		},
	})
}

type RemoteControlInfo struct {
	SessionURL string `json:"session_url"`
	ConnectURL string `json:"connect_url"`
}

func (e *Event) RemoteControlInfo() (RemoteControlInfo, bool) {
	var info RemoteControlInfo
	if e.Response == nil || len(e.Response.Response) == 0 {
		return info, false
	}
	if json.Unmarshal(e.Response.Response, &info) != nil || info.SessionURL == "" {
		return info, false
	}
	return info, true
}

type Command struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argumentHint"`
}

func ParseCommands(raw json.RawMessage) []Command {
	var body struct {
		Commands []Command `json:"commands"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return nil
	}
	out := body.Commands[:0]
	for _, c := range body.Commands {
		if c.Name != "" {
			out = append(out, c)
		}
	}
	return out
}

type RateWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resetsAt"`
	Status      string  `json:"status"`
}

type RateLimit struct {
	Status         string                `json:"status"`
	Type           string                `json:"rateLimitType"`
	Utilization    float64               `json:"utilization"`
	ResetsAt       int64                 `json:"resetsAt"`
	UnifiedWindows map[string]RateWindow `json:"unifiedWindows"`
}

func (r *RateLimit) Window(name string) (RateWindow, bool) {
	if r == nil {
		return RateWindow{}, false
	}
	if w, ok := r.UnifiedWindows[name]; ok {
		return w, true
	}
	if r.Type == name {
		return RateWindow{Utilization: r.Utilization, ResetsAt: r.ResetsAt, Status: r.Status}, true
	}
	return RateWindow{}, false
}
