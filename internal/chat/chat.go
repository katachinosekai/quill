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
	Time        time.Time // when it was said: the transcript's timestamp, or when it arrived
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
	at         time.Time // the event being applied (Apply), for new items
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
	// subagents and their tokens (agents.go)
	agents      map[string]*AgentUse
	agentByTool map[string]string
	agentSeq    int
	agentsRev   int
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
	if it.Time.IsZero() {
		it.Time = c.at
		if it.Time.IsZero() {
			it.Time = time.Now()
		}
	}
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

// optionsLine: a line that lists a question's options ("Yes (recommended) or
// no?", "Approve as is (recommended), approve with changes, or stop").
var optionsLine = regexp.MustCompile(`(?i)\bor\b`)

// JoinOptionLines puts an options line back on the question line above it
// ("1. Can I run steps 1–6?\nYes (recommended) or no?" → one line), so the
// question and its options are read together (user 2026-11-22: options on
// their own line got no card). The options line must follow the question
// directly, not be numbered itself, and gets a "?" if it has none.
func JoinOptionLines(text string) string {
	out, _ := JoinOptionLineSpans(text)
	return strings.Join(out, "\n")
}

// JoinOptionLineSpans is JoinOptionLines' lines, with the index of the
// original line each one starts at (a joined line covers its original lines
// up to the next one's start).
func JoinOptionLineSpans(text string) ([]string, []int) {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	first := make([]int, 0, len(lines))
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if n := len(out); n > 0 && t != "" && !numberedQ.MatchString(t) && optionsLine.MatchString(t) && len(strings.Fields(t)) <= 30 {
			prev := strings.TrimRight(strings.TrimSpace(out[n-1]), "*_ ")
			if strings.HasSuffix(prev, "?") {
				opt := strings.TrimSpace(strings.Trim(t, "*_ "))
				if !strings.HasSuffix(opt, "?") {
					opt = strings.TrimRight(opt, ".!") + "?"
				}
				out[n-1] = prev + " " + opt
				continue
			}
		}
		out = append(out, l)
		first = append(first, i)
	}
	return out, first
}

var yesNoOptsRe = regexp.MustCompile(`(?i)^\W*yes\b.*\bor\b.*\bno\b`)

// yesNoProposal: "Can I run `kmem find x`? Yes (recommended) or no?": an offer
// to do work whose options are Yes and no. Its Yes is a go-ahead even though
// the question names no plan words (user 2026-11-22, tf2db21: a quick-choice
// Yes to it didn't count as plan approval).
func yesNoProposal(line string) bool {
	line = quotedSpans.ReplaceAllString(numberedQ.ReplaceAllString(strings.TrimSpace(line), ""), " ")
	i := strings.Index(line, "?")
	if i < 0 {
		return false
	}
	q := line[:i]
	if m := sentenceEnd.FindAllStringIndex(q, -1); len(m) > 0 {
		q = q[m[len(m)-1][1]:]
	}
	q = strings.TrimLeft(q, "*_ ")
	rest := strings.Trim(line[i+1:], "*_ ")
	if !yesNoOptsRe.MatchString(rest) {
		return false
	}
	return proposalAsk.MatchString(q) || actionAsk(q, rest)
}

// actionVerbs: verbs a proposal of work starts with ("Fix 2 and 3?",
// "Release Quill Android 0.9.36 now?", "Merge it?"). Verbs that ask for
// information (show, check, tell, explain) and that stop things (stop,
// cancel, kill) are left out on purpose: a Yes there is not a go-ahead.
const actionVerbs = `fix|merge|release|ship|run|rerun|re-run|commit|push|delete|remove|apply|add|update|create|start|rename|revert|deploy|publish|install|build|restart|rebase|implement|continue|proceed|tag|cut|bump|land|move|switch|enable|disable|reset|drop|archive|write|rewrite|save|refactor|migrate|port|replace|retry|redo|upload|send|patch|squash|cherry-pick|finish|go ahead|go with|do it|do that|do this|do both|do all|clean up|set up|turn on|turn off`

var actionStart = regexp.MustCompile(`(?i)^(?:(?:ok|okay|so|and|also|then|now)[,:]?\s+)?(?:` + actionVerbs + `)\b|^(?:ok|okay) to\b`)

// plainYesNo: the options of a bare yes/no question ("Yes or no?", "Yes
// (recommended) or no?" with the parentheses blanked).
var plainYesNo = regexp.MustCompile(`(?i)^yes,?\s+or\s+no[.!?]*$`)

// actionAsk: q, the closing question before "Yes or no?", proposes doing
// something: it starts with an imperative or action verb. Information
// questions ("Did it show its replies?", "Is that placement OK?") never do.
func actionAsk(q, rest string) bool {
	q = strings.TrimSpace(q)
	if q == "" || len(q) > 200 || openAsk.MatchString(q) || whAsk.MatchString(q) {
		return false
	}
	if !plainYesNo.MatchString(strings.Join(strings.Fields(rest), " ")) {
		return false
	}
	return actionStart.MatchString(q)
}

// ApprovalQuestion: q, a question put to the user (a dialog's question),
// asks to go ahead with work: its last sentence is an approval question
// (asksToApprove) or proposes doing something (ProposesWork).
func ApprovalQuestion(q string) bool {
	q = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(q), "*_ "))
	if !strings.HasSuffix(q, "?") {
		return false
	}
	last := lastQuestion(q)
	return asksToApprove(last) || ProposesWork(last)
}

// ProposesWork: question q (one sentence) proposes doing something: an offer
// ("Can I run …", "Want me to …") or an action verb ("Merge it", "Fix 2 and 3").
func ProposesWork(q string) bool {
	q = strings.TrimLeft(strings.TrimSpace(quotedSpans.ReplaceAllString(q, " ")), "*_ ")
	if q == "" || openAsk.MatchString(q) || whAsk.MatchString(q) {
		return false
	}
	return proposalAsk.MatchString(q) || len(q) <= 200 && actionStart.MatchString(q) || approvalAsk.MatchString(q)
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
		t := strings.TrimRight(strings.TrimSpace(JoinOptionLines(it.Text)), "*_ ")
		last := t[strings.LastIndex(t, "\n")+1:]
		if strings.HasSuffix(t, "?") && asksToApprove(lastQuestion(last)) {
			return true
		}
		// "Can I run steps 1–6? Yes (recommended) or no?": the options
		// sentence follows the approval question on its line.
		if q := firstQuestion(numberedQ.ReplaceAllString(last, "")); q != "" && asksToApprove(q) && strings.HasSuffix(t, "?") {
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
		if ClosingApproval(t) != "" || yesNoProposal(last) {
			return true
		}
		if RequestsYes(t) {
			return true
		}
		return proposes(last)
	}
	return false
}

// yesPlanHead: a plan heading that says it waits for the user's go-ahead
// ("**Plan (needs your yes):**", "Plan, awaiting your approval:").
var yesPlanHead = regexp.MustCompile(`(?i)^\W*(?:the |my )?plan\b.{0,40}?\b(?:needs|awaiting|waiting (?:for|on)|for)\s+your\s+(?:yes|approval|ok|okay|go[- ]ahead)\b.{0,20}$`)

// replyYes: a closing line that asks for a typed go-ahead without a question
// mark ("Reply **yes** to run it.", "Say yes and I'll start.").
var replyYes = regexp.MustCompile(`(?i)^\W*(?:just\s+|please\s+)?(?:reply|answer|say|type|send)\s+\W*(?:yes|approved?|ok|okay|go)\W*\s+(?:to|and|if)\b`)

// RequestsYes: text ends asking for the user's yes without a closing
// question (t225b3e, 2026-11-23: a plan under "**Plan (needs your yes):**"
// with numbered steps and nothing after them, and one ending "Reply **yes**
// to run it.", got no card, so a typed "yes" didn't approve). Either its last
// line asks for a typed yes, or a heading asking for the user's yes is
// followed only by the plan's numbered steps (and their indented or bulleted
// continuation lines).
func RequestsYes(text string) bool {
	lines := strings.Split(strings.TrimRight(strings.TrimSpace(text), "*_ "), "\n")
	if len(lines) == 0 {
		return false
	}
	if replyYes.MatchString(strings.TrimSpace(lines[len(lines)-1])) {
		return true
	}
	steps := 0
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		t := strings.TrimSpace(l)
		switch {
		case t == "":
		case numberedQ.MatchString(t):
			steps++
		case steps > 0 && yesPlanHead.MatchString(t):
			return true
		case (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") || strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ")):
		default:
			return false
		}
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
	lines := strings.Split(strings.TrimRight(strings.TrimSpace(JoinOptionLines(text)), "*_ "), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		if !numberedQ.MatchString(l) || !strings.Contains(l, "?") {
			return ""
		}
		q := strings.TrimSpace(strings.Trim(numberedQ.ReplaceAllString(l, ""), "*_ "))
		if lq := lastQuestionOf(q); lq != "" && asksToApprove(lq) || yesNoProposal(q) {
			return q
		}
	}
	return ""
}

// firstQuestion: the sentence that ends at the first "?" of line (quotes and
// parentheses blanked), or "".
func firstQuestion(line string) string {
	i := strings.Index(line, "?")
	if i < 0 {
		return ""
	}
	q := quotedSpans.ReplaceAllString(line[:i], " ")
	if m := sentenceEnd.FindAllStringIndex(q, -1); len(m) > 0 {
		q = q[m[len(m)-1][1]:]
	}
	return strings.TrimLeft(q, "*_ ")
}

// AsksToApprove: a question sentence that asks to go ahead with a plan.
func AsksToApprove(q string) bool { return asksToApprove(q) }

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
		t := strings.TrimSpace(JoinOptionLines(it.Text))
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
	// Items from a transcript keep the time they were said.
	c.at = time.Time{}
	if t, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil {
		c.at = t.Local()
	}
	defer func() { c.at = time.Time{} }()
	if (len(c.bgTasks) > 0 || len(c.agents) > 0) && bytes.Contains(ev.Raw, []byte("<task-notification>")) {
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
	case "task_started", "task_progress", "task_notification", "task_updated":
		c.applyTaskEvent(ev)
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
	results := 0
	for _, b := range ev.Message.Blocks() {
		if b.Type == "tool_result" {
			results++
		}
	}
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
			if len(c.agents) > 0 {
				c.agentResult(ev, it, results == 1)
			}
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
			c.noteAgentCall(ev, b)
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
			if a := c.agents[k]; k != "" && a != nil {
				c.endAgent(a, "stopped")
			}
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
		if id != nil && st != nil && len(c.agents) > 0 {
			c.agentNotification(part, id[1], st[1])
		}
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
	c.stopAgents()
}
