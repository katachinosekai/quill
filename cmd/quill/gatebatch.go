package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

// One assistant message's tool batch is decided as a whole (ta43580, Quill
// Desktop security, 2026-11-23). In a batch of parallel calls, plan-first
// refused the Write that created a script and then let the Bash call next to
// it run `pkexec bash <that script>`: the refusal set planWait, which made the
// user's earlier "yes" (read again from the transcript) count as an approval
// for the very next request. So:
//
//   - Once a gated call of the session's own is refused, every other gated
//     call of the same assistant message is refused too, whatever approval
//     shows up in between. Read-only calls are not gated and still run.
//   - The transcript approval fallback never counts once a call of this turn
//     was refused: the message it reads came before that refusal, so it
//     can't be the answer to it.
//   - Privileged commands (privilegedCommand) never go through an allowlist
//     or a fallback; only an approved plan (or a subagent's grant under one)
//     runs them.
//   - The same holds for a subagent's own batch (one assistant message of
//     the subagent). A foreground subagent's messages are streamed; a
//     background one's are read from its transcript
//     (<session>/subagents/agent-<id>.jsonl), which has each tool_use block
//     before the CLI asks about it. A call whose message can't be found is
//     gated on its own, as before.
//
// Agent calls of a refused batch (Zomboid, 2026-11-23). The CLI starts a
// subagent without a can_use_tool request, so the gate never sees the call
// itself: in a batch of an Agent call and a Bash call the gate refused, the
// subagent ran (and did the refused work). The rule:
//
//   - An Agent call of a batch the gate refused a call of is refused too: the
//     subagent is stopped (stop_task) as soon as its task id is known, from
//     its launch result or task_started event, whichever order the calls and
//     the refusal come in, and any call it still makes is refused.
//   - An Agent call in a batch with no refusal starts; its own calls are
//     gated as always (a subagent's call is never the session's own call).

// maxBatchCalls caps the tool_use ids remembered per turn.
const maxBatchCalls = 1024

// stoppedAgentMessage: what a stopped subagent's late calls are told.
const stoppedAgentMessage = "Quill plan-first approval: this subagent was started in the same batch as a call the user has not approved, so it was stopped and nothing was run. Stop and report back; the main session will ask the user for a plan."

// noteBatch remembers which assistant message each tool_use block belongs to
// (the stream sends one event per block, sharing the message id), the
// session's own and a streamed subagent's, and the session's Agent calls.
func noteBatch(s *deskSession, ev *protocol.Event) []*chat.Item {
	if ev.Type != "assistant" || ev.Message == nil || ev.Message.ID == "" {
		return nil
	}
	var out []*chat.Item
	for _, b := range ev.Message.Blocks() {
		if b.Type != "tool_use" || b.ID == "" || len(s.batchOf) >= maxBatchCalls {
			continue
		}
		if s.batchOf == nil {
			s.batchOf = map[string]string{}
		}
		s.batchOf[b.ID] = ev.Message.ID
		if (b.Name == "Agent" || b.Name == "Task") && !ev.IsSubagent() {
			if s.batchAgents == nil {
				s.batchAgents = map[string]string{}
			}
			s.batchAgents[b.ID] = ""
			if s.batchRefused[ev.Message.ID] {
				out = append(out, refuseAgentCall(s, b.ID)...)
			}
		}
	}
	return out
}

// callBatch: the assistant message r's call belongs to, or "".
func (d *desk) callBatch(s *deskSession, r *protocol.ControlRequest) string {
	if r == nil || r.ToolUseID == "" {
		return ""
	}
	if msg, ok := s.batchOf[r.ToolUseID]; ok {
		return msg
	}
	if r.AgentID == "" {
		return ""
	}
	return subagentCallMessage(d.subagentTranscript(s, r.AgentID), r.ToolUseID)
}

// subagentTranscript: where Claude Code writes a subagent's transcript.
func (d *desk) subagentTranscript(s *deskSession, agent string) string {
	p := d.transcriptPath(s)
	if p == "" || agent == "" || strings.ContainsAny(agent, "/\\.") {
		return ""
	}
	return filepath.Join(strings.TrimSuffix(p, ".jsonl"), "subagents", "agent-"+agent+".jsonl")
}

// subagentCallMessage: the message id of the tool_use id in a transcript's
// tail, or "".
func subagentCallMessage(path, id string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	off := max(0, st.Size()-approvalTail)
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return ""
	}
	key := []byte(`"` + id + `"`)
	lines := bytes.Split(buf, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if !bytes.Contains(lines[i], key) {
			continue
		}
		var o struct {
			Type    string            `json:"type"`
			Message *protocol.Message `json:"message"`
		}
		if json.Unmarshal(lines[i], &o) != nil || o.Type != "assistant" || o.Message == nil {
			continue
		}
		for _, b := range o.Message.Blocks() {
			if b.Type == "tool_use" && b.ID == id {
				return o.Message.ID
			}
		}
	}
	return ""
}

// refuseBatch: the gate refused r; the rest of its batch is refused too,
// the session's Agent calls in it included.
func (d *desk) refuseBatch(s *deskSession, r *protocol.ControlRequest) []*chat.Item {
	msg := d.callBatch(s, r)
	if msg == "" {
		return nil
	}
	if r.AgentID != "" {
		// A subagent's batch can outlive the session's turn.
		if len(s.subRefused) >= maxBatchCalls {
			s.subRefused = nil
		}
		if s.subRefused == nil {
			s.subRefused = map[string]bool{}
		}
		s.subRefused[msg] = true
		return nil
	}
	if s.batchRefused == nil {
		s.batchRefused = map[string]bool{}
	}
	s.batchRefused[msg] = true
	var out []*chat.Item
	for id := range s.batchAgents {
		if s.batchOf[id] == msg {
			out = append(out, refuseAgentCall(s, id)...)
		}
	}
	return out
}

// inRefusedBatch: r's call is in a batch the gate already refused a call of.
func (d *desk) inRefusedBatch(s *deskSession, r *protocol.ControlRequest) bool {
	if r == nil || r.ToolUseID == "" {
		return false
	}
	if r.AgentID != "" {
		if len(s.subRefused) == 0 {
			return false
		}
		msg := d.callBatch(s, r)
		return msg != "" && s.subRefused[msg]
	}
	msg, ok := s.batchOf[r.ToolUseID]
	return ok && s.batchRefused[msg]
}

// refuseAgentCall: the Agent call id belongs to a refused batch. Its
// subagent is stopped now, or as soon as its task id is known.
func refuseAgentCall(s *deskSession, id string) []*chat.Item {
	if s.stopAgents[id] {
		return nil
	}
	if s.stopAgents == nil {
		s.stopAgents = map[string]bool{}
	}
	s.stopAgents[id] = true
	if task := s.batchAgents[id]; task != "" {
		return stopRefusedAgent(s, task)
	}
	return nil
}

// stopRefusedAgent stops the subagent task and refuses its later calls.
func stopRefusedAgent(s *deskSession, task string) []*chat.Item {
	if s.refusedAgents[task] || len(s.refusedAgents) >= maxBatchCalls {
		return nil
	}
	if s.refusedAgents == nil {
		s.refusedAgents = map[string]bool{}
	}
	s.refusedAgents[task] = true
	stopSubagent(s, task)
	return []*chat.Item{s.conv.AddNotice("Stopped a subagent started together with a refused call: plan-first needs your approval for that batch.")}
}

// stopSubagent asks the CLI to stop a subagent task (tests replace it).
var stopSubagent = func(s *deskSession, task string) {
	p := s.proc
	if p == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		p.Control(ctx, map[string]any{"subtype": "stop_task", "task_id": task})
	}()
}

// noteAgentTask learns the task id of this turn's Agent calls (task_started,
// or the launch result) and stops the subagent of a refused batch.
func noteAgentTask(s *deskSession, ev *protocol.Event) []*chat.Item {
	if len(s.batchAgents) == 0 {
		return nil
	}
	learn := func(id, task string) []*chat.Item {
		if old, ok := s.batchAgents[id]; !ok || task == "" || old != "" {
			return nil
		}
		s.batchAgents[id] = task
		if s.stopAgents[id] {
			return stopRefusedAgent(s, task)
		}
		return nil
	}
	if ev.Type == "system" && ev.Subtype == "task_started" {
		return learn(ev.ToolUseID, ev.TaskID)
	}
	if ev.Type != "user" || ev.IsSubagent() || ev.Message == nil {
		return nil
	}
	var out []*chat.Item
	var results []protocol.Block
	for _, b := range ev.Message.Blocks() {
		if _, ok := s.batchAgents[b.ToolUseID]; b.Type == "tool_result" && ok {
			results = append(results, b)
		}
	}
	for _, b := range results {
		if m := agentIDRe.FindSubmatch(b.Content); m != nil {
			out = append(out, learn(b.ToolUseID, string(m[1]))...)
		} else if len(results) == 1 {
			var r struct {
				AgentID string `json:"agentId"`
			}
			json.Unmarshal(ev.ToolUseResult, &r)
			out = append(out, learn(b.ToolUseID, r.AgentID)...)
		}
	}
	return out
}

// endBatches: the session's turn is over.
func endBatches(s *deskSession) {
	s.batchOf, s.batchRefused = nil, nil
	s.batchAgents, s.stopAgents, s.planSends = nil, nil, nil
}

// escalators run a command as another user (root).
var escalators = map[string]bool{"sudo": true, "sudoedit": true, "su": true, "doas": true, "pkexec": true, "run0": true, "machinectl": true, "systemd-run": true}

// rootTools change the system as root (or ask polkit to).
var rootTools = map[string]bool{
	"mount": true, "umount": true, "swapon": true, "swapoff": true, "mkswap": true, "mkfs": true, "fdisk": true, "sfdisk": true,
	"gdisk": true, "parted": true, "wipefs": true, "cryptsetup": true, "losetup": true, "chroot": true, "reboot": true,
	"shutdown": true, "poweroff": true, "halt": true, "modprobe": true, "insmod": true, "rmmod": true, "udisksctl": true,
	"btrfs": true, "zramctl": true, "sysctl": true, "chown": true, "chattr": true, "setcap": true, "useradd": true,
	"usermod": true, "userdel": true, "passwd": true, "visudo": true, "dnf": true, "rpm": true, "flatpak": true, "grub2-mkconfig": true,
	"dracut": true, "efibootmgr": true, "firewall-cmd": true, "nmcli": true, "localectl": true, "timedatectl": true, "hostnamectl": true,
}

// wrappers run the command that follows them (or one in their arguments).
var wrappers = map[string]bool{
	"env": true, "nice": true, "nohup": true, "timeout": true, "stdbuf": true, "ionice": true, "setsid": true, "time": true,
	"command": true, "exec": true, "xargs": true, "chrt": true, "taskset": true, "flock": true, "watch": true, "unbuffer": true,
	"script": true, "parallel": true, "ssh": true, "sh": true, "bash": true, "zsh": true, "dash": true, "fish": true, "eval": true,
	"systemd-inhibit": true, "strace": true, "ltrace": true, "gdb": true, "busybox": true,
}

var systemctlReads = map[string]bool{
	"status": true, "show": true, "cat": true, "list-units": true, "list-unit-files": true, "list-timers": true, "list-sockets": true,
	"list-jobs": true, "list-dependencies": true, "list-machines": true, "is-active": true, "is-enabled": true, "is-failed": true,
	"is-system-running": true, "help": true, "get-default": true, "show-environment": true,
}

// escalatorWord: an escalator named anywhere in a word (`bash -c "sudo x"`,
// `$(pkexec y)`, `/usr/bin/doas`).
var escalatorWord = regexp.MustCompile(`(?:^|[^\w.-])(?:sudo|sudoedit|su|doas|pkexec|run0)(?:[^\w.-]|$)`)

func cmdName(w string) string {
	n := path.Base(w)
	if strings.HasPrefix(n, "mkfs.") {
		return "mkfs"
	}
	return n
}

// privilegedWords: one shell segment's words run something as root.
func privilegedWords(f []string) bool {
	for len(f) > 0 && assignName.MatchString(f[0]) {
		f = f[1:]
	}
	if len(f) == 0 {
		return false
	}
	name := cmdName(f[0])
	switch {
	case escalators[name] || rootTools[name]:
		return true
	case name == "systemctl":
		user := false
		for _, a := range f[1:] {
			if a == "--user" {
				user = true
			}
		}
		if user {
			return false
		}
		for _, a := range f[1:] {
			if !strings.HasPrefix(a, "-") {
				return !systemctlReads[a]
			}
		}
		return false
	case wrappers[name]:
		for _, w := range f[1:] {
			if escalatorWord.MatchString(w) {
				return true
			}
			if n := cmdName(w); rootTools[n] || n == "systemctl" {
				return true
			}
		}
	}
	return false
}

// privilegedCommand: a shell command runs (or may run) something as root. A
// command the parser can't follow counts when it names an escalator at all.
func privilegedCommand(cmd string) bool {
	segs, ok := shSegments(cmd)
	if !ok {
		return escalatorWord.MatchString(cmd)
	}
	for _, seg := range segs {
		f := make([]string, 0, len(seg))
		unknown := false
		for _, w := range seg {
			f = append(f, w.text)
			unknown = unknown || w.unknown
		}
		if privilegedWords(f) {
			return true
		}
		if unknown && escalatorWord.MatchString(strings.Join(f, " ")) {
			return true
		}
	}
	return strings.Contains(cmd, "$(") && escalatorWord.MatchString(cmd) || strings.Contains(cmd, "`") && escalatorWord.MatchString(cmd)
}

// privilegedRequest: r is a Bash call running a privileged command.
func privilegedRequest(r *protocol.ControlRequest) bool {
	if r == nil || r.ToolName != "Bash" {
		return false
	}
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(r.Input, &in) != nil {
		return true // can't tell: the safe side
	}
	return privilegedCommand(in.Command)
}
