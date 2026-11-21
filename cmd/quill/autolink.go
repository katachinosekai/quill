package main

import (
	"regexp"
	"strings"
)

var bareURL = regexp.MustCompile("(?:https?|file)://[^\\s<>()\\[\\]`\"']+")

func autolinks(text string) string {
	if !strings.Contains(text, "://") {
		return text
	}
	lines := strings.Split(text, "\n")
	fence := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fence = !fence
			continue
		}
		if fence || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			continue
		}
		parts := strings.Split(line, "`")
		for j := 0; j < len(parts); j += 2 {
			parts[j] = linkProse(parts[j])
		}
		lines[i] = strings.Join(parts, "`")
	}
	return strings.Join(lines, "\n")
}

func linkProse(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range bareURL.FindAllStringIndex(s, -1) {
		start, end := m[0], m[1]
		url := strings.TrimRight(s[start:end], ".,;:!?*_")
		end = start + len(url)
		if start >= 2 && s[start-2:start] == "](" || start > 0 && s[start-1] == '<' || strings.HasPrefix(s[end:], "](") {
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString("[" + url + "](" + url + ")")
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}
