package main

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Auto formatting in the message box (user 2026-11-22; Settings → Typing,
// on by default): a capital letter at the start of a sentence, " -- " → " — ",
// and Shift+Enter continues a "- " or "1. " list (an empty item ends it).
// It never touches code (inside backticks), slash commands or "--flags", and
// Backspace right after a change undoes it, like autocorrect. No typographic
// quotes: they break commands pasted into a prompt.

const prefAutoFormat = "autoFormat"

func (d *desk) autoFormatOn() bool {
	return d.app.Preferences().BoolWithFallback(prefAutoFormat, true)
}

// fmtChange is the last automatic change, for Backspace to undo.
type fmtChange struct {
	start  int    // rune offset where it begins
	before string // what the user typed
	after  string // what auto formatting made of it
}

func (e *sendEntry) formatting() bool {
	return e.autoFormat != nil && e.autoFormat() && !e.browsing
}

// inCodeSpan: the cursor is inside a `code span` or a ``` block.
func inCodeSpan(before []rune) bool {
	return strings.Count(string(before), "`")%2 == 1
}

// sentenceStart: a letter typed here begins a sentence.
func sentenceStart(before []rune) bool {
	n := len(before)
	if n == 0 {
		return true
	}
	if before[n-1] == '\n' {
		return true
	}
	if before[n-1] != ' ' {
		return false
	}
	// Skip the spaces; what is before them must end a sentence or be a
	// list marker at the start of a line.
	i := n - 1
	for i >= 0 && before[i] == ' ' {
		i--
	}
	if i < 0 {
		return true
	}
	if strings.ContainsRune(".!?", before[i]) {
		// "e.g. " and "v1.2. " end with a dot but are not sentences if the
		// word before has another dot.
		j := i - 1
		for j >= 0 && !unicode.IsSpace(before[j]) {
			if before[j] == '.' {
				return false
			}
			j--
		}
		return true
	}
	line := string(before[strings.LastIndex(string(before), "\n")+1:])
	return listMarker.MatchString(line) && strings.TrimSpace(listMarker.ReplaceAllString(line, "")) == ""
}

var listMarker = regexp.MustCompile(`^(\s*)([-*+]|(\d+)[.)])\s+`)

// formatRune may change the rune about to be typed (UI thread). It returns
// true when it inserted the text itself.
func (e *sendEntry) formatRune(r rune) bool {
	e.lastFmt = nil
	if !e.formatting() {
		return false
	}
	rs := []rune(e.Text)
	off := min(e.CursorTextOffset(), len(rs))
	before := rs[:off]
	if inCodeSpan(before) {
		return false
	}
	// A slash command's first word stays as typed.
	if strings.HasPrefix(strings.TrimSpace(string(rs)), "/") && !strings.ContainsAny(string(before), " \n") {
		return false
	}
	switch {
	case unicode.IsLower(r) && sentenceStart(before):
		up := string(localeUpper(r, acSystemLang().Tag))
		e.SetText(string(before) + up + string(rs[off:]))
		e.setCursor(off + 1)
		e.lastFmt = &fmtChange{start: off, before: string(r), after: up}
		return true
	case r == ' ' && off >= 3 && string(before[off-3:]) == " --":
		// " -- " between words is a dash; "--flag" has no space after it.
		e.SetText(string(before[:off-2]) + "— " + string(rs[off:]))
		e.setCursor(off)
		e.lastFmt = &fmtChange{start: off - 2, before: "-- ", after: "— "}
		return true
	}
	return false
}

// formatNewline continues or ends a list after Shift+Enter put a new line in.
func (e *sendEntry) formatNewline() {
	e.lastFmt = nil
	if !e.formatting() {
		return
	}
	rs := []rune(e.Text)
	off := min(e.CursorTextOffset(), len(rs))
	if off == 0 || rs[off-1] != '\n' || inCodeSpan(rs[:off]) {
		return
	}
	prevStart := strings.LastIndex(string(rs[:off-1]), "\n") + 1
	prev := []rune(string(rs[:off-1]))[len([]rune(string(rs[:off-1])[:prevStart])):]
	m := listMarker.FindStringSubmatch(string(prev))
	if m == nil {
		return
	}
	pStart := len([]rune(string(rs[:off-1])[:prevStart]))
	if strings.TrimSpace(listMarker.ReplaceAllString(string(prev), "")) == "" {
		// An empty item ends the list: drop its marker and the new line.
		e.SetText(string(rs[:pStart]) + string(rs[off:]))
		e.setCursor(pStart)
		return
	}
	marker := m[2]
	if m[3] != "" {
		n, _ := strconv.Atoi(m[3])
		marker = strconv.Itoa(n+1) + m[2][len(m[3]):]
	}
	next := m[1] + marker + " "
	e.SetText(string(rs[:off]) + next + string(rs[off:]))
	e.setCursor(off + len([]rune(next)))
	e.lastFmt = &fmtChange{start: off, before: "", after: next}
}

// undoFormat: Backspace right after an automatic change puts back what was typed.
func (e *sendEntry) undoFormat() bool {
	f := e.lastFmt
	e.lastFmt = nil
	if f == nil {
		return false
	}
	rs := []rune(e.Text)
	after := []rune(f.after)
	end := f.start + len(after)
	if end > len(rs) || e.CursorTextOffset() != end || string(rs[f.start:end]) != f.after {
		return false
	}
	e.SetText(string(rs[:f.start]) + f.before + string(rs[end:]))
	e.setCursor(f.start + len([]rune(f.before)))
	return true
}
