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
	"type": true, "findmnt": true, "pdfinfo": true, "read": true,
}

var argChecked = map[string]bool{
	"kmem": true, "sed": true, "find": true, "git": true, "sort": true, "tree": true, "rg": true, "file": true, "uniq": true,
	"awk": true, "journalctl": true, "date": true, "systemctl": true, "command": true, "pdftotext": true, "gh": true,
}

// ghReads: gh subcommands that only show things (without --web).
var ghReads = map[string]map[string]bool{
	"pr":      {"view": true, "list": true, "diff": true, "checks": true, "status": true},
	"issue":   {"view": true, "list": true, "status": true},
	"release": {"view": true, "list": true},
	"repo":    {"view": true},
	"run":     {"view": true, "list": true},
}

var (
	pdftotextFlags     = map[string]bool{"-layout": true, "-raw": true, "-nopgbrk": true, "-nodiag": true, "-htmlmeta": true, "-bbox": true, "-bbox-layout": true, "-tsv": true, "-cropbox": true, "-q": true, "-simple": true, "-simple2": true}
	pdftotextValueOpts = map[string]bool{"-f": true, "-l": true, "-r": true, "-x": true, "-y": true, "-W": true, "-H": true, "-fixed": true, "-enc": true, "-eol": true, "-opw": true, "-upw": true, "-colspacing": true}
)

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

// shWord: one word of a parsed shell command. alts lists every value a word
// can take when it uses a for-loop variable (`for p in a b; do x $p; done`);
// text is then alts[0] and readOnlySegment checks each value.
type shWord struct {
	text    string
	unknown bool
	plain   bool // only unquoted literal characters, so it can be a shell keyword
	alts    []string
}

func (w shWord) values() []string {
	if len(w.alts) > 0 {
		return w.alts
	}
	return []string{w.text}
}

// maxAlts caps the values one word (or one variable) can take; more counts
// as unknown.
const maxAlts = 64

// shKeywords open or close if/while/until/for bodies; shSegments drops them
// and keeps the commands they lead into (`do sed -n 1p f` → `sed -n 1p f`).
var shKeywords = map[string]bool{"if": true, "then": true, "elif": true, "else": true, "fi": true, "while": true, "until": true, "do": true, "done": true, "!": true}

// braceExpansion: an unquoted {a,b} or {1..3} that bash turns into several
// words (`git log {--output=x,-1}`), so the word counts as unknown.
var braceExpansion = regexp.MustCompile(`\{[^{}]*(,|\.\.)[^{}]*\}`)

// shLoop: an open while/until/for body. used: variables read inside it; a
// variable used there can't be set again inside it, or the next round would
// see a value the parser never checked (`for i in 1 2; do sed -n $X f; X=w; done`).
type shLoop struct {
	used      map[string]bool
	forVar    string
	before    shWord
	hadBefore bool
	cond      bool
}

// unionWord: a variable that holds either a or b (a set only conditionally).
func unionWord(a shWord, hadA bool, b shWord) shWord {
	if !hadA || a.unknown || b.unknown {
		return shWord{text: b.text, unknown: true}
	}
	var vals []string
	seen := map[string]bool{}
	for _, v := range append(append([]string{}, a.values()...), b.values()...) {
		if !seen[v] {
			seen[v] = true
			vals = append(vals, v)
		}
	}
	if len(vals) > maxAlts {
		return shWord{text: b.text, unknown: true}
	}
	if len(vals) == 1 {
		return shWord{text: vals[0]}
	}
	return shWord{text: vals[0], alts: vals}
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

// shSegments splits a shell command into simple commands, expanding the
// variables it set itself. It fails (false) on anything it can't follow:
// command substitution, subshells, here-docs, redirects to files.
func shSegments(cmd string) ([][]shWord, bool) {
	vars := map[string]shWord{}
	if home, err := os.UserHomeDir(); err == nil {
		vars["HOME"] = shWord{text: home}
	}
	var segs [][]shWord
	var seg []shWord
	cur := []string{""} // every value of the word being read
	var raw strings.Builder
	in, unknown, plain := false, false, true
	var loops []*shLoop
	compound, ifs := 0, 0
	prevSep := ";"
	add := func(s string) {
		for i := range cur {
			cur[i] += s
		}
	}
	word := func() {
		if in {
			if braceExpansion.MatchString(raw.String()) {
				unknown = true
			}
			w := shWord{text: cur[0], unknown: unknown, plain: plain}
			if len(cur) > 1 {
				w.alts = cur
			}
			seg = append(seg, w)
		}
		cur = []string{""}
		raw.Reset()
		in, unknown, plain = false, false, true
	}
	fd := func() {
		if in && len(cur) == 1 && plain && strings.Trim(cur[0], "0123456789") == "" {
			cur = []string{""}
			raw.Reset()
			in, unknown, plain = false, false, true
		}
		word()
	}
	bind := func(name string, v shWord, cond bool) bool {
		for _, l := range loops {
			if l.used[name] {
				return false
			}
		}
		if old, ok := vars[name]; cond {
			vars[name] = unionWord(old, ok, v)
		} else {
			vars[name] = v
		}
		return true
	}
	end := func(sep string) bool {
		word()
		for len(seg) > 0 && seg[0].plain && shKeywords[seg[0].text] {
			switch seg[0].text {
			case "if":
				compound++
				ifs++
			case "fi":
				if ifs == 0 {
					return false
				}
				ifs--
				compound--
			case "while", "until":
				loops = append(loops, &shLoop{used: map[string]bool{}})
				compound++
			case "done":
				if len(loops) == 0 {
					return false
				}
				l := loops[len(loops)-1]
				loops = loops[:len(loops)-1]
				compound--
				if l.forVar != "" && (l.cond || sep == "|" || sep == "&") {
					v := vars[l.forVar]
					vars[l.forVar] = unionWord(l.before, l.hadBefore, v)
				}
			}
			seg = seg[1:]
		}
		cond := compound > 0 || prevSep == "&&" || prevSep == "||" || prevSep == "|" || sep == "|" || sep == "&"
		prevSep = sep
		if len(seg) == 0 {
			return true
		}
		if seg[0].plain && seg[0].text == "for" {
			// for NAME in WORDS: the body sees exactly these words.
			if len(seg) < 4 || !seg[1].plain || shName.FindString(seg[1].text) != seg[1].text || !seg[2].plain || seg[2].text != "in" {
				return false
			}
			name := seg[1].text
			v := shWord{}
			var vals []string
			for _, w := range seg[3:] {
				if w.unknown {
					v.unknown = true
				}
				for _, x := range w.values() {
					if strings.ContainsAny(x, "*?[") {
						v.unknown = true
					}
					vals = append(vals, x)
				}
			}
			v.text = vals[0]
			if len(vals) > maxAlts {
				v.unknown = true
			} else if len(vals) > 1 {
				v.alts = vals
			}
			if v.unknown {
				v.alts = nil
			}
			l := &shLoop{used: map[string]bool{}, forVar: name, cond: cond}
			l.before, l.hadBefore = vars[name]
			if !bind(name, v, false) {
				return false
			}
			loops = append(loops, l)
			compound++
			segs = append(segs, seg)
			seg = nil
			return true
		}
		ci := 0
		for ci < len(seg) && assignName.MatchString(seg[ci].text) {
			ci++
		}
		switch {
		case ci == len(seg):
			for _, w := range seg {
				n := assignName.FindStringSubmatch(w.text)[1]
				v := shWord{text: w.text[len(n)+1:], unknown: w.unknown}
				if len(w.alts) > 1 {
					for _, a := range w.alts {
						v.alts = append(v.alts, a[len(n)+1:])
					}
				}
				if !bind(n, v, cond) {
					return false
				}
			}
		case seg[ci].text == "read":
			// read sets the variables it names (and REPLY) from its input.
			for _, w := range append(seg[ci+1:], shWord{text: "REPLY"}) {
				if w.unknown {
					return false
				}
				for _, a := range w.values() {
					if shName.FindString(a) == a && !bind(a, shWord{text: a, unknown: true}, cond) {
						return false
					}
				}
			}
		case seg[ci].text == "cd":
			for _, n := range []string{"PWD", "OLDPWD"} {
				if !bind(n, shWord{text: n, unknown: true}, cond) {
					return false
				}
			}
		}
		segs = append(segs, seg)
		seg = nil
		return true
	}
	dollar := func(i int, quoted bool) (int, bool) {
		in = true
		plain = false
		raw.WriteByte(0)
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
			if strings.ContainsRune(name, '=') {
				return i, false // ${X:=v} sets X
			}
		case shName.MatchString(rest):
			name = shName.FindString(rest)
			next = i + len(name)
		case rest != "" && strings.IndexByte("$?#@*!-0123456789", rest[0]) >= 0:
			add(cmd[i : i+2])
			unknown = true
			return i + 1, true
		default:
			add("$")
			return i, true
		}
		for _, l := range loops {
			l.used[name] = true
		}
		v, ok := vars[name]
		if !ok || shName.FindString(name) != name {
			add(cmd[i : next+1])
			unknown = true
			return next, true
		}
		if v.unknown {
			unknown = true
		}
		vals := v.values()
		for _, x := range vals {
			if !quoted && strings.ContainsAny(x, " \t\n*?[") {
				unknown = true
			}
		}
		if len(vals) == 1 {
			add(vals[0])
			return next, true
		}
		var n []string
		for _, c := range cur {
			for _, x := range vals {
				n = append(n, c+x)
			}
		}
		if len(n) > maxAlts {
			unknown = true
			n = n[:1]
		}
		cur = n
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
			add(cmd[i+1 : i+1+j])
			raw.WriteByte(0)
			in, plain = true, false
			i += j + 1
		case '"':
			in, plain = true, false
			raw.WriteByte(0)
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
					add(string(cmd[i]))
				default:
					add(string(c))
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
				add(string(cmd[i]))
				raw.WriteByte(0)
				in, plain = true, false
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
			if !end(";") {
				return nil, false
			}
		case '#':
			if in {
				add("#")
				raw.WriteByte(ch)
				continue
			}
			for i < len(cmd) && cmd[i] != '\n' {
				i++
			}
			if !end(";") {
				return nil, false
			}
		case '|':
			sep := "|"
			if strings.HasPrefix(cmd[i+1:], "|") {
				sep = "||"
				i++
			} else if strings.HasPrefix(cmd[i+1:], "&") {
				i++
			}
			if !end(sep) {
				return nil, false
			}
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
				if !end("&&") {
					return nil, false
				}
			default:
				if !end("&") {
					return nil, false
				}
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
			add(string(ch))
			raw.WriteByte(ch)
			in = true
		}
	}
	if !end(";") || len(loops) > 0 || ifs > 0 {
		return nil, false
	}
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

// readOnlySegment: one simple command from shSegments only reads. A word
// holding a for-loop variable is checked with each of its values.
func readOnlySegment(seg []shWord, extra [][]string) bool {
	if len(seg) > 0 && seg[0].plain && seg[0].text == "for" {
		return true // a loop header shSegments checked: sets the loop variable only
	}
	n := 1
	for _, w := range seg {
		if len(w.alts) > 1 {
			if n *= len(w.alts); n > 256 {
				return false
			}
		}
	}
	if n == 1 {
		return readOnlyWords(seg, extra)
	}
	for k := 0; k < n; k++ {
		one := make([]shWord, len(seg))
		r := k
		for i, w := range seg {
			one[i] = w
			if len(w.alts) > 1 {
				one[i].text, one[i].alts = w.alts[r%len(w.alts)], nil
				r /= len(w.alts)
			}
		}
		if !readOnlyWords(one, extra) {
			return false
		}
	}
	return true
}

func readOnlyWords(seg []shWord, extra [][]string) bool {
	isRead := false
	for _, w := range seg {
		if !assignName.MatchString(w.text) {
			isRead = w.text == "read"
			break
		}
	}
	for len(seg) > 0 {
		m := assignName.FindStringSubmatch(seg[0].text)
		if m == nil {
			break
		}
		// `IFS= read -r line` only changes how read splits its input.
		if !safeAssign(m[1]) && !(isRead && m[1] == "IFS") {
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
	case "printf":
		// printf -v NAME sets a variable the parser would not follow.
		return len(seg) == 1 || !seg[1].unknown && !strings.HasPrefix(args[0], "-v")
	case "command":
		return readOnlyCommandV(args)
	case "pdftotext":
		return readOnlyPdftotext(args)
	case "gh":
		return readOnlyGh(args)
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

// readOnlyExtraFor: readOnlyExtra plus `kmem --as <own name> brief|rules` for
// session s. Under another name those two mark that name's brief/rules as
// read and stay gated.
func (d *desk) readOnlyExtraFor(s *deskSession) [][]string {
	extra := d.readOnlyExtra()
	if s == nil {
		return extra
	}
	if k := d.org.Kmem[s.id]; k != "" {
		for _, sub := range []string{"brief", "rules"} {
			extra = append(extra, []string{"kmem", "--as", k, sub}, []string{"kmem", "--as=" + k, sub})
		}
	}
	return extra
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
// only mark them read for that name, so under --as they count only for the
// session's own name: desk.readOnlyExtraFor adds those two; user 2026-11-23).
func readOnlyKmem(args []string) bool {
	asName := false
	if len(args) >= 2 && args[0] == "--as" {
		args, asName = args[2:], true
	} else if len(args) >= 1 && strings.HasPrefix(args[0], "--as=") {
		args, asName = args[1:], true
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
	case "brief", "rules":
		return !asName
	case "get", "find", "show", "context", "recall", "topics", "version", "help":
		return true
	case "--version", "--help", "-h":
		return len(words) == 1
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

// readOnlyCommandV: `command -v|-V NAME…` only looks names up; any other
// `command` runs NAME.
func readOnlyCommandV(args []string) bool {
	lookup := false
	i := 0
	for ; i < len(args) && len(args[i]) > 1 && strings.HasPrefix(args[i], "-"); i++ {
		if args[i] == "--" {
			i++
			break
		}
		if strings.Trim(args[i][1:], "pvV") != "" {
			return false
		}
		lookup = lookup || strings.ContainsAny(args[i], "vV")
	}
	return lookup && i < len(args)
}

// readOnlyPdftotext: `pdftotext [opts] FILE -` writes the text to stdout;
// with any other second file it writes that file.
func readOnlyPdftotext(args []string) bool {
	var files []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case pdftotextFlags[a]:
		case pdftotextValueOpts[a]:
			if i++; i >= len(args) {
				return false
			}
		case a == "-v" || a == "-h" || a == "-help" || a == "--help":
			return len(args) == 1
		case a != "-" && strings.HasPrefix(a, "-"):
			return false
		default:
			files = append(files, a)
		}
	}
	return len(files) == 2 && files[1] == "-"
}

// readOnlyGh: gh commands that only read from GitHub: `gh api` GET requests
// and GraphQL queries, and the view/list verbs in ghReads without --web.
func readOnlyGh(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "api":
		return readOnlyGhAPI(args[1:])
	case "--version", "version":
		return len(args) == 1
	}
	if len(args) < 2 || !ghReads[args[0]][args[1]] {
		return false
	}
	for _, a := range args[2:] {
		if a == "--web" || strings.HasPrefix(a, "--web=") || strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsRune(a, 'w') {
			return false
		}
	}
	return true
}

func readOnlyGhAPI(args []string) bool {
	var endpoints, fields []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		flag, val, hasVal := a, "", false
		if strings.HasPrefix(a, "--") {
			flag, val, hasVal = strings.Cut(a, "=")
		}
		value := func() (string, bool) {
			if hasVal {
				return val, true
			}
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch flag {
		case "--paginate", "--slurp", "-i", "--include", "--silent", "--verbose":
			if hasVal {
				return false
			}
		case "-q", "--jq", "-t", "--template", "--cache", "--hostname", "-p", "--preview":
			if _, ok := value(); !ok {
				return false
			}
		case "-X", "--method":
			if v, ok := value(); !ok || !strings.EqualFold(v, "GET") {
				return false
			}
		case "-H", "--header":
			// Only Accept: another header could override the method.
			v, ok := value()
			if h, _, _ := strings.Cut(v, ":"); !ok || !strings.EqualFold(strings.TrimSpace(h), "Accept") {
				return false
			}
		case "-f", "--raw-field", "-F", "--field":
			v, ok := value()
			if !ok {
				return false
			}
			fields = append(fields, v)
		default:
			if strings.HasPrefix(a, "-") {
				return false // --input and anything unknown
			}
			endpoints = append(endpoints, a)
		}
	}
	if len(endpoints) != 1 {
		return false
	}
	if endpoints[0] != "graphql" {
		return len(fields) == 0 // fields make gh send a POST
	}
	// GraphQL: a query (never a mutation) given inline; other fields are
	// its variables.
	query := false
	for _, f := range fields {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return false
		}
		if k == "query" {
			if strings.HasPrefix(v, "@") || strings.Contains(strings.ToLower(v), "mutation") {
				return false
			}
			query = true
		}
	}
	return query
}
