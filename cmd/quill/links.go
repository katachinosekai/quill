package main

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The shared link rules (docs/design/phone-links.md §3): one rule set for
// Quill Desktop (this file) and Quill Android (Links.java), checked by the
// same cases in testdata/links.json. Change the rules here, in the case file
// and in docs/phone-links-cases.sha256 together.

// Link kinds.
const (
	linkWeb   = "web"   // http/https: the browser
	linkFile  = "file"  // file:// URL, Target is the decoded absolute path
	linkPath  = "path"  // absolute or ~/ path, Target as written (no line hint)
	linkOther = "other" // another scheme, only as a Markdown link target
)

// linksMax: at most this many links per item; the rest stay plain text.
const linksMax = 50

// textLink is one link found in a text. Start and End are byte offsets of the
// whole tappable span (a Markdown link's `[label](target)`, a path with its
// line hint, the inside of an inline code span); End is exclusive.
type textLink struct {
	Kind       string
	Start, End int
	Target     string
	Label      string // Markdown link label
	Line       int    // `:N` hint (paths), 0 = none
	LineEnd    int    // `:N-M` hint, 0 = none
}

var (
	mdLinkRe   = regexp.MustCompile(`\[([^\[\]\n]*)\]\(([^()\s]+)\)`)
	angleRe    = regexp.MustCompile(`<((?:https?|file)://[^\s<>]+)>`)
	bareLinkRe = regexp.MustCompile("(?:https?|file)://[^\\s<>()\\[\\]`\"']+")
	schemeRe   = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.\-]*):`)
	lineHintRe = regexp.MustCompile(`:([0-9]+)(?:([-:])([0-9]+))?$`)
)

// findLinks returns the links in text, in order, at most linksMax.
func findLinks(text string) []textLink {
	var out []textLink
	off := 0
	fence := ""
	for _, line := range strings.SplitAfter(text, "\n") {
		start := off
		off += len(line)
		body := strings.TrimSuffix(line, "\n")
		trim := strings.TrimSpace(body)
		if fence != "" {
			if strings.HasPrefix(trim, fence) {
				fence = ""
			}
			continue
		}
		if f := fenceMark(trim); f != "" {
			// A fence opened and closed on the same line is skipped alone.
			if !strings.Contains(trim[3:], f) {
				fence = f
			}
			continue
		}
		if strings.HasPrefix(body, "    ") || strings.HasPrefix(body, "\t") {
			continue
		}
		out = append(out, lineLinks(body, start)...)
		if len(out) >= linksMax {
			return out[:linksMax]
		}
	}
	return out
}

func fenceMark(trim string) string {
	for _, f := range []string{"```", "~~~"} {
		if strings.HasPrefix(trim, f) {
			return f
		}
	}
	return ""
}

// lineLinks: prose parts (between inline code spans) get every rule; an
// inline code span is a path link only when it is one path as a whole.
func lineLinks(line string, base int) []textLink {
	var out []textLink
	pos := 0
	parts := strings.Split(line, "`")
	for i, part := range parts {
		if i%2 == 0 {
			out = append(out, proseLinks(part, base+pos)...)
		} else if i < len(parts)-1 { // an unclosed span stays code
			if l, ok := codePath(part, base+pos); ok {
				out = append(out, l)
			}
		}
		pos += len(part) + 1
	}
	return out
}

func codePath(code string, base int) (textLink, bool) {
	t := strings.TrimSpace(code)
	if t == "" {
		return textLink{}, false
	}
	lead := strings.Index(code, t)
	end, ok := pathEnd(t, 0)
	if !ok || end != len(t) {
		return textLink{}, false
	}
	l, ok := pathLink(t, base+lead)
	return l, ok
}

type span struct{ a, b int }

func overlaps(spans []span, a, b int) bool {
	for _, s := range spans {
		if a < s.b && s.a < b {
			return true
		}
	}
	return false
}

func proseLinks(s string, base int) []textLink {
	var out []textLink
	var taken []span
	add := func(l textLink, a, b int) {
		taken = append(taken, span{a, b})
		if l.Kind != "" {
			l.Start, l.End = base+a, base+b
			out = append(out, l)
		}
	}
	for _, m := range mdLinkRe.FindAllStringSubmatchIndex(s, -1) {
		l := classifyTarget(s[m[4]:m[5]])
		l.Label = s[m[2]:m[3]]
		add(l, m[0], m[1])
	}
	for _, m := range angleRe.FindAllStringSubmatchIndex(s, -1) {
		if overlaps(taken, m[0], m[1]) {
			continue
		}
		add(classifyTarget(s[m[2]:m[3]]), m[0], m[1])
	}
	for _, m := range bareLinkRe.FindAllStringIndex(s, -1) {
		raw := strings.TrimRight(s[m[0]:m[1]], ".,;:!?*_")
		a, b := m[0], m[0]+len(raw)
		if overlaps(taken, a, b) {
			continue
		}
		l := classifyTarget(raw)
		if l.Kind != linkWeb && l.Kind != linkFile {
			l.Kind = ""
		}
		add(l, a, b)
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '/' && !(s[i] == '~' && i+1 < len(s) && s[i+1] == '/') {
			continue
		}
		if i > 0 && pathBlocked(s[i-1]) || overlaps(taken, i, i+1) {
			continue
		}
		end, ok := pathEnd(s, i)
		if !ok {
			continue
		}
		raw := strings.TrimRight(s[i:end], ".,;:!?")
		if l, ok := pathLink(raw, 0); ok && !overlaps(taken, i, i+len(raw)) {
			add(l, i, i+len(raw))
		}
		i = end - 1
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// pathBlocked: a path can't start right after a letter, digit or one of
// these (`and/or`, `1/2`, URL paths, `../x`).
func pathBlocked(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80 || strings.IndexByte("/:.~-\\@#%+&$", c) >= 0
}

// pathEnd: the end of the run of path characters starting at i.
func pathEnd(s string, i int) (int, bool) {
	j := i
	if s[j] == '~' {
		j++
	}
	if j >= len(s) || s[j] != '/' {
		return 0, false
	}
	for j < len(s) && !pathStop(s[j]) {
		j++
	}
	return j, true
}

func pathStop(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v' || strings.IndexByte("<>\"'`()[]*", c) >= 0
}

// pathLink checks raw (path chars only, trailing punctuation already
// dropped) and splits off the line hint.
func pathLink(raw string, start int) (textLink, bool) {
	l := textLink{Kind: linkPath, Start: start, End: start + len(raw)}
	p := raw
	if m := lineHintRe.FindStringSubmatchIndex(p); m != nil {
		l.Line, _ = strconv.Atoi(p[m[2]:m[3]])
		if m[4] >= 0 && p[m[4]:m[5]] == "-" {
			l.LineEnd, _ = strconv.Atoi(p[m[6]:m[7]])
		}
		p = p[:m[0]]
	}
	if !validPath(p) {
		return textLink{}, false
	}
	l.Target = p
	return l, true
}

// validPath: `/a/b...` (two segments at least, so `/clear` and `/2` aren't
// paths) or `~/a...`; never `//`.
func validPath(p string) bool {
	rest, min := "", 2
	switch {
	case strings.HasPrefix(p, "~/"):
		rest, min = p[2:], 1
	case strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//"):
		rest = p[1:]
	default:
		return false
	}
	n := 0
	for _, seg := range strings.Split(rest, "/") {
		if seg != "" {
			n++
		}
	}
	return n >= min
}

// classifyTarget: a Markdown link target, an <autolink> or a bare URL.
// Kind "" = not a link (relative targets, quillpair:, bad file URLs).
func classifyTarget(t string) textLink {
	lower := strings.ToLower(t)
	switch {
	case strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://"):
		if len(t) > len("https://") || !strings.HasSuffix(lower, "://") {
			return textLink{Kind: linkWeb, Target: t}
		}
		return textLink{}
	case strings.HasPrefix(lower, "file://"):
		if p, ok := fileURLPath(t); ok {
			return textLink{Kind: linkFile, Target: p}
		}
		return textLink{}
	case strings.HasPrefix(t, "/") || strings.HasPrefix(t, "~/"):
		l, ok := pathLink(t, 0)
		if !ok {
			return textLink{}
		}
		return textLink{Kind: linkPath, Target: l.Target, Line: l.Line, LineEnd: l.LineEnd}
	}
	if m := schemeRe.FindStringSubmatch(t); m != nil && strings.ToLower(m[1]) != "quillpair" {
		return textLink{Kind: linkOther, Target: t}
	}
	return textLink{}
}

// fileURLPath: file:///abs or file://localhost/abs, percent-decoded.
func fileURLPath(t string) (string, bool) {
	rest := t[len("file://"):]
	if strings.HasPrefix(strings.ToLower(rest), "localhost/") {
		rest = rest[len("localhost"):]
	}
	if !strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, "//") {
		return "", false
	}
	p, err := url.PathUnescape(rest)
	if err != nil || strings.ContainsRune(p, 0) || strings.Trim(p, "/") == "" {
		return "", false
	}
	return p, true
}
