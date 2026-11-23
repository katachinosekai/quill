package chat

import (
	"regexp"
	"strings"
)

var (
	// "Sources:", "**Sources:**", "## References", "Links" (case-insensitive).
	sourcesHead = regexp.MustCompile(`(?i)^[#>\s]*[*_]*\s*(?:sources?|references?|links?|citations?)\s*:?\s*[*_]*\s*:?$`)
	// A list item (dash, bullet or number) that holds only links.
	linkOnlyItem = regexp.MustCompile(`^(?:[-*+•]|\d+[.)])?\s*(?:\[[^\]]*\]\([^)\s]*\)|<?https?://\S+>?)(?:\s*[,;]?\s*(?:\[[^\]]*\]\([^)\s]*\)|<?https?://\S+>?))*\s*$`)
)

// SplitSources splits a trailing sources/links block off text (web-search
// replies always end with one): blank lines, a "Sources:"-style heading and
// list items that are only links. The question a reply closes with sits just
// above it. body is text without the block, right-trimmed; src is the block
// (empty, with body == text, when there is none or text is nothing but it).
func SplitSources(text string) (body, src string) {
	trimmed := strings.TrimRight(text, " \t\r\n")
	lines := strings.Split(trimmed, "\n")
	i := len(lines)
	for i > 0 {
		t := strings.TrimSpace(lines[i-1])
		if t != "" && !sourcesHead.MatchString(t) && !linkOnlyItem.MatchString(t) {
			break
		}
		i--
	}
	if i == len(lines) || i == 0 {
		return trimmed, ""
	}
	body = strings.TrimRight(strings.Join(lines[:i], "\n"), " \t\r\n")
	if body == "" {
		return trimmed, ""
	}
	return body, strings.Trim(strings.Join(lines[i:], "\n"), "\n")
}
