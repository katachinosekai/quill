package main

import (
	"encoding/json"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/katachinosekai/quill/internal/protocol"
)

// Gate split (decision fc42fe7, user 2026-11-24; audit
// /tmp/quill-design/guard-audit.md). Behind Settings → General → "Allow
// reads and kmem notes without a plan" (prefGateSplit, on by default; a
// Security setting). With it off the gate is what it was before.
//
//   - More reads: git rev-list, tag (list forms), branch (list forms),
//     ls-tree, ls-remote, cat-file -e/-p/-t/-s, worktree list, config --get;
//     `timeout N <read>`; `pzlock status`, `pzlock check NAME`; kmem
//     sessions, features, version, howto list/show, end-check, relate A.
//   - kmem bookkeeping (classNoPlan): `kmem add`, `kmem task
//     add|claim|checkpoint|note|release`, `kmem handoff save`, under the
//     session's own kmem name only (--as NAME, KMEM_SESSION=NAME, or no
//     --as when the session's environment sets KMEM_SESSION to it), no
//     --force, --pin, --retires or --transfer, no option kmem doesn't list
//     for them. kmem fences claims itself (owner-only note, checkpoint,
//     release, done). task done/transfer/block, retire, trash, pin, import,
//     … still need a plan.
//   - Zomboid launch and `pkill|killall <projectzomboid>` (classNoPlan) when
//     the installed kmem lists feature "pz-lock-closed": kmem-guard's pz-lock
//     guard then denies them fail-closed unless this session holds the lock,
//     so Quill leaves them to it. `pzlock acquire` stays gated.
//
// A no-plan call is not a read: it loses to a refused batch (gatebatch.go)
// like any gated call, but since it is allowed it never refuses a batch
// itself, so it no longer stops a subagent started next to it.

// prefGateSplit: the Security switch.
const prefGateSplit = "gateSplit"

// pzLockClosed: the kmem feature (`kmem features --json`, also in `kmem
// version --json`, which Quill already reads and caches: kmemclient.go)
// kmem-lock-audit adds once its pz-lock guard is fail-closed.
const pzLockClosed = "pz-lock-closed"

// gateSplitExplain: the caption under the switch in Settings.
const gateSplitExplain = "With Plan first on: more read-only commands (git rev-list, tag -l, ls-remote, worktree list, config --get, " +
	"timeout N <read>, pzlock status/check) and kmem bookkeeping under the session's own name (kmem add, task add/claim/" +
	"checkpoint/note/release, handoff save; never --force) run without a plan. task done, retire, trash, commits, pushes " +
	"and builds still need one. Off: only the reads Quill always allowed."

// gateClass: how plan-first treats one command.
type gateClass int

const (
	classRead   gateClass = iota // runs without a plan, also in a refused batch
	classNoPlan                  // runs without a plan; refused in a refused batch
	classGated                   // needs a plan
)

// Marker lines readOnlyExtra/readOnlyExtraFor add to extra. They start with a
// NUL byte, which no line parsed from the user's text can (parseReadOnlyExtra
// drops such lines).
const (
	markSplit = "\x00split"    // the switch is on
	markOwn   = "\x00own"      // {markOwn, NAME}: the session's own kmem name
	markPZ    = "\x00pzclosed" // kmem-guard's pz-lock is fail-closed
)

type gateOpts struct {
	split, pzClosed bool
	own             string
}

func gateOptsOf(extra [][]string) gateOpts {
	var o gateOpts
	for _, e := range extra {
		if len(e) == 0 {
			continue
		}
		switch e[0] {
		case markSplit:
			o.split = true
		case markOwn:
			if len(e) == 2 {
				o.own = e[1]
			}
		case markPZ:
			o.pzClosed = true
		}
	}
	if !o.split {
		o.own, o.pzClosed = "", false
	}
	return o
}

// sessionKmemEnv: KMEM_SESSION in a session's environment (sessions inherit
// Quill's; Quill sets none of its own). A var for tests.
var sessionKmemEnv = func() string { return os.Getenv("KMEM_SESSION") }

func (d *desk) gateSplitOn() bool {
	return d.app.Preferences().BoolWithFallback(prefGateSplit, true)
}

// requestClass: requestClass of a tool call (only Bash commands can be
// no-plan).
func requestClass(r *protocol.ControlRequest, extra [][]string) gateClass {
	if r == nil {
		return classGated
	}
	if readOnlyTools[r.ToolName] {
		return classRead
	}
	if r.ToolName != "Bash" {
		return classGated
	}
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(r.Input, &in) != nil {
		return classGated
	}
	return commandClass(in.Command, extra)
}

// noPlanRequest: r runs without a plan but is not a read.
func noPlanRequest(r *protocol.ControlRequest, extra [][]string) bool {
	return requestClass(r, extra) == classNoPlan
}

// commandClass: every simple command of cmd must be read or no-plan; the
// line takes the strictest class of its parts. Anything shSegments can't
// follow ($(…), subshells, here-docs, redirects into files) is gated.
func commandClass(cmd string, extra [][]string) gateClass {
	segs, ok := shSegments(cmd)
	if !ok || len(segs) == 0 {
		return classGated
	}
	c := classRead
	for _, seg := range segs {
		if c = max(c, segmentClass(seg, extra)); c == classGated {
			return c
		}
	}
	return c
}

func wordsClass(seg []shWord, extra [][]string) gateClass {
	if readOnlyWords(seg, extra) {
		return classRead
	}
	if noPlanWords(seg, extra) {
		return classNoPlan
	}
	return classGated
}

// noPlanWords: one simple command that is kmem bookkeeping under the
// session's own name, or a Zomboid launch/kill kmem-guard owns.
func noPlanWords(seg []shWord, extra [][]string) bool {
	o := gateOptsOf(extra)
	if !o.split {
		return false
	}
	envAs := ""
	for len(seg) > 0 {
		m := assignName.FindStringSubmatch(seg[0].text)
		if m == nil {
			break
		}
		v := seg[0].text[len(m[0]):]
		switch {
		case m[1] == "KMEM_SESSION" && !seg[0].unknown && o.own != "" && v == o.own:
			envAs = v
		case !safeAssign(m[1], v, !seg[0].unknown):
			return false
		}
		seg = seg[1:]
	}
	if len(seg) == 0 || seg[0].unknown {
		return false
	}
	f := make([]string, 0, len(seg))
	for _, w := range seg {
		if w.unknown {
			return false // the words the program sees are not known
		}
		f = append(f, w.text)
	}
	if privilegedWords(f) {
		return false
	}
	if name := binName(f[0]); name == "timeout" {
		if k := timeoutPayload(f[1:]); k > 0 && !assignName.MatchString(f[1+k]) {
			return noPlanWords(seg[1+k:], extra)
		}
		return false
	} else if name == "kmem" {
		return o.own != "" && kmemBookkeeping(f[1:], o.own, envAs)
	}
	return o.pzClosed && zomboidWords(f)
}

// Bookkeeping options: those kmem lists (VALOPTS, MULTI, FLAGS) that only
// describe the note, task or snapshot written. Left out: --force (takes
// others' claims), --pin, --retires (retires another fact), --transfer
// (hands claims over), --session/--pid (another session), --delete, --off.
var (
	kmemNoteValueOpts = map[string]bool{"--why": true, "--kind": true, "--replaces": true, "--prio": true, "--topic": true,
		"--after": true, "--src": true, "--valid-from": true, "--valid-to": true, "--lease": true, "--token": true, "--wip": true,
		"--lock": true, "--pointer": true, "--state": true, "--hypothesis": true, "--job": true, "--path": true,
		"--question": true, "--option": true, "--recommend": true}
	kmemNoteFlags = map[string]bool{"--json": true, "--new": true, "--same": true}
)

// kmemBookkeeping: args (after `kmem`) write the session's own notes, tasks
// or snapshot. own is the session's kmem name, envAs a KMEM_SESSION=NAME the
// command line sets (already checked to be own).
func kmemBookkeeping(args []string, own, envAs string) bool {
	as, words, ok := kmemSplit(args)
	if !ok || len(words) == 0 {
		return false
	}
	who := firstNonEmpty(as, firstNonEmpty(envAs, sessionKmemEnv()))
	if who != own {
		return false
	}
	var ops []string
	for i := 0; i < len(words); i++ {
		w := words[i]
		name, _, eq := strings.Cut(w, "=")
		switch {
		case !strings.HasPrefix(w, "-"):
			ops = append(ops, w)
		case eq && strings.HasPrefix(w, "--") && kmemNoteValueOpts[name]:
		case kmemNoteValueOpts[w]:
			if i+1 >= len(words) {
				return false
			}
			i++
		case kmemNoteFlags[w]:
		default:
			return false // --force, --pin, --retires, unknown
		}
	}
	if len(ops) == 0 {
		return false
	}
	switch ops[0] {
	case "add":
		return len(ops) >= 2
	case "task":
		if len(ops) < 2 {
			return false
		}
		switch ops[1] {
		case "add", "claim", "checkpoint", "note", "release":
			return len(ops) >= 3
		}
	case "handoff":
		return len(ops) >= 3 && ops[1] == "save"
	}
	return false
}

var timeoutDuration = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?[smhd]?$`)

// timeoutPayload: how many words of `timeout` arguments come before the
// command it runs (options and the duration), or 0 if they are not
// understood.
func timeoutPayload(args []string) int {
	i := 0
	for ; i < len(args) && strings.HasPrefix(args[i], "-"); i++ {
		a := args[i]
		switch {
		case a == "--preserve-status" || a == "--foreground" || a == "-v" || a == "--verbose":
		case a == "-s" || a == "-k" || a == "--signal" || a == "--kill-after":
			if i++; i >= len(args) {
				return 0
			}
		case strings.HasPrefix(a, "--signal=") || strings.HasPrefix(a, "--kill-after="):
		case len(a) > 2 && (strings.HasPrefix(a, "-s") || strings.HasPrefix(a, "-k")):
		default:
			return 0
		}
	}
	if i+1 >= len(args) || !timeoutDuration.MatchString(args[i]) {
		return 0
	}
	return i + 1
}

// timeoutRead: `timeout [opts] N <read-only command>`.
func timeoutRead(seg []shWord, extra [][]string) bool {
	args := make([]string, 0, len(seg))
	for _, w := range seg[1:] {
		args = append(args, w.text)
	}
	k := timeoutPayload(args)
	if k == 0 || assignName.MatchString(args[k]) {
		return false // timeout runs a program named X=1, not an assignment
	}
	for _, w := range seg[1 : 1+k] {
		if w.unknown {
			return false
		}
	}
	return readOnlyWords(seg[1+k:], extra)
}

// readOnlyPzlock: `pzlock status` and `pzlock check NAME`.
func readOnlyPzlock(args []string) bool {
	switch {
	case len(args) == 1 && args[0] == "status":
		return true
	case len(args) == 2 && args[0] == "check":
		return args[1] != "" && !strings.HasPrefix(args[1], "-")
	}
	return false
}

var (
	zomboidLaunch  = map[string]bool{"pz_launch": true, "projectzomboid.sh": true, "ProjectZomboid64": true, "ProjectZomboid32": true}
	zomboidPattern = regexp.MustCompile(`^(?i)[a-z0-9_.-]*projectzomboid[a-z0-9_.-]*$`)
	killSignals    = map[string]bool{"-9": true, "-15": true, "-2": true, "-1": true, "-KILL": true, "-TERM": true, "-INT": true,
		"-HUP": true, "-SIGKILL": true, "-SIGTERM": true, "-SIGINT": true, "-SIGHUP": true}
)

// zomboidWords: what kmem-guard's pz-lock guard checks: a Zomboid launch
// (pz_launch, projectzomboid.sh, ProjectZomboid64/32, by any path), or
// pkill/killall of one plain projectzomboid pattern (no regex alternation, so
// nothing else is killed with it).
func zomboidWords(f []string) bool {
	if f[0] != "" && zomboidLaunch[path.Base(f[0])] {
		return true
	}
	name := binName(f[0])
	if name != "pkill" && name != "killall" {
		return false
	}
	pats := 0
	for _, a := range f[1:] {
		switch {
		case killSignals[a] || name == "pkill" && (a == "-f" || a == "-e") || name == "killall" && (a == "-e" || a == "-q"):
		case strings.HasPrefix(a, "-"):
			return false
		case zomboidPattern.MatchString(a):
			pats++
		default:
			return false
		}
	}
	return pats == 1
}

// readOnlyGitMore: git subcommands the switch adds (only list and show
// forms).
func readOnlyGitMore(sub string, args []string) bool {
	for _, a := range args {
		if longOpt(a, "--output", "--ext-diff", "--textconv", "--filters", "--upload-pack", "--exec") {
			return false
		}
	}
	switch sub {
	case "rev-list", "ls-tree":
		return true
	case "branch":
		return gitListForm(args, map[string]bool{"-a": true, "-r": true, "-v": true, "-vv": true, "--all": true, "--remotes": true,
			"--show-current": true, "--no-color": true, "--color": true, "--no-column": true, "-i": true, "--ignore-case": true},
			[]string{"--sort=", "--format=", "--color=", "--column"})
	case "tag":
		return gitListForm(args, map[string]bool{"--no-column": true, "-i": true, "--ignore-case": true, "--no-color": true, "--color": true, "--omit-empty": true},
			[]string{"--sort=", "--format=", "--color=", "--column", "-n"})
	case "ls-remote":
		for _, a := range args {
			switch {
			case a == "--heads" || a == "-h" || a == "--tags" || a == "-t" || a == "--refs" || a == "--quiet" || a == "-q" ||
				a == "--symref" || a == "--exit-code" || a == "--get-url" || a == "--branches" || a == "-b" || strings.HasPrefix(a, "--sort="):
			case strings.HasPrefix(a, "-"):
				return false // -u/--upload-pack runs a program, -o sends options
			case strings.Contains(a, "::"):
				return false // transport helpers (ext::cmd runs cmd)
			}
		}
		return true
	case "cat-file":
		return len(args) == 2 && (args[0] == "-e" || args[0] == "-p" || args[0] == "-t" || args[0] == "-s") && !strings.HasPrefix(args[1], "-")
	case "worktree":
		if len(args) == 0 || args[0] != "list" {
			return false
		}
		for _, a := range args[1:] {
			if a != "--porcelain" && a != "-v" && a != "--verbose" && a != "-z" {
				return false
			}
		}
		return true
	case "config":
		return readOnlyGitConfig(args)
	}
	return false
}

// gitListForm: `git branch|tag` that only lists: no option outside flags
// (or with a prefix in prefixes), and names only with a flag that makes it a
// list (-l/--list, --contains, --merged, …). Bare `git tag` lists too.
func gitListForm(args []string, flags map[string]bool, prefixes []string) bool {
	list, names := false, 0
	listFlags := []string{"--contains", "--no-contains", "--merged", "--no-merged", "--points-at"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-l" || a == "--list":
			list = true
		case flags[a]:
		case strings.HasPrefix(a, "-") && hasAnyPrefix(a, prefixes):
			if a == "-n" || (strings.HasPrefix(a, "-n") && strings.Trim(a[2:], "0123456789") == "") {
				list = true // tag -n[N] lists with annotations
			}
		case contains(listFlags, a):
			list = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++ // its commit
			}
		case strings.HasPrefix(a, "--") && contains(listFlags, strings.SplitN(a, "=", 2)[0]):
			list = true
		case strings.HasPrefix(a, "-"):
			return false // -d, -D, -m, -a, -s, -f, -u, --set-upstream-to, …
		default:
			names++
		}
	}
	return names == 0 || list
}

func hasAnyPrefix(a string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(a, p) {
			return true
		}
	}
	return false
}

// readOnlyGitConfig: `git config --get|--get-all|--get-regexp|--list …` and
// `git config get|list …` (git 2.46+): reads only.
func readOnlyGitConfig(args []string) bool {
	scope := map[string]bool{"--global": true, "--system": true, "--local": true, "--worktree": true, "--show-origin": true,
		"--show-scope": true, "-z": true, "--null": true, "--name-only": true, "--bool": true, "--int": true, "--path": true,
		"--includes": true, "--no-includes": true}
	mode := 0
	start := 0
	if len(args) > 0 && (args[0] == "get" || args[0] == "list") {
		mode, start = 1, 1
		scope["--all"], scope["--regexp"], scope["--show-names"] = true, true, true
	}
	for _, a := range args[start:] {
		switch {
		case a == "--get" || a == "--get-all" || a == "--get-regexp" || a == "-l" || a == "--list":
			mode++
		case scope[a] || strings.HasPrefix(a, "--type=") || strings.HasPrefix(a, "--default="):
		case strings.HasPrefix(a, "-"):
			return false
		}
	}
	return mode == 1
}
