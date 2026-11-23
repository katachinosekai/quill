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
	"strconv"
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
var escalators = map[string]bool{"sudo": true, "sudoedit": true, "su": true, "doas": true, "pkexec": true, "run0": true, "machinectl": true, "systemd-run": true,
	"kdesu": true, "kdesudo": true, "gksu": true, "gksudo": true, "sudo-rs": true}

// rootTools change the system as root (or ask polkit to).
var rootTools = map[string]bool{
	"mount": true, "umount": true, "swapon": true, "swapoff": true, "mkswap": true, "mkfs": true, "fdisk": true, "sfdisk": true,
	"gdisk": true, "parted": true, "wipefs": true, "cryptsetup": true, "losetup": true, "chroot": true, "reboot": true,
	"shutdown": true, "poweroff": true, "halt": true, "modprobe": true, "insmod": true, "rmmod": true, "udisksctl": true,
	"btrfs": true, "zramctl": true, "sysctl": true, "chown": true, "chattr": true, "setcap": true, "useradd": true,
	"usermod": true, "userdel": true, "passwd": true, "visudo": true, "dnf": true, "rpm": true, "flatpak": true, "grub2-mkconfig": true,
	"dracut": true, "efibootmgr": true, "firewall-cmd": true, "nmcli": true, "localectl": true, "timedatectl": true, "hostnamectl": true,
	"pkcon": true, "rpm-ostree": true, "fwupdmgr": true, "bootctl": true, "nsenter": true, "grubby": true, "kernel-install": true,
}

// wrappers run the command that follows them (or one in their arguments).
var wrappers = map[string]bool{
	"env": true, "nice": true, "nohup": true, "timeout": true, "stdbuf": true, "ionice": true, "setsid": true, "time": true,
	"command": true, "exec": true, "xargs": true, "chrt": true, "taskset": true, "flock": true, "watch": true, "unbuffer": true,
	"script": true, "parallel": true, "ssh": true, "sh": true, "bash": true, "zsh": true, "dash": true, "fish": true, "eval": true,
	"systemd-inhibit": true, "strace": true, "ltrace": true, "gdb": true, "busybox": true,
	// shell keywords and runners whose later words are commands too
	"do": true, "then": true, "else": true, "elif": true, "if": true, "while": true, "until": true, "{": true, "!": true,
	"find": true, "flatpak-spawn": true, "toolbox": true, "distrobox-host-exec": true, "distrobox-enter": true, "host-spawn": true,
}

var systemctlReads = map[string]bool{
	"status": true, "show": true, "cat": true, "list-units": true, "list-unit-files": true, "list-timers": true, "list-sockets": true,
	"list-jobs": true, "list-dependencies": true, "list-machines": true, "is-active": true, "is-enabled": true, "is-failed": true,
	"is-system-running": true, "help": true, "get-default": true, "show-environment": true,
}

// escalatorWord: an escalator named anywhere in a word (`bash -c "sudo x"`,
// `$(pkexec y)`, `/usr/bin/doas`).
var escalatorWord = regexp.MustCompile(`(?:^|[^\w.-])(?:sudo|sudoedit|su|doas|pkexec|run0|kdesu|kdesudo|gksu|gksudo|sudo-rs)(?:[^\w.-]|$)`)

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

// Auto-approve's root check (autoapprove.go, review 2026-11-23) is
// deliberately cruder than privilegedCommand: it doesn't follow the shell
// grammar, it looks at every word of the command, inside quotes, -c / -e
// strings, loop and brace bodies and find -exec alike. A false alarm only
// costs a plan (read-only commands still run without one); a miss runs
// something as root with nobody asked. A command needs a plan when:
//   - any word (after dropping quotes and backslashes, decoding \x / \u /
//     octal escapes and expanding {a,b} braces) is an escalator or a tool in
//     rootTools / autoRootWords, or systemctl with a verb that changes
//     something, or docker / podman with anything but a read verb;
//   - it logs in as root over ssh, sets a setuid/setgid bit, starts a
//     command from a variable, substitution or glob, pipes a decoder or
//     downloader into an interpreter, or has an interpreter's inline code
//     run other programs (system, subprocess, exec…);
//   - a script, Makefile, package.json or git hook it runs (an existing
//     file it names, read now) does any of the above.
// What it can't see is in docs/plan-format.md (Auto-approve).

// autoRootWords: besides escalators and rootTools, words that need a plan
// under auto-approve wherever they appear.
var autoRootWords = map[string]bool{
	"unshare": true, "setpriv": true, "busctl": true, "eval": true, "sg": true, "newgrp": true,
	"flatpak-spawn": true, "toolbox": true, "distrobox-host-exec": true, "distrobox-enter": true, "host-spawn": true,
	"capsh": true, "pkttyagent": true, "loginctl": true, "ostree": true, "semanage": true, "setsebool": true,
	"authselect": true, "update-alternatives": true, "alternatives": true, "akmods": true, "dkms": true,
	"systemd-sysext": true, "systemd-nspawn": true, "lxc": true, "incus": true, "virsh": true, "qemu-system-x86_64": true,
	"yum": true, "apt": true, "apt-get": true, "dpkg": true, "pacman": true, "zypper": true, "snap": true,
	"dbus-send": true, "gdbus": true, "qdbus": true, "nerdctl": true,
}

// autoEscalatorPart: an escalator's name inside a longer word (-Ssudo,
// x_pkexec); "su" alone is too common a part of words to count.
var autoEscalatorPart = regexp.MustCompile(`sudo|pkexec|doas|run0|kdesu|gksu`)

// autoRunners: words that run code from their arguments, input or files.
var autoRunners = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true, "busybox": true, "source": true,
	"xargs": true, "parallel": true, "make": true, "gmake": true, "just": true, "npm": true, "pnpm": true, "yarn": true, "npx": true,
	"python": true, "python3": true, "perl": true, "ruby": true, "node": true, "php": true, "lua": true, "deno": true, "bun": true,
	"tclsh": true, "awk": true, "gawk": true, "mawk": true, "env": true, "exec": true, "watch": true,
}

// autoInterps run inline code (-c / -e) that can start other programs.
var autoInterps = map[string]bool{
	"python": true, "python3": true, "perl": true, "ruby": true, "node": true, "php": true, "lua": true, "deno": true,
	"bun": true, "tclsh": true, "awk": true, "gawk": true, "mawk": true, "vim": true, "vi": true, "nvim": true, "emacs": true,
}

var (
	autoWordRe  = regexp.MustCompile(`[A-Za-z0-9_.+/-]+`)
	autoEscRe   = regexp.MustCompile(`\\(x[0-9A-Fa-f]{1,2}|u[0-9A-Fa-f]{4}|U[0-9A-Fa-f]{8}|[0-7]{1,4})`)
	autoBraceRe = regexp.MustCompile(`([A-Za-z0-9_./-]*)\{([^{}\s]*,[^{}\s]*)\}([A-Za-z0-9_./-]*)`)
	// a command word that is a variable, a substitution or a glob
	autoCmdPos = `(?:^|[;&|(){}` + "`" + `\n]|\b(?:then|do|else|exec|command|xargs|env|nohup|time|nice|timeout|setsid)\s)\s*(?:\w+=\S*\s+)*`
	autoVarCmd = regexp.MustCompile(autoCmdPos + `(?:\$|[^\s;&|()\[]*[?*])`)
	// in a Makefile $(GO) is a make variable (its value is in the file, read
	// too); a shell variable is $$x
	autoVarCmdMk = regexp.MustCompile(autoCmdPos + `(?:\$\$|[^\s;&|()\[$]*[?*])`)
	// an interpreter's code starting other programs
	autoSpawnRe     = regexp.MustCompile(`(?i)\b(?:system|popen|subprocess|spawn\w*|child_process|exec[lv]?p?e?|os\.exec\w*|pty|ctypes|syscall|qx|getattr|__import__|importlib|compile)\b`)
	autoDockerReads = map[string]bool{"ps": true, "images": true, "inspect": true, "logs": true, "version": true, "info": true,
		"stats": true, "top": true, "port": true, "history": true, "search": true, "--version": true, "--help": true, "help": true}
	autoDecoders   = map[string]bool{"base64": true, "base32": true, "basenc": true, "xxd": true, "od": true, "openssl": true, "rev": true, "tr": true, "gzip": true, "gunzip": true, "zcat": true, "uudecode": true, "printf": true, "curl": true, "wget": true}
	autoShellWords = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true, "source": true, "python": true, "python3": true, "perl": true, "ruby": true, "node": true}
)

// autoDecode turns \xHH, \uHHHH and octal escapes into the characters
// they stand for ($'\x73udo', printf '\163udo').
func autoDecode(s string) string {
	return autoEscRe.ReplaceAllStringFunc(s, func(m string) string {
		e := m[1:]
		var n uint64
		var err error
		switch e[0] {
		case 'x', 'u', 'U':
			n, err = strconv.ParseUint(e[1:], 16, 32)
		default:
			n, err = strconv.ParseUint(e, 8, 32)
		}
		if err != nil || n == 0 {
			return m
		}
		return string(rune(n))
	})
}

// autoExpandBraces expands {a,b} lists (s{u,x}do → sudo sxdo).
func autoExpandBraces(s string) string {
	for range 8 {
		t := autoBraceRe.ReplaceAllStringFunc(s, func(m string) string {
			p := autoBraceRe.FindStringSubmatch(m)
			var alts []string
			for _, a := range strings.Split(p[2], ",") {
				alts = append(alts, p[1]+a+p[3])
			}
			return strings.Join(alts, " ")
		})
		if t == s {
			break
		}
		s = t
	}
	return s
}

// autoForms: the command as written and decoded, quotes and backslashes
// dropped, braces expanded.
func autoForms(cmd string) []string {
	strip := strings.NewReplacer(`\`, "", `"`, "", `'`, "")
	forms := []string{autoExpandBraces(strip.Replace(cmd))}
	if d := autoDecode(cmd); d != cmd {
		forms = append(forms, autoExpandBraces(strip.Replace(d)))
	}
	return forms
}

// autoMaxFiles caps the files one check reads; autoMaxSize their size.
const (
	autoMaxFiles = 64
	autoMaxSize  = 1 << 20
)

// autoNeedsPlan: under auto-approve, cmd (run in cwd) still needs a plan.
func autoNeedsPlan(cmd, cwd string) bool {
	if privilegedCommand(cmd) {
		return true
	}
	budget := autoMaxFiles
	return autoScan(cmd, cwd, 0, false, false, &budget)
}

// autoScan checks one text (a command, or a file it runs; interp: the file
// is a program an interpreter runs; mk: a Makefile).
func autoScan(text, cwd string, depth int, interp, mk bool, budget *int) bool {
	for _, form := range autoForms(text) {
		if autoRiskyForm(form, interp, mk) {
			return true
		}
	}
	if depth >= 3 {
		return false
	}
	// Files the command runs (or may run): read them now.
	words := autoWordRe.FindAllString(autoForms(text)[0], -1)
	runs, interpRun := false, false
	for _, w := range words {
		n := cmdName(w)
		if autoRunners[n] {
			runs = true
		}
		if autoInterps[n] {
			interpRun = true
		}
	}
	var files []string
	for i, w := range words {
		if strings.HasPrefix(w, "./") || strings.HasPrefix(w, "/") || runs {
			files = append(files, w)
		}
		if w == "." && i+1 < len(words) {
			files = append(files, words[i+1]) // `. file` sources it (find . -name: no such file)
		}
		switch cmdName(w) {
		case "make", "gmake":
			files = append(files, "Makefile", "makefile", "GNUmakefile")
		case "npm", "pnpm", "yarn", "npx":
			files = append(files, "package.json")
		case "just":
			files = append(files, "justfile", "Justfile")
		case "git":
			if ents, err := os.ReadDir(filepath.Join(cwd, ".git", "hooks")); err == nil {
				for _, e := range ents {
					if !strings.HasSuffix(e.Name(), ".sample") {
						files = append(files, filepath.Join(".git", "hooks", e.Name()))
					}
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, f := range files {
		p := f
		if !filepath.IsAbs(p) {
			if cwd == "" {
				continue
			}
			p = filepath.Join(cwd, p)
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() || st.Size() > autoMaxSize {
			continue
		}
		if *budget <= 0 {
			return true // too much to read: the safe side
		}
		*budget--
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if bytes.IndexByte(b, 0) >= 0 {
			continue // a binary, not a script
		}
		base := filepath.Base(p)
		mk := base == "Makefile" || base == "makefile" || base == "GNUmakefile" || strings.HasSuffix(base, ".mk")
		if autoScan(string(b), filepath.Dir(p), depth+1, interpRun, mk, budget) {
			return true
		}
	}
	return false
}

// autoRiskyForm: the rules that read one normalised form of a text.
func autoRiskyForm(form string, interp, mk bool) bool {
	varCmd := autoVarCmd
	if mk {
		varCmd = autoVarCmdMk
	}
	if varCmd.MatchString(form) || autoEscalatorPart.MatchString(form) {
		return true
	}
	words := autoWordRe.FindAllString(form, -1)
	names := make([]string, len(words))
	has := map[string]bool{}
	for i, w := range words {
		names[i] = cmdName(w)
		has[names[i]] = true
	}
	user := has["--user"]
	for i, n := range names {
		next := ""
		if i+1 < len(names) {
			next = words[i+1]
		}
		switch {
		case escalators[n] || rootTools[n] || autoRootWords[n]:
			return true
		case n == "systemctl" && !user:
			for _, a := range words[i+1:] {
				if !strings.HasPrefix(a, "-") {
					if !systemctlReads[a] {
						return true
					}
					break
				}
			}
		case n == "docker" || n == "podman":
			if !autoDockerReads[next] {
				return true
			}
		case (n == "chmod" || n == "install") && autoSetuid(words[i+1:]):
			return true
		case (n == "ssh" || n == "scp" || n == "sftp" || n == "rsync" || n == "mosh") && has["root"]:
			return true
		case autoInterps[n]:
			interp = true
		}
	}
	if interp && autoSpawnRe.MatchString(form) {
		return true
	}
	decode, shell := false, false
	for n := range has {
		decode = decode || autoDecoders[n]
		shell = shell || autoShellWords[n]
	}
	return decode && shell
}

// autoSetuid: chmod / install arguments that set a setuid or setgid bit.
func autoSetuid(args []string) bool {
	for _, a := range args {
		a = strings.TrimPrefix(a, "-m")
		if m, _ := regexp.MatchString(`^[ugoa]*[+=][rwxXst]*s[rwxXst]*$|^0?[2-7][0-7]{3}$`, a); m {
			return true
		}
	}
	return false
}
