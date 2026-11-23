package main

import (
	"encoding/json"
	"os"
	"path"
	"regexp"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/protocol"
)

var readOnlyTools = map[string]bool{"Read": true, "Grep": true, "Glob": true, "LS": true, "ToolSearch": true, "ListAgents": true, "NotebookRead": true}

var readOnlyCommands = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "grep": true, "rg": true, "wc": true, "file": true, "stat": true,
	"sort": true, "uniq": true, "cut": true, "tr": true, "diff": true, "cmp": true, "sha256sum": true, "md5sum": true,
	"du": true, "df": true, "pwd": true, "which": true, "date": true, "echo": true, "printf": true, "true": true,
	"pgrep": true, "ps": true, "uptime": true, "free": true, "id": true, "whoami": true, "basename": true, "dirname": true,
	"realpath": true, "readlink": true, "tree": true, "jq": true, "column": true, "nl": true, "strings": true,
	"cd": true, "test": true, "[": true, "tac": true, "rev": true, "od": true, "hexdump": true, "sha1sum": true,
	"sha512sum": true, "uname": true, "nproc": true, "lscpu": true, "lsblk": true, "printenv": true, "seq": true,
	"type": true, "findmnt": true,
}

var argChecked = map[string]bool{
	"kmem": true, "sed": true, "find": true, "git": true, "sort": true, "tree": true, "rg": true, "file": true, "uniq": true,
	"awk": true, "journalctl": true, "date": true, "systemctl": true,
}

var readOnlyGit = map[string]bool{"status": true, "log": true, "diff": true, "show": true, "branch": true, "remote": true, "rev-parse": true, "ls-files": true, "blame": true, "describe": true}

var journalWrites = []string{"--vacuum", "--rotate", "--flush", "--sync", "--relinquish", "--smart-relinquish", "--setup-keys", "--update-catalog"}

var (
	shName         = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
	assignName     = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=`)
	sedPrintRange  = regexp.MustCompile(`^(\d+|\$)?(,(\d+|\$))?p$`)
	awkPrintRedir  = regexp.MustCompile(`printf?\b[^;{}]*>`)
	awkSystem      = regexp.MustCompile(`\bsystem\s*\(`)
	readOnlyBinDir = []string{"/usr/bin/", "/bin/", "/usr/local/bin/", "/usr/sbin/", "~/.local/bin/"}
)

type shWord struct {
	text    string
	unknown bool
}

func shTarget(cmd string, j int) (string, int) {
	for j < len(cmd) && (cmd[j] == ' ' || cmd[j] == '\t') {
		j++
	}
	k := j
	for k < len(cmd) && !strings.ContainsRune(" \t\n;&|<>()'\"`$\\", rune(cmd[k])) {
		k++
	}
	return cmd[j:k], k
}

func shSegments(cmd string) ([][]shWord, bool) {
	vars := map[string]shWord{}
	if home, err := os.UserHomeDir(); err == nil {
		vars["HOME"] = shWord{text: home}
	}
	var segs [][]shWord
	var seg []shWord
	var cur strings.Builder
	in, unknown := false, false
	word := func() {
		if in {
			seg = append(seg, shWord{cur.String(), unknown})
		}
		cur.Reset()
		in, unknown = false, false
	}
	fd := func() {
		if in && strings.Trim(cur.String(), "0123456789") == "" {
			cur.Reset()
			in, unknown = false, false
		}
		word()
	}
	end := func() {
		word()
		if len(seg) == 0 {
			return
		}
		all := true
		for _, w := range seg {
			all = all && assignName.MatchString(w.text)
		}
		if all {
			for _, w := range seg {
				n := assignName.FindStringSubmatch(w.text)[1]
				vars[n] = shWord{w.text[len(n)+1:], w.unknown}
			}
		}
		segs = append(segs, seg)
		seg = nil
	}
	dollar := func(i int, quoted bool) (int, bool) {
		in = true
		rest := cmd[i+1:]
		var name string
		next := i
		switch {
		case strings.HasPrefix(rest, "("):
			return i, false
		case strings.HasPrefix(rest, "{"):
			j := strings.IndexByte(rest, '}')
			if j < 0 {
				return i, false
			}
			name, next = rest[1:j], i+1+j
		case shName.MatchString(rest):
			name = shName.FindString(rest)
			next = i + len(name)
		case rest != "" && strings.IndexByte("$?#@*!-0123456789", rest[0]) >= 0:
			cur.WriteString(cmd[i : i+2])
			unknown = true
			return i + 1, true
		default:
			cur.WriteByte('$')
			return i, true
		}
		v, ok := vars[name]
		if !ok || shName.FindString(name) != name {
			cur.WriteString(cmd[i : next+1])
			unknown = true
			return next, true
		}
		cur.WriteString(v.text)
		if v.unknown || !quoted && strings.ContainsAny(v.text, " \t\n*?[") {
			unknown = true
		}
		return next, true
	}
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		var ok bool
		switch ch {
		case '\'':
			j := strings.IndexByte(cmd[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			cur.WriteString(cmd[i+1 : i+1+j])
			in = true
			i += j + 1
		case '"':
			in = true
			for i++; i < len(cmd) && cmd[i] != '"'; i++ {
				c := cmd[i]
				switch {
				case c == '`':
					return nil, false
				case c == '$':
					if i, ok = dollar(i, true); !ok {
						return nil, false
					}
				case c == '\\' && i+1 < len(cmd) && strings.IndexByte("$`\"\\\n", cmd[i+1]) >= 0:
					i++
					cur.WriteByte(cmd[i])
				default:
					cur.WriteByte(c)
				}
			}
			if i >= len(cmd) {
				return nil, false
			}
		case '\\':
			if i+1 >= len(cmd) {
				return nil, false
			}
			i++
			if cmd[i] != '\n' {
				cur.WriteByte(cmd[i])
				in = true
			}
		case '$':
			if i, ok = dollar(i, false); !ok {
				return nil, false
			}
		case '`', '(', ')':
			return nil, false
		case ' ', '\t':
			word()
		case ';', '\n':
			end()
		case '#':
			if in {
				cur.WriteByte(ch)
				continue
			}
			for i < len(cmd) && cmd[i] != '\n' {
				i++
			}
			end()
		case '|':
			if strings.HasPrefix(cmd[i+1:], "|") || strings.HasPrefix(cmd[i+1:], "&") {
				i++
			}
			end()
		case '&':
			switch {
			case strings.HasPrefix(cmd[i+1:], ">"):
				word()
				i++
				if strings.HasPrefix(cmd[i+1:], ">") {
					i++
				}
				t, n := shTarget(cmd, i+1)
				if t != "/dev/null" {
					return nil, false
				}
				i = n - 1
			case strings.HasPrefix(cmd[i+1:], "&"):
				i++
				end()
			default:
				end()
			}
		case '>':
			fd()
			rest := cmd[i+1:]
			switch {
			case strings.HasPrefix(rest, "&"):
				t, n := shTarget(cmd, i+2)
				if t != "1" && t != "2" && t != "-" {
					return nil, false
				}
				i = n - 1
			case strings.HasPrefix(rest, "("):
				return nil, false
			default:
				j := i + 1
				if strings.HasPrefix(rest, ">") || strings.HasPrefix(rest, "|") {
					j++
				}
				t, n := shTarget(cmd, j)
				if t != "/dev/null" {
					return nil, false
				}
				i = n - 1
			}
		case '<':
			fd()
			if rest := cmd[i+1:]; rest != "" && strings.IndexByte("<(&>", rest[0]) >= 0 {
				return nil, false
			}
			t, n := shTarget(cmd, i+1)
			if t == "" {
				return nil, false
			}
			i = n - 1
		default:
			cur.WriteByte(ch)
			in = true
		}
	}
	end()
	return segs, true
}

func binName(w string) string {
	if !strings.Contains(w, "/") {
		return w
	}
	dir, base := path.Split(w)
	for _, d := range readOnlyBinDir {
		if dir == d {
			return base
		}
	}
	if home, err := os.UserHomeDir(); err == nil && dir == home+"/.local/bin/" {
		return base
	}
	return ""
}

func safeAssign(name string) bool {
	switch name {
	case "PATH", "IFS", "ENV", "BASH_ENV", "CDPATH", "PROMPT_COMMAND", "SHELLOPTS", "BASHOPTS", "PS4", "GLOBIGNORE", "HOME", "SHELL":
		return false
	}
	for _, p := range []string{"LD_", "GIT_", "BASH_FUNC", "LESS"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return !strings.HasSuffix(name, "PAGER") && !strings.HasSuffix(name, "EDITOR")
}

func readOnlySed(args []string) bool {
	quiet := false
	script := 0
	for _, a := range args {
		switch {
		case a == "-n" || a == "--quiet" || a == "--silent":
			quiet = true
		case strings.HasPrefix(a, "-"):
			return false
		case script == 0:
			script++
			if !sedPrintRange.MatchString(a) {
				return false
			}
		}
	}
	return quiet && script == 1
}

func readOnlyGitArgs(sub string, args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "--output") || strings.HasPrefix(a, "--ext-diff") {
			return false
		}
	}
	switch sub {
	case "branch":
		for _, a := range args {
			switch a {
			case "-a", "-r", "-v", "-vv", "--list", "--show-current", "--all", "--remotes":
			default:
				return false
			}
		}
	case "remote":
		// "git remote get-url [--push] [--all] NAME" only prints a URL.
		if len(args) > 0 && args[0] == "get-url" {
			names := 0
			for _, a := range args[1:] {
				switch {
				case a == "--push" || a == "--all":
				case strings.HasPrefix(a, "-"):
					return false
				default:
					names++
				}
			}
			return names == 1
		}
		for _, a := range args {
			if a != "-v" && a != "--verbose" {
				return false
			}
		}
	}
	return true
}

func readOnlyRequest(r *protocol.ControlRequest, extra [][]string) bool {
	if r == nil {
		return false
	}
	if readOnlyTools[r.ToolName] {
		return true
	}
	if r.ToolName != "Bash" {
		return false
	}
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(r.Input, &in) != nil {
		return false
	}
	return readOnlyCommand(in.Command, extra)
}

func readOnlyCommand(cmd string, extra [][]string) bool {
	segs, ok := shSegments(cmd)
	if !ok || len(segs) == 0 {
		return false
	}
	for _, seg := range segs {
		if !readOnlySegment(seg, extra) {
			return false
		}
	}
	return true
}

func hasWordPrefix(f, prefix []string) bool {
	if len(prefix) == 0 || len(prefix) > len(f) {
		return false
	}
	for i, w := range prefix {
		if f[i] != w {
			return false
		}
	}
	return true
}

func readOnlySegment(seg []shWord, extra [][]string) bool {
	for len(seg) > 0 {
		m := assignName.FindStringSubmatch(seg[0].text)
		if m == nil {
			break
		}
		if !safeAssign(m[1]) {
			return false
		}
		seg = seg[1:]
	}
	if len(seg) == 0 {
		return true
	}
	name := binName(seg[0].text)
	if seg[0].unknown || name == "" {
		return false
	}
	f := []string{name}
	for _, w := range seg[1:] {
		f = append(f, w.text)
	}
	// Root never counts as read-only, whatever the user's list says
	// (ta43580; gatebatch.go).
	if privilegedWords(append([]string{seg[0].text}, f[1:]...)) {
		return false
	}
	for _, e := range extra {
		if hasWordPrefix(f, e) {
			return true
		}
	}
	if argChecked[name] {
		for _, w := range seg[1:] {
			if w.unknown {
				return false
			}
		}
	}
	args := f[1:]
	switch name {
	case "sed":
		return readOnlySed(args)
	case "find":
		for _, a := range args {
			if a == "-delete" || strings.HasPrefix(a, "-exec") || strings.HasPrefix(a, "-ok") || strings.HasPrefix(a, "-fprint") || a == "-fls" {
				return false
			}
		}
	case "kmem":
		return readOnlyKmem(args)
	case "git":
		for len(args) > 0 && strings.HasPrefix(args[0], "-") {
			switch {
			case args[0] == "-C" && len(args) > 1:
				args = args[2:]
			case args[0] == "--no-pager" || args[0] == "-P":
				args = args[1:]
			default:
				return false
			}
		}
		return len(args) > 0 && readOnlyGit[args[0]] && readOnlyGitArgs(args[0], args[1:])
	case "sort", "tree":
		for _, a := range args {
			if strings.HasPrefix(a, "--output") || strings.HasPrefix(a, "--compress-program") {
				return false
			}
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsRune(a, 'o') {
				return false
			}
		}
	case "rg":
		for _, a := range args {
			if strings.HasPrefix(a, "--pre") || strings.HasPrefix(a, "--search-zip") || a == "-z" {
				return false
			}
		}
	case "file":
		for _, a := range args {
			if a == "-C" || a == "--compile" {
				return false
			}
		}
	case "uniq":
		files := 0
		for _, a := range args {
			if a == "-" || !strings.HasPrefix(a, "-") {
				files++
			}
		}
		return files <= 1
	case "awk":
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case a == "-F" || a == "-v":
				i++
			case strings.HasPrefix(a, "-F") || strings.HasPrefix(a, "-v"):
			case strings.HasPrefix(a, "-"):
				return false
			case awkSystem.MatchString(a) || strings.Contains(a, "|") || awkPrintRedir.MatchString(a) || strings.Contains(a, "@load") || strings.Contains(a, "@include") || strings.Contains(a, "inplace"):
				return false
			}
		}
	case "journalctl":
		for _, a := range args {
			for _, w := range journalWrites {
				if strings.HasPrefix(a, w) {
					return false
				}
			}
		}
	case "systemctl":
		// Status and listing verbs only (systemctlReads), user or system
		// units; privilegedWords already refused every other system verb.
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				return systemctlReads[a]
			}
		}
		return false
	case "date":
		for _, a := range args {
			if strings.HasPrefix(a, "--set") || strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "s") {
				return false
			}
		}
	default:
		return readOnlyCommands[name]
	}
	return true
}

func parseReadOnlyExtra(text string) [][]string {
	var out [][]string
	for _, line := range strings.Split(text, "\n") {
		segs, ok := shSegments(strings.TrimSpace(line))
		if !ok || len(segs) != 1 {
			continue
		}
		f := []string{binName(segs[0][0].text)}
		if f[0] == "" {
			continue
		}
		for _, w := range segs[0][1:] {
			f = append(f, w.text)
		}
		out = append(out, f)
	}
	return out
}

const builtinReadOnly = "kmem brief\nkmem get\nkmem find\nkmem show\nkmem context\nkmem recall\nkmem rules\nkmem task list\nkmem task check\nkmem handoff get\nkmem handoff list\nkmem handoff policy"

func (d *desk) readOnlyExtra() [][]string {
	return parseReadOnlyExtra(builtinReadOnly + "\n" + d.app.Preferences().String("extraReadOnly"))
}

func (d *desk) readOnlyDialog() {
	e := widget.NewMultiLineEntry()
	e.SetText(d.app.Preferences().String("extraReadOnly"))
	e.SetPlaceHolder("pzlock status")
	e.SetMinRowsVisible(6)
	explain := widget.NewLabel("With Plan first on, commands that only read run without a plan round. " +
		"Add your own here, one per line: a command that starts with one of these lines counts as read-only " +
		"(\"kmem get\" also lets \"kmem get notes\" through). Only add commands that never change anything.")
	explain.Wrapping = fyne.TextWrapWord
	explain.SizeName = theme.SizeNameCaptionText
	dlg := dialog.NewCustomConfirm("Read-only commands", "Save", "Cancel", container.NewBorder(nil, explain, nil, nil, e), func(ok bool) {
		if ok {
			d.app.Preferences().SetString("extraReadOnly", strings.TrimSpace(e.Text))
		}
	}, d.win)
	dlg.Resize(fyne.NewSize(560, 380))
	dlg.Show()
}

// readOnlyKmem: kmem commands that only read (Kmem Lock Audit, 2026-11-22:
// `kmem brief`, `get`, `find`, `show` needed a plan). "kmem fixed TOPIC" sets
// a mark (kmem 1.7.0), so only the bare "kmem fixed" list counts; "kmem
// rules" only marks the rules read. "--as NAME" in front counts the same
// (user 2026-11-23: `kmem --as zomboid find …` needed a plan; brief and rules
// only mark them read for that name).
func readOnlyKmem(args []string) bool {
	if len(args) >= 2 && args[0] == "--as" {
		args = args[2:]
	} else if len(args) >= 1 && strings.HasPrefix(args[0], "--as=") {
		args = args[1:]
	}
	if len(args) == 0 {
		return false
	}
	var words []string
	for _, a := range args {
		if a != "--json" {
			words = append(words, a)
		}
	}
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "brief", "get", "find", "show", "context", "recall", "topics", "version", "help", "rules":
		return true
	case "fixed":
		return len(words) == 1
	case "task":
		return len(words) >= 2 && words[1] == "list"
	case "handoff":
		return len(words) >= 2 && (words[1] == "get" || words[1] == "list" || words[1] == "resolve" || words[1] == "policy" && len(words) == 2)
	case "trash":
		return len(words) >= 2 && words[1] == "list"
	}
	return false
}
