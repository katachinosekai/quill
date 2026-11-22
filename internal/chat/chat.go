package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

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
	Rotate
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
	PermRetryAllowed
	PermRuleAllowed
)

type Item struct {
	Seq         int
	Kind        Kind
	Text        string
	Streaming   bool
	InPlan      bool
	MsgID       string
	UUID        string
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
	bgTasks    map[string]BgTask
	CacheTTL   time.Duration
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

// approvalAsk: a closing question that asks to go ahead with a plan
// ("Approve this plan?", "Go ahead with steps 1–3?", "Okay to run this?",
// "Shall I proceed?"; user 2026-11-21 after PZ Launcher's missed cards). An
// ordinary question ("Anything else?") is not one. Since 2026-11-21 (user,
// via Zomboid Networking 2) a closing proposal ("Should I ship 1.3.7 now?",
// "Do you want me to adapt it next?") also gets the card, unless it names
// options ("A or B?") or asks which/what: those get option buttons.
var approvalAsk = regexp.MustCompile(`(?i)\bapprov|\bgo ahead\b|\bproceed\b|\b(?:okay|ok) to\b|\brun (?:this|it|these|them)\b|\bcarry (?:it|this|them) out\b|\bsteps? \d`)

// quotedSpans are quotes and parentheses: words in them are named, not asked
// ("…instead of "Approve plan & run all"?" is no approval question).
var quotedSpans = regexp.MustCompile("\"[^\"]*\"|“[^”]*”|`[^`]*`|\\([^)]*\\)")

var proposalAsk = regexp.MustCompile(`(?i)^\W*(?:should|shall|can|may) (?:i|we)\b|^\W*(?:do you )?want me to\b|^\W*would you like me to\b|^\W*ready for me to\b`)
var sentenceEnd = regexp.MustCompile(`[.!?]\s+`) // not the dots in 1.3.7
var whAsk = regexp.MustCompile(`(?i)^\W*(?:which|what|who|where|when|why|how)\b`)

// proposes: the last line's question (up to its last "?") offers to do work
// and names no options.
func proposes(last string) bool {
	q := last
	if i := strings.LastIndex(q, "?"); i >= 0 {
		q = q[:i]
	} else {
		return false
	}
	q = quotedSpans.ReplaceAllString(q, " ")
	// The question is the last sentence before the "?".
	if m := sentenceEnd.FindAllStringIndex(q, -1); len(m) > 0 {
		q = q[m[len(m)-1][1]:]
	}
	q = strings.TrimLeft(q, "*_ ")
	if whAsk.MatchString(q) || strings.Contains(" "+strings.ToLower(q)+" ", " or ") {
		return false
	}
	return proposalAsk.MatchString(q)
}

// lastQuestion: the last sentence of line that ends with "?" (quotes and
// parentheses blanked), or "".
func lastQuestion(line string) string {
	i := strings.LastIndex(line, "?")
	if i < 0 {
		return ""
	}
	q := quotedSpans.ReplaceAllString(line[:i], " ")
	if m := sentenceEnd.FindAllStringIndex(q, -1); len(m) > 0 {
		q = q[m[len(m)-1][1]:]
	}
	return strings.TrimLeft(q, "*_ ")
}

// AsksApproval: the turn's last text ends in a question asking to approve,
// or in a proposal of work (see approvalAsk).
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
		if strings.HasSuffix(t, "?") && asksToApprove(lastQuestion(last)) {
			return true
		}
		// The approval question can be followed by a note on the same line
		// ("Do you approve steps 1–3? Your approval also covers …."; user
		// 2026-11-21: a typed "Yes" to that was not taken as approval).
		if q := lastQuestion(last); q != "" && asksToApprove(q) {
			return true
		}
		// Rule 28 (user 2026-11-21): a plan ends with its questions numbered,
		// one per line; the approval can be any of them, not just the last.
		if ClosingApproval(t) != "" {
			return true
		}
		return proposes(last)
	}
	return false
}

var numberedQ = regexp.MustCompile(`^\s*(?:[-*]\s*)?(?:\*\*)?\d+[.)]\s+`)

// openAsk: questions that ask for information, not for a go ahead ("How
// would you like me to proceed?", "Why did step 3 fail?", "Did the approval
// dialog look right?"; two-model review 2026-11-22).
var openAsk = regexp.MustCompile(`(?i)^\W*(?:and |so |also )?(?:which|what|why|how|where|when|who|whose|did|does)\b`)

// asksToApprove: a question sentence that asks to go ahead with a plan.
func asksToApprove(q string) bool {
	q = strings.TrimSpace(quotedSpans.ReplaceAllString(q, " "))
	if q == "" || openAsk.MatchString(q) {
		return false
	}
	return approvalAsk.MatchString(q)
}

// ClosingApproval: the approval question among the numbered question lines
// that end a message ("1. Do you approve steps 1–6? …", "2. …?"), or "".
func ClosingApproval(text string) string {
	lines := strings.Split(strings.TrimRight(strings.TrimSpace(text), "*_ "), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		if !numberedQ.MatchString(l) || !strings.Contains(l, "?") {
			return ""
		}
		q := strings.TrimSpace(strings.Trim(numberedQ.ReplaceAllString(l, ""), "*_ "))
		if lq := lastQuestionOf(q); lq != "" && asksToApprove(lq) {
			return q
		}
	}
	return ""
}

// lastQuestionOf: the first question sentence of s (quotes blanked).
func lastQuestionOf(s string) string {
	i := strings.Index(s, "?")
	if i < 0 {
		return ""
	}
	return quotedSpans.ReplaceAllString(s[:i], " ")
}

// AsksQuestion: the turn's last text ends with a question to the user (its
// last line has a "?"), whatever it asks.
func (c *Conversation) AsksQuestion() bool {
	for i := len(c.Items) - 1; i >= 0; i-- {
		it := c.Items[i]
		if it.Kind == User || it.Kind == Plan {
			return false
		}
		if it.Kind != Assistant || strings.TrimSpace(it.Text) == "" {
			continue
		}
		t := strings.TrimSpace(it.Text)
		return strings.Contains(t[strings.LastIndex(t, "\n")+1:], "?")
	}
	return false
}

func Automated(text string) bool {
	t := strings.TrimSpace(text)
	for _, p := range []string{"<cross-session-message", "<channel-message", "<scheduled-trigger"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

func (c *Conversation) AwaitsReply() bool {
	human := -1
	for i := len(c.Items) - 1; i >= 0; i-- {
		if it := c.Items[i]; it.Kind == User && !Automated(it.Text) {
			human = i
			break
		}
	}
	var last *Item
	for _, it := range c.Items[human+1:] {
		if it.Kind == User {
			break
		}
		if it.Kind == Assistant && strings.TrimSpace(it.Text) != "" {
			last = it
		}
	}
	if last == nil || last.Streaming {
		return false
	}
	return strings.HasSuffix(strings.TrimRight(strings.TrimSpace(last.Text), "*_ "), "?")
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

func (c *Conversation) AddRotate(text string) *Item {
	return c.add(&Item{Kind: Rotate, Text: text, PermState: PermPending})
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
		if it.Kind == Plan {
			continue
		}
		it.PermState = PermCancelled
		out = append(out, it)
	}
	return out
}

func (c *Conversation) CancelPlans() []*Item {
	var out []*Item
	for _, it := range c.PendingPermissions() {
		if it.Kind == Plan {
			it.PermState = PermCancelled
			out = append(out, it)
		}
	}
	return out
}

func (c *Conversation) Apply(ev *protocol.Event) []*Item {
	if len(c.bgTasks) > 0 && bytes.Contains(ev.Raw, []byte("<task-notification>")) {
		c.NoteNotifications(string(ev.Raw))
	}
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
	used, window := ev.LastContext(c.Stats.Model, c.Model)
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
	if ev.Message == nil {
		return nil
	}
	for _, b := range ev.Message.Blocks() {
		if b.Type == "text" {
			c.taskNotification(b.Text)
		}
	}
	if ev.IsMeta {
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
			c.trackTask(it)
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
			u := c.AddUser(t)
			u.UUID = ev.UUID
			out = append(out, u)
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
		switch {
		case u.CacheCreation.Ephemeral1h > 0:
			c.CacheTTL = time.Hour
		case u.CacheCreation.Ephemeral5m > 0:
			c.CacheTTL = 5 * time.Minute
		}
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
			if ev.UUID != "" {
				it.UUID = ev.UUID
			}
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

var (
	bgShell      = regexp.MustCompile(`^\s*(?:Command running in background with ID:\s*|Command did not complete within[^\n]*?moved to the background \(ID:\s*)([A-Za-z0-9_-]{4,})`)
	bgAnyID      = regexp.MustCompile(`(?i)\b(?:task ?id|agentId|ID)[:=]?\s*([A-Za-z0-9_-]{6,})`)
	notifyID     = regexp.MustCompile(`<task-id>\s*([^<\s]+)\s*</task-id>`)
	notifyState  = regexp.MustCompile(`<status>\s*([a-z_]+)\s*</status>`)
	outputStatus = regexp.MustCompile(`(?i)(?:<status>|\bstatus\W{0,3})\s*(completed|failed|killed|stopped|cancelled|canceled|timeout|timed_out|error)\b`)
	taskDone     = map[string]bool{"completed": true, "failed": true, "killed": true, "stopped": true, "cancelled": true, "canceled": true, "timeout": true, "timed_out": true, "error": true}
)

func (c *Conversation) trackTask(it *Item) {
	if it.ResultError {
		return
	}
	var in struct {
		Background  bool   `json:"run_in_background"`
		TaskID      string `json:"task_id"`
		ShellID     string `json:"shell_id"`
		BashID      string `json:"bash_id"`
		Command     string `json:"command"`
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}
	_ = json.Unmarshal(it.Input, &in)
	id := ""
	switch it.ToolName {
	case "Bash":
		if m := bgShell.FindStringSubmatch(it.Result); m != nil {
			moved := strings.HasPrefix(strings.TrimSpace(it.Result), "Command did not complete within")
			if in.Background && !moved || moved && strings.Contains(it.Result, "Output is being written to:") {
				id = m[1]
			}
		}
	case "Monitor":
		if m := bgAnyID.FindStringSubmatch(it.Result); m != nil {
			id = m[1]
		}
	case "Agent", "Task":
		if in.Background {
			if m := bgAnyID.FindStringSubmatch(it.Result); m != nil {
				id = m[1]
			}
		}
	case "TaskStop", "KillShell", "KillBash":
		for _, k := range []string{in.TaskID, in.ShellID, in.BashID} {
			delete(c.bgTasks, k)
		}
		return
	case "TaskOutput", "BashOutput":
		if m := outputStatus.FindStringSubmatch(it.Result); m != nil && taskDone[strings.ToLower(m[1])] {
			for _, k := range []string{in.TaskID, in.ShellID, in.BashID} {
				delete(c.bgTasks, k)
			}
		}
		return
	}
	if id == "" {
		return
	}
	if c.bgTasks == nil {
		c.bgTasks = map[string]BgTask{}
	}
	what := in.Description
	if what == "" {
		what = in.Command
	}
	if what == "" {
		what = in.Prompt
	}
	t := BgTask{ID: id, Tool: it.ToolName, What: strings.TrimSpace(what), seq: len(c.bgTasks)}
	if m := bgLog.FindStringSubmatch(it.Result); m != nil {
		t.Log = strings.TrimRight(m[1], ".,")
	}
	c.bgTasks[id] = t
}

type BgTask struct {
	ID, Tool, What, Log string
	seq                 int
}

var bgLog = regexp.MustCompile(`Output is being written to:\s*(\S+)`)

func (c *Conversation) BackgroundTasks() []BgTask {
	out := make([]BgTask, 0, len(c.bgTasks))
	for _, t := range c.bgTasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

func (c *Conversation) taskNotification(text string) {
	c.NoteNotifications(text)
}

func (c *Conversation) NoteNotifications(text string) {
	parts := strings.Split(text, "<task-notification>")
	for _, part := range parts[1:] {
		if end := strings.Index(part, "</task-notification>"); end >= 0 {
			part = part[:end]
		}
		id := notifyID.FindStringSubmatch(part)
		st := notifyState.FindStringSubmatch(part)
		if id == nil || st == nil || !taskDone[st[1]] {
			continue
		}
		delete(c.bgTasks, id[1])
	}
}

func (c *Conversation) Background() int {
	return len(c.bgTasks)
}

func (c *Conversation) Working() int {
	n := 0
	for _, t := range c.bgTasks {
		if t.Tool != "Monitor" {
			n++
		}
	}
	return n
}

func (c *Conversation) ClearBackground() {
	c.bgTasks = nil
}
