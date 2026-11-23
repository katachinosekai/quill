package main

import "strings"

// Formatted reveal (user 2026-11-23): while a reply is revealed, Markdown
// markers (** * ` # [text](url) - 1. and ``` fences) are never drawn, and
// they take no reveal time: the reveal position counts the characters that
// show. scanMD finds the markers of a text. streamMarkdown cuts the markers
// at the end of the revealed text (the ones not yet followed by a visible
// character) and closes what is still open, so bold, italics, code, links
// and headings show formatted from their first visible letter; a link shows
// with a placeholder address until its next character arrives.

// mdOpen: what is open (not yet closed) after some text.
type mdOpen struct {
	stk   [8]byte // 'b' bold, 'i' italic, 'c' code, 'l' link text, oldest first
	n     int
	fence bool
}

func (o *mdOpen) has(k byte) bool {
	for _, c := range o.stk[:o.n] {
		if c == k {
			return true
		}
	}
	return false
}

func (o *mdOpen) push(k byte) {
	if o.n < len(o.stk) {
		o.stk[o.n] = k
		o.n++
	}
}

func (o *mdOpen) drop(k byte) {
	for i := o.n - 1; i >= 0; i-- {
		if o.stk[i] == k {
			copy(o.stk[i:], o.stk[i+1:o.n])
			o.n--
			return
		}
	}
}

func mdSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// scanMD classifies the bytes of text: marks[i] says byte i is a marker (a
// nil marks is not filled in). cut is the end of the last byte that shows
// and open what is open there.
func scanMD(text string, marks []bool) (cut int, open mdOpen) {
	var cur mdOpen
	mark := func(from, to int) {
		if marks != nil {
			for k := from; k < to; k++ {
				marks[k] = true
			}
		}
	}
	shows := func(to int) { cut, open = to, cur }
	for i := 0; i < len(text); {
		end, complete := len(text), false
		if e := strings.IndexByte(text[i:], '\n'); e >= 0 {
			end, complete = i+e+1, true
		}
		body := strings.TrimRight(text[i:end], "\r\n")
		trim := strings.TrimLeft(body, " \t")
		indent := len(body) - len(trim)
		content := end // where the line's text ends (before its newline)
		if complete {
			content = i + len(strings.TrimRight(text[i:end], "\r\n"))
		}
		switch {
		case cur.fence:
			if strings.HasPrefix(trim, "```") {
				mark(i, end)
				cur.fence = false
			} else if !complete && trim != "" && strings.Trim(trim, "`") == "" {
				mark(i, end) // the start of a closing fence
			} else {
				shows(end)
			}
		case strings.HasPrefix(trim, "```"):
			mark(i, end)
			cur = mdOpen{fence: true}
		default:
			cur = mdOpen{}
			j := i + indent
			m := lineMarker(text[j:content], complete)
			if m > 0 {
				mark(j, j+m)
				if j > i {
					shows(j)
				}
			} else if j > i {
				shows(j)
			}
			j += m
			for j < content {
				j = inlineMD(text, j, content, &cur, mark, shows)
			}
			if complete {
				cur = mdOpen{}
				shows(end)
			}
		}
		i = end
	}
	return cut, open
}

// lineMarker: the length of the block marker a line starts with (heading,
// quote, list item, rule), 0 for none; a marker still being typed counts.
func lineMarker(line string, complete bool) int {
	if line == "" {
		return 0
	}
	spaces := func(n int) int {
		for n < len(line) && (line[n] == ' ' || line[n] == '\t') {
			n++
		}
		return n
	}
	n := 0
	switch c := line[0]; {
	case c == '#':
		for n < len(line) && line[n] == '#' {
			n++
		}
		if n > 6 || n < len(line) && line[n] != ' ' && line[n] != '\t' {
			return 0
		}
		return spaces(n)
	case c == '>':
		return spaces(1)
	case c == '-' || c == '+' || c == '*':
		if strings.Trim(line, string(c)) == "" && len(line) >= 3 && c != '+' {
			return len(line) // a rule
		}
		if len(line) == 1 && c != '*' && !complete {
			return 1 // typed so far: "-"
		}
		if len(line) > 1 && (line[1] == ' ' || line[1] == '\t') {
			return spaces(1)
		}
	case c >= '0' && c <= '9':
		for n < len(line) && line[n] >= '0' && line[n] <= '9' {
			n++
		}
		if n >= 10 {
			return 0
		}
		if n == len(line) {
			return n // digits so far: maybe "1."
		}
		if line[n] != '.' && line[n] != ')' {
			return 0
		}
		n++
		if n == len(line) {
			return n
		}
		if line[n] == ' ' || line[n] == '\t' {
			return spaces(n)
		}
	}
	return 0
}

// inlineMD handles the byte at j of a line (to content): returns the next.
func inlineMD(text string, j, content int, cur *mdOpen, mark func(int, int), shows func(int)) int {
	c := text[j]
	if c == '\\' {
		if j+1 < content && strings.IndexByte("\\`*_{}[]()#+-.!>~|", text[j+1]) >= 0 {
			mark(j, j+1)
			shows(j + 2)
			return j + 2
		}
		if j+1 >= len(text) {
			mark(j, j+1) // an escape in the making
			return j + 1
		}
	}
	if cur.has('c') {
		if c == '`' {
			mark(j, j+1)
			cur.drop('c')
			return j + 1
		}
		shows(j + 1)
		return j + 1
	}
	switch c {
	case '`':
		k := j
		for k < content && text[k] == '`' {
			k++
		}
		mark(j, k)
		cur.push('c')
		return k
	case '*':
		l := 1
		if j+1 < content && text[j+1] == '*' {
			l = 2
		}
		if j+l >= len(text) {
			mark(j, j+l) // "*" now, maybe "**" next; or "**" about to open
			return j + l
		}
		kind := byte('i')
		if l == 2 {
			kind = 'b'
		}
		afterSpace := mdSpace(text[j+l])
		beforeSpace := j == 0 || mdSpace(text[j-1])
		switch {
		case cur.has(kind) && !beforeSpace:
			mark(j, j+l)
			cur.drop(kind)
			return j + l
		case !cur.has(kind) && !afterSpace:
			mark(j, j+l)
			cur.push(kind)
			return j + l
		}
		shows(j + l)
		return j + l
	case '[':
		if !cur.has('l') {
			mark(j, j+1)
			cur.push('l')
			return j + 1
		}
	case ']':
		if cur.has('l') {
			switch {
			case j+1 >= len(text):
				mark(j, j+1) // "]" now, maybe "](" next
				return j + 1
			case j+1 < content && text[j+1] == '(':
				k := j + 2
				for k < content && text[k] != ')' {
					k++
				}
				if k < content {
					k++ // the closing ")"
				}
				mark(j, k)
				cur.drop('l')
				return k
			}
			cur.drop('l') // brackets of its own
		}
	}
	shows(j + 1)
	return j + 1
}

// streamMarkdown: the revealed paragraph s ready to draw (see above).
func streamMarkdown(s string) string {
	cut, open := scanMD(s, nil)
	out := s[:cut]
	if open.fence {
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		return out + "```"
	}
	if open.n == 0 {
		return out
	}
	out = strings.TrimRight(out, " \t\r\n")
	for i := open.n - 1; i >= 0; i-- {
		switch open.stk[i] {
		case 'b':
			out += "**"
		case 'i':
			out += "*"
		case 'c':
			out += "`"
		case 'l':
			out += "](#)"
		}
	}
	return out
}
