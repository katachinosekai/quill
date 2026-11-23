package main

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"

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

// maxBatchCalls caps the tool_use ids remembered per turn.
const maxBatchCalls = 1024

// noteBatch remembers which assistant message each of the session's own
// tool_use blocks belongs to (the stream sends one event per block, sharing
// the message id).
func noteBatch(s *deskSession, ev *protocol.Event) {
	if ev.Type != "assistant" || ev.IsSubagent() || ev.Message == nil || ev.Message.ID == "" {
		return
	}
	for _, b := range ev.Message.Blocks() {
		if b.Type != "tool_use" || b.ID == "" || len(s.batchOf) >= maxBatchCalls {
			continue
		}
		if s.batchOf == nil {
			s.batchOf = map[string]string{}
		}
		s.batchOf[b.ID] = ev.Message.ID
	}
}

// refuseBatch: the gate refused r; the rest of its batch is refused too.
func refuseBatch(s *deskSession, r *protocol.ControlRequest) {
	if r == nil || r.AgentID != "" {
		return
	}
	msg, ok := s.batchOf[r.ToolUseID]
	if !ok {
		return
	}
	if s.batchRefused == nil {
		s.batchRefused = map[string]bool{}
	}
	s.batchRefused[msg] = true
}

// inRefusedBatch: r is the session's own call in a batch the gate already
// refused a call of.
func inRefusedBatch(s *deskSession, r *protocol.ControlRequest) bool {
	if r == nil || r.AgentID != "" || r.ToolUseID == "" {
		return false
	}
	msg, ok := s.batchOf[r.ToolUseID]
	return ok && s.batchRefused[msg]
}

// endBatches: the turn is over.
func endBatches(s *deskSession) {
	s.batchOf, s.batchRefused = nil, nil
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
