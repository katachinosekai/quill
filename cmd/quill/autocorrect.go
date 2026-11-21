package main

import (
	"bufio"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/katachinosekai/quill/internal/chat"
)

const (
	acLang    = "en_US"
	acTimeout = 300 * time.Millisecond
	acHintFor = 4 * time.Second
)

type speller interface {
	check(word string) (bool, []string)
}

type hunspell struct {
	mu   sync.Mutex
	in   io.WriteCloser
	out  *bufio.Reader
	dead bool
}

func hunspellAvailable() bool {
	_, err := exec.LookPath("hunspell")
	return err == nil
}

func startHunspell(lang string) (*hunspell, error) {
	cmd := exec.Command("hunspell", "-a", "-d", lang)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	h := &hunspell{in: in, out: bufio.NewReader(out)}
	if _, err := h.out.ReadString('\n'); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *hunspell) check(word string) (bool, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.dead {
		return true, nil
	}
	type reply struct {
		ok   bool
		sugg []string
		err  error
	}
	ch := make(chan reply, 1)
	go func() {
		if _, err := io.WriteString(h.in, "^"+word+"\n"); err != nil {
			ch <- reply{err: err}
			return
		}
		r := reply{ok: true}
		for {
			line, err := h.out.ReadString('\n')
			if err != nil {
				ch <- reply{err: err}
				return
			}
			line = strings.TrimRight(line, "\n")
			if line == "" {
				ch <- r
				return
			}
			switch line[0] {
			case '&':
				r.ok = false
				if i := strings.Index(line, ": "); i >= 0 {
					for _, s := range strings.Split(line[i+2:], ", ") {
						r.sugg = append(r.sugg, strings.TrimSpace(s))
					}
				}
			case '#':
				r.ok = false
			}
		}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			h.dead = true
			return true, nil
		}
		return r.ok, r.sugg
	case <-time.After(acTimeout):
		h.dead = true
		return true, nil
	}
}

type autocorrector struct {
	sp      speller
	start   func() speller
	enabled func() bool
	ignored func(string) bool
	ignore  func(string)
}

func (a *autocorrector) speller() speller {
	if a.sp == nil && a.start != nil {
		a.sp = a.start()
		a.start = nil
	}
	return a.sp
}

func isACBoundary(r rune) bool {
	return r == ' ' || r == '\n' || strings.ContainsRune(".,;:!?)", r)
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || r == '\''
}

func plainWord(w string) bool {
	rs := []rune(w)
	if len(rs) < 3 || rs[0] == '\'' {
		return false
	}
	for i, r := range rs {
		if !isWordRune(r) {
			return false
		}
		if i > 0 && unicode.IsUpper(r) {
			return false
		}
	}
	return true
}

func inCode(before string) bool {
	fences := strings.Count(before, "```")
	if fences%2 == 1 {
		return true
	}
	return (strings.Count(before, "`")-3*fences)%2 == 1
}

func (a *autocorrector) fix(word, token, before string, known func(string) bool) (string, bool) {
	if a == nil || a.enabled != nil && !a.enabled() || !plainWord(word) || token != word || inCode(before) {
		return "", false
	}
	if unicode.IsUpper([]rune(word)[0]) {
		return "", false
	}
	lw := strings.ToLower(word)
	if a.ignored != nil && a.ignored(lw) || known != nil && known(lw) {
		return "", false
	}
	sp := a.speller()
	if sp == nil {
		return "", false
	}
	ok, sugg := sp.check(word)
	if ok || len(sugg) == 0 {
		return "", false
	}
	best := sugg[0]
	limit := 2
	if len([]rune(word)) <= 4 {
		limit = 1
	}
	if strings.ContainsAny(best, " -") || strings.EqualFold(best, word) || damerau(lw, strings.ToLower(best)) > limit {
		return "", false
	}
	return best, true
}

func damerau(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}

type acFix struct {
	start int
	orig  string
	fixed string
	punct rune
}

func (e *sendEntry) setCursor(off int) {
	e.SetCursorTextOffset(off)
}

func (e *sendEntry) correctBefore(punct rune) bool {
	e.lastFix = nil
	if e.ac == nil || e.browsing {
		return false
	}
	rs := []rune(e.Text)
	off := min(e.CursorTextOffset(), len(rs))
	ws := off
	for ws > 0 && isWordRune(rs[ws-1]) {
		ws--
	}
	ts := off
	for ts > 0 && !unicode.IsSpace(rs[ts-1]) {
		ts--
	}
	word := string(rs[ws:off])
	var known func(string) bool
	if e.known != nil {
		known = e.known()
	}
	fixed, ok := e.ac.fix(word, string(rs[ts:off]), string(rs[:ws]), known)
	if !ok {
		return false
	}
	e.SetText(string(rs[:ws]) + fixed + string(rs[off:]))
	e.setCursor(ws + len([]rune(fixed)))
	e.lastFix = &acFix{start: ws, orig: word, fixed: fixed, punct: punct}
	if e.onFix != nil {
		e.onFix(word + " → " + fixed + " · Backspace to undo")
	}
	return true
}

func (e *sendEntry) undoFix() bool {
	f := e.lastFix
	e.lastFix = nil
	if f == nil {
		return false
	}
	rs := []rune(e.Text)
	want := []rune(f.fixed + string(f.punct))
	end := f.start + len(want)
	if f.punct == 0 {
		end = f.start + len([]rune(f.fixed))
		want = []rune(f.fixed)
	}
	off := e.CursorTextOffset()
	if end > len(rs) || off != end || string(rs[f.start:end]) != string(want) {
		return false
	}
	restored := f.orig
	if f.punct != 0 {
		restored += string(f.punct)
	}
	e.SetText(string(rs[:f.start]) + restored + string(rs[end:]))
	e.setCursor(f.start + len([]rune(restored)))
	if e.ac != nil && e.ac.ignore != nil {
		e.ac.ignore(strings.ToLower(f.orig))
	}
	if e.onFix != nil {
		e.onFix("Kept “" + f.orig + "”; it won't be corrected again")
	}
	return true
}

func (e *sendEntry) TypedRune(r rune) {
	if isACBoundary(r) && e.correctBefore(r) {
		e.Entry.TypedRune(r)
		return
	}
	e.lastFix = nil
	e.Entry.TypedRune(r)
}

func (d *desk) autocorrectOn() bool {
	return d.app.Preferences().BoolWithFallback("autocorrect", true)
}

func (d *desk) acIgnored() map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(d.app.Preferences().String("autocorrectIgnore"))) {
		out[w] = true
	}
	return out
}

func (d *desk) newAutocorrector() *autocorrector {
	if d.ac != nil {
		return d.ac
	}
	d.ac = &autocorrector{
		enabled: d.autocorrectOn,
		ignored: func(w string) bool { return d.acIgnored()[w] },
		ignore: func(w string) {
			list := d.app.Preferences().String("autocorrectIgnore")
			d.app.Preferences().SetString("autocorrectIgnore", strings.TrimSpace(list+"\n"+w))
		},
		start: func() speller {
			if !hunspellAvailable() {
				return nil
			}
			h, err := startHunspell(acLang)
			if err != nil {
				return nil
			}
			return h
		},
	}
	return d.ac
}

type wordSet struct {
	n   int
	set map[string]bool
}

func (d *desk) knownWords(s *deskSession) func(string) bool {
	if s == nil {
		return nil
	}
	if d.acKnown == nil {
		d.acKnown = map[string]*wordSet{}
	}
	ws := d.acKnown[s.id]
	if ws == nil || ws.n != len(s.conv.Items) {
		ws = &wordSet{n: len(s.conv.Items), set: map[string]bool{}}
		for _, it := range s.conv.Items {
			if it.Kind != chat.Assistant && it.Kind != chat.Tool {
				continue
			}
			for _, w := range strings.FieldsFunc(it.Text+" "+it.Summary, func(r rune) bool { return !isWordRune(r) }) {
				ws.set[strings.ToLower(w)] = true
			}
		}
		d.acKnown[s.id] = ws
	}
	return func(w string) bool { return ws.set[w] }
}

func (d *desk) flashHint(t *canvas.Text, msg string) {
	if t == nil {
		return
	}
	prev := t.Text
	if d.hintPrev != nil {
		if p, ok := d.hintPrev[t]; ok {
			prev = p
		}
	} else {
		d.hintPrev = map[*canvas.Text]string{}
	}
	d.hintPrev[t] = prev
	setText(t, msg)
	time.AfterFunc(acHintFor, func() {
		d.do(func() {
			if t.Text == msg {
				setText(t, prev)
			}
			if d.hintPrev[t] == prev {
				delete(d.hintPrev, t)
			}
		})
	})
}

func (d *desk) autocorrectSettings() fyne.CanvasObject {
	chk := widget.NewCheck("Autocorrect the message box", func(on bool) { d.app.Preferences().SetBool("autocorrect", on) })
	chk.Checked = d.autocorrectOn()
	words := widget.NewButton("Ignored words...", func() {
		e := widget.NewMultiLineEntry()
		e.SetText(d.app.Preferences().String("autocorrectIgnore"))
		e.SetMinRowsVisible(8)
		note := widget.NewLabel("One word per line. Words here are never corrected. Backspace right after a correction adds the word for you.")
		note.Wrapping = fyne.TextWrapWord
		dialog.ShowCustomConfirm("Ignored words", "Save", "Cancel", container.NewBorder(note, nil, nil, nil, e), func(ok bool) {
			if ok {
				d.app.Preferences().SetString("autocorrectIgnore", strings.TrimSpace(e.Text))
			}
		}, d.win)
	})
	words.Importance = widget.LowImportance
	row := container.NewHBox(chk, words)
	if !hunspellAvailable() {
		chk.Disable()
		words.Disable()
		return container.NewVBox(row, mutedText("Needs hunspell with an en_US dictionary (e.g. dnf install hunspell hunspell-en-US, or apt install hunspell hunspell-en-us).", fontMeta))
	}
	return row
}
