package chat

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/katachinosekai/quill/internal/protocol"
)

type Kind int

const (
	User Kind = iota
	Assistant
	Tool
	Permission
	Notice
	Error
	Thinking
	Plan
)

type PermState int

const (
	PermPending PermState = iota
	PermAllowed
	PermAllowedAlways
	PermDenied
	PermCancelled
	PermAutoAllowed
	PermPlanAsked
)

type Item struct {
	Seq         int
	Kind        Kind
	Text        string
	Streaming   bool
	InPlan      bool
	MsgID       string
	ToolName    string
	ToolID      string
	Summary     string
	Input       json.RawMessage
	inputBuf    strings.Builder
	Result      string
	ResultError bool
	HasResult   bool
	Diff        []DiffLine
	DiffPath    string
	RequestID   string
	Request     *protocol.ControlRequest
	PermState   PermState
}

type Stats struct {
	CostUSD     float64
	ContextUsed int
	ContextMax  int
	Turns       int
	Model       string
}

type Conversation struct {
	Items      []*Item
	SessionID  string
	Model      string
	Mode       string
	Cwd        string
	Stats      Stats
	Busy       bool
	Thinking   bool
	Suggestion string
	streamKeys map[string]*Item
	thinkKeys  map[string]string
	byTool     map[string]*Item
	byRequest  map[string]*Item
	seq        int
}

func New() *Conversation {
	return &Conversation{
		streamKeys: map[string]*Item{},
		thinkKeys:  map[string]string{},
		byTool:     map[string]*Item{},
		byRequest:  map[string]*Item{},
	}
}

func (c *Conversation) add(it *Item) *Item {
	c.seq++
	it.Seq = c.seq
	c.Items = append(c.Items, it)
	return it
}

func (c *Conversation) AddUser(text string) *Item {
	c.Suggestion = ""
	return c.add(&Item{Kind: User, Text: text})
}

func (c *Conversation) AddPlan() *Item {
	return c.add(&Item{Kind: Plan, PermState: PermPending})
}

func (c *Conversation) AsksApproval() bool {
	for i := len(c.Items) - 1; i >= 0; i-- {
		it := c.Items[i]
		if it.Kind == User || it.Kind == Plan {
			return false
		}
		if it.Kind != Assistant || strings.TrimSpace(it.Text) == "" {
			continue
		}
		t := strings.TrimRight(strings.TrimSpace(it.Text), "*_ ")
		last := t[strings.LastIndex(t, "\n")+1:]
		return strings.HasSuffix(t, "?") && strings.Contains(strings.ToLower(last), "approve")
	}
	return false
}

func (c *Conversation) MarkPlanText() []*Item {
	var out []*Item
	for i := len(c.Items) - 1; i >= 0; i-- {
		it := c.Items[i]
		if it.Kind == User || it.Kind == Plan || it.Kind == Permission && it.PermState == PermPlanAsked {
			break
		}
		if it.Kind == Assistant && strings.TrimSpace(it.Text) != "" {
			it.InPlan = true
			out = append(out, it)
		}
	}
	return out
}

func (c *Conversation) PlanText(plan *Item) string {
	var parts []string
	for i := len(c.Items) - 1; i >= 0; i-- {
		if c.Items[i] != plan {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			it := c.Items[j]
			if it.Kind == User || it.Kind == Plan {
				break
			}
			if it.Kind == Assistant && it.InPlan {
				parts = append([]string{it.Text}, parts...)
			}
		}
		break
	}
	return strings.Join(parts, "\n\n")
}

func (c *Conversation) AddNotice(text string) *Item {
	return c.add(&Item{Kind: Notice, Text: text})
}

func (c *Conversation) AddError(text string) *Item {
	return c.add(&Item{Kind: Error, Text: text})
}

func (c *Conversation) PendingPermissions() []*Item {
	var out []*Item
	for _, it := range c.Items {
		if (it.Kind == Permission || it.Kind == Plan) && it.PermState == PermPending {
			out = append(out, it)
		}
	}
	return out
}

func (c *Conversation) Permission(requestID string) *Item {
	return c.byRequest[requestID]
}

func (c *Conversation) CancelPending() []*Item {
	var out []*Item
	for _, it := range c.PendingPermissions() {
		it.PermState = PermCancelled
		out = append(out, it)
	}
	return out
}

func (c *Conversation) Apply(ev *protocol.Event) []*Item {
	if ev.IsSidechain || ev.IsSubagent() {
		return nil
	}
	switch ev.Type {
	case "user":
		return c.applyUser(ev)
	case "assistant":
		return c.applyAssistant(ev)
	case "stream_event":
		return c.applyStream(ev)
	case "control_request":
		return c.applyControl(ev)
	case "prompt_suggestion":
		c.Suggestion = strings.TrimSpace(ev.Suggestion)
		return nil
	case "control_cancel_request":
		if it := c.byRequest[ev.RequestID]; it != nil && it.PermState == PermPending {
			it.PermState = PermCancelled
			return []*Item{it}
		}
	case "system":
		return c.applySystem(ev)
	case "result":
		return c.applyResult(ev)
	}
	return nil
}

func (c *Conversation) applySystem(ev *protocol.Event) []*Item {
	switch ev.Subtype {
	case "init":
		if ev.SessionID != "" {
			c.SessionID = ev.SessionID
		}
		c.Model = ev.Model
		c.Mode = ev.PermissionMode
		c.Cwd = ev.Cwd
		c.Stats.Model = ev.Model
	case "status":
		c.Busy = true
	case "thinking_tokens":
		c.Thinking = true
	case "api_retry":
		return []*Item{c.AddNotice("Retrying API request...")}
	case "compact_boundary":
		return []*Item{c.AddNotice("Conversation compacted")}
	}
	return nil
}

func (c *Conversation) applyResult(ev *protocol.Event) []*Item {
	c.Busy = false
	c.Thinking = false
	if ev.TotalCostUSD > 0 {
		c.Stats.CostUSD = ev.TotalCostUSD
	}
	c.Stats.Turns += ev.NumTurns
	used, window := ev.LastContext()
	if used > 0 {
		c.Stats.ContextUsed = used
	}
	if window > 0 {
		c.Stats.ContextMax = window
	}
	var out []*Item
	for _, it := range c.Items {
		if it.Streaming {
			it.Streaming = false
			out = append(out, it)
		}
	}
	for _, it := range c.CancelPending() {
		out = append(out, it)
	}
	if strings.HasPrefix(ev.TerminalReason, "aborted") {
		return out
	}
	if ev.IsError || (ev.Subtype != "" && ev.Subtype != "success") {
		msg := ev.Result
		if msg == "" && len(ev.Errors) > 0 {
			msg = strings.Join(ev.Errors, "\n")
		}
		if msg == "" {
			msg = ev.Subtype
		}
		out = append(out, c.AddError(msg))
	}
	return out
}

var (
	cmdName   = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	cmdArgs   = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	cmdStdout = regexp.MustCompile(`(?s)<local-command-stdout>(.*?)</local-command-stdout>`)
)

func CleanUserText(s string) (string, bool) {
	t := strings.TrimSpace(s)
	if t == "" {
		return "", false
	}
	if m := cmdName.FindStringSubmatch(t); m != nil {
		args := ""
		if a := cmdArgs.FindStringSubmatch(t); a != nil {
			args = strings.TrimSpace(a[1])
		}
		name := strings.TrimSpace(m[1])
		if args != "" {
			name += " " + args
		}
		return name, true
	}
	if m := cmdStdout.FindStringSubmatch(t); m != nil {
		return "", false
	}
	for _, p := range []string{"<local-command-caveat>", "<system-reminder>", "<task-notification>", "<local-command-stderr>"} {
		if strings.HasPrefix(t, p) {
			return "", false
		}
	}
	return t, true
}

func (c *Conversation) applyUser(ev *protocol.Event) []*Item {
	if ev.IsMeta || ev.Message == nil {
		return nil
	}
	var out []*Item
	for _, b := range ev.Message.Blocks() {
		switch b.Type {
		case "tool_result":
			it := c.byTool[b.ToolUseID]
			if it == nil {
				continue
			}
			it.Result = protocol.ContentText(b.Content)
			it.ResultError = b.IsError
			it.HasResult = true
			out = append(out, it)
		case "text":
			t, ok := CleanUserText(b.Text)
			if !ok {
				continue
			}
			if strings.HasPrefix(t, "[Request interrupted by user") {
				out = append(out, c.AddNotice("Interrupted"))
				continue
			}
			out = append(out, c.AddUser(t))
		case "image":
			out = append(out, c.AddUser("[image]"))
		}
	}
	return out
}

func (c *Conversation) applyAssistant(ev *protocol.Event) []*Item {
	if ev.Message == nil {
		return nil
	}
	msgID := ev.Message.ID
	if ev.Message.Model != "" && ev.Message.Model != "<synthetic>" {
		c.Stats.Model = ev.Message.Model
	}
	if u := ev.Message.Usage; u != nil && u.Context() > 0 {
		c.Stats.ContextUsed = u.Context()
	}
	var out []*Item
	for _, b := range ev.Message.Blocks() {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) == "" || ev.Message.Model == "<synthetic>" && b.Text == "No response requested." {
				continue
			}
			var it *Item
			for _, cand := range c.Items {
				if cand.Kind == Assistant && cand.MsgID == msgID && msgID != "" && cand.Streaming {
					it = cand
					break
				}
			}
			if it == nil {
				it = c.add(&Item{Kind: Assistant, MsgID: msgID})
			}
			it.Text = b.Text
			it.Streaming = false
			out = append(out, it)
		case "thinking":
			if strings.TrimSpace(b.Thinking) == "" {
				continue
			}
			var it *Item
			for _, cand := range c.Items {
				if cand.Kind == Thinking && cand.MsgID == msgID && msgID != "" && (cand.Text == b.Thinking || cand.Streaming) {
					it = cand
					break
				}
			}
			if it == nil {
				it = c.add(&Item{Kind: Thinking, MsgID: msgID})
			}
			it.Text = b.Thinking
			it.Streaming = false
			out = append(out, it)
		case "tool_use":
			it := c.byTool[b.ID]
			if it == nil {
				it = c.add(&Item{Kind: Tool, ToolID: b.ID, MsgID: msgID})
				c.byTool[b.ID] = it
			}
			it.ToolName = b.Name
			it.Streaming = false
			c.setToolInput(it, b.Input)
			out = append(out, it)
		}
	}
	return out
}

func (c *Conversation) applyStream(ev *protocol.Event) []*Item {
	se := ev.Event
	if se == nil {
		return nil
	}
	key := fmt.Sprintf("%s#%d", ev.APIMessageID, se.Index)
	switch se.Type {
	case "message_start":
		c.Busy = true
	case "content_block_start":
		if se.ContentBlock == nil {
			return nil
		}
		switch se.ContentBlock.Type {
		case "text":
			it := c.add(&Item{Kind: Assistant, MsgID: ev.APIMessageID, Streaming: true})
			c.streamKeys[key] = it
			c.Thinking = false
			return []*Item{it}
		case "tool_use":
			it := c.byTool[se.ContentBlock.ID]
			if it == nil {
				it = c.add(&Item{Kind: Tool, ToolID: se.ContentBlock.ID, ToolName: se.ContentBlock.Name, MsgID: ev.APIMessageID, Streaming: true})
				c.byTool[se.ContentBlock.ID] = it
			}
			c.streamKeys[key] = it
			c.Thinking = false
			return []*Item{it}
		case "thinking":
			c.Thinking = true
			c.thinkKeys[key] = ev.APIMessageID
		}
	case "content_block_delta":
		if se.Delta != nil && se.Delta.Type == "thinking_delta" {
			if se.Delta.Thinking == "" {
				return nil
			}
			it := c.streamKeys[key]
			if it == nil {
				if _, ok := c.thinkKeys[key]; !ok {
					return nil
				}
				it = c.add(&Item{Kind: Thinking, MsgID: ev.APIMessageID, Streaming: true})
				c.streamKeys[key] = it
			}
			it.Text += se.Delta.Thinking
			return []*Item{it}
		}
		it := c.streamKeys[key]
		if it == nil || se.Delta == nil {
			return nil
		}
		switch se.Delta.Type {
		case "text_delta":
			if !it.Streaming {
				return nil
			}
			it.Text += se.Delta.Text
			return []*Item{it}
		case "input_json_delta":
			it.inputBuf.WriteString(se.Delta.PartialJSON)
		}
	case "content_block_stop":
		it := c.streamKeys[key]
		delete(c.streamKeys, key)
		if _, ok := c.thinkKeys[key]; ok {
			delete(c.thinkKeys, key)
			c.Thinking = false
		}
		if it != nil && it.Kind == Thinking {
			it.Streaming = false
			return []*Item{it}
		}
		if it != nil && it.Kind == Tool && len(it.Input) == 0 && it.inputBuf.Len() > 0 {
			c.setToolInput(it, json.RawMessage(it.inputBuf.String()))
			return []*Item{it}
		}
	}
	return nil
}

func (c *Conversation) applyControl(ev *protocol.Event) []*Item {
	if ev.Request == nil || ev.Request.Subtype != "can_use_tool" {
		return nil
	}
	it := c.add(&Item{
		Kind:      Permission,
		RequestID: ev.RequestID,
		Request:   ev.Request,
		ToolName:  ev.Request.ToolName,
		ToolID:    ev.Request.ToolUseID,
		PermState: PermPending,
	})
	it.Summary = ToolSummary(ev.Request.ToolName, ev.Request.Input)
	it.Input = ev.Request.Input
	it.Diff, it.DiffPath = ToolDiff(ev.Request.ToolName, ev.Request.Input)
	c.byRequest[ev.RequestID] = it
	return []*Item{it}
}

func (c *Conversation) setToolInput(it *Item, in json.RawMessage) {
	if len(in) == 0 || string(in) == "{}" && len(it.Input) > 0 {
		return
	}
	it.Input = in
	it.Summary = ToolSummary(it.ToolName, in)
	it.Diff, it.DiffPath = ToolDiff(it.ToolName, in)
}

func ToolSummary(name string, in json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(in, &m) != nil {
		return ""
	}
	str := func(k string) string {
		if v, ok := m[k].(string); ok {
			return v
		}
		return ""
	}
	first := func(keys ...string) string {
		for _, k := range keys {
			if v := str(k); v != "" {
				return v
			}
		}
		return ""
	}
	var s string
	switch name {
	case "Bash":
		s = first("description", "command")
		if cmd := str("command"); cmd != "" && s != cmd {
			s += ": " + cmd
		}
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		s = first("file_path", "notebook_path")
	case "Grep", "Glob":
		s = str("pattern")
		if p := str("path"); p != "" {
			s += " in " + p
		}
	case "WebFetch":
		s = str("url")
	case "WebSearch":
		s = str("query")
	case "Task", "Agent":
		s = first("description", "prompt")
	default:
		s = first("description", "file_path", "path", "command", "query", "url", "pattern", "prompt", "name")
	}
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 140 {
		s = string(r[:140]) + "..."
	}
	return s
}

func ToolDiff(name string, in json.RawMessage) ([]DiffLine, string) {
	var m struct {
		FilePath  string `json:"file_path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
		Content   string `json:"content"`
		Edits     []struct {
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		} `json:"edits"`
	}
	if json.Unmarshal(in, &m) != nil {
		return nil, ""
	}
	path := m.FilePath
	switch name {
	case "Edit":
		return LineDiff(m.OldString, m.NewString), path
	case "MultiEdit":
		var out []DiffLine
		for i, e := range m.Edits {
			if i > 0 {
				out = append(out, DiffLine{Op: ' ', Text: "..."})
			}
			out = append(out, LineDiff(e.OldString, e.NewString)...)
		}
		return out, path
	case "Write":
		return LineDiff("", m.Content), path
	}
	return nil, ""
}

func ShortPath(p, cwd string) string {
	if cwd != "" {
		if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return p
}
