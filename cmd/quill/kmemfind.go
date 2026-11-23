package main

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/transcript"
)

// Find old conversation (topic tabs design §3.7 #3, Q17; sub H). Old
// conversations are no longer listed or resumed. This searches the
// transcripts Quill Desktop already scanned (d.order): titles, kmem names and
// folders at once, full text on demand off the UI thread. A hit opens read
// only; "Copy as kmem fact" files an excerpt through `kmem add` after the user
// picks the topic. Nothing here starts a session, so a topic never gets a
// second live one (one live session per topic, topics.go).

const (
	findMaxHits     = 100
	findFullScanMax = 400 // most recent transcripts read for a full text search
	findViewMax     = 200 << 10
	findFactMax     = 2000
)

func queryWords(q string) []string { return strings.Fields(strings.ToLower(q)) }

func hasAllWords(text string, words []string) bool {
	t := strings.ToLower(text)
	for _, w := range words {
		if !strings.Contains(t, w) {
			return false
		}
	}
	return len(words) > 0
}

// findOld: transcripts whose title, kmem name, folder or id holds every word
// of q, newest first. Only transcripts with a file can be shown.
func findOld(infos []transcript.Info, q string) []transcript.Info {
	words := queryWords(q)
	var hits []transcript.Info
	for _, in := range infos {
		if in.Path == "" {
			continue
		}
		if hasAllWords(in.Title+" "+in.Kmem+" "+folderLabel(in.Cwd)+" "+in.ID, words) {
			hits = append(hits, in)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Last.After(hits[j].Last) })
	if len(hits) > findMaxHits {
		hits = hits[:findMaxHits]
	}
	return hits
}

// convText is what a read-only view shows: what the user and Claude said.
func convText(c *chat.Conversation) string {
	var b strings.Builder
	for _, it := range c.Items {
		who := ""
		switch it.Kind {
		case chat.User:
			who = "You"
		case chat.Assistant:
			who = "Claude"
		default:
			continue
		}
		if t := strings.TrimSpace(it.Text); t != "" {
			b.WriteString(who + ": " + t + "\n\n")
		}
		if b.Len() > findViewMax {
			b.WriteString("… (cut)")
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// snippetAround: a short excerpt of text around the first query word.
func snippetAround(text string, words []string) string {
	low := strings.ToLower(text)
	at := 0
	for _, w := range words {
		if i := strings.Index(low, w); i >= 0 {
			at = i
			break
		}
	}
	from := max(0, at-60)
	for from > 0 && from < len(text) && text[from]&0xC0 == 0x80 {
		from-- // not in the middle of a rune
	}
	to := min(len(text), at+120)
	for to < len(text) && text[to]&0xC0 == 0x80 {
		to++
	}
	return oneLine(strings.TrimSpace(text[from:to]), 180)
}

// fullTextHit reads one transcript and reports an excerpt when every word of
// the query is in it (blocking: call off the UI thread).
func fullTextHit(path string, words []string) (string, bool) {
	c, err := transcript.Load(path)
	if err != nil && c == nil {
		return "", false
	}
	text := convText(c)
	if !hasAllWords(text, words) {
		return "", false
	}
	return snippetAround(text, words), true
}

type findHit struct {
	Info    transcript.Info
	Snippet string
}

func (h findHit) label() string {
	t := firstNonEmpty(h.Info.Title, "(no title)")
	if h.Info.Kmem != "" {
		t += "  [" + h.Info.Kmem + "]"
	}
	return t
}

func (h findHit) caption() string {
	s := folderLabel(h.Info.Cwd) + " · " + ago(h.Info.Last)
	if h.Snippet != "" {
		s += " · …" + h.Snippet
	}
	return s
}

// factText: the excerpt filed by "Copy as kmem fact": the user's selection,
// else the start of the conversation, cut to findFactMax.
func factText(selected, whole string) string {
	t := strings.TrimSpace(firstNonEmpty(selected, whole))
	if len(t) > findFactMax {
		t = t[:findFactMax]
		for len(t) > 0 && t[len(t)-1]&0xC0 == 0x80 {
			t = t[:len(t)-1]
		}
		t += " …"
	}
	return t
}

// addFactArgs: `kmem add TOPIC --why W --src secondary -- TEXT` (what follows
// -- is text, so a leading dash or option word in it stays text).
func addFactArgs(topic, title, text string) []string {
	return []string{"add", topic, "--why", "Quill Desktop: copied by the user from the old conversation “" + oneLine(title, 80) + "”", "--src", "secondary", "--", text}
}

// showFindOld opens the search dialog (Topics tab, "Find old conversation").
func (p *kmemPanel) showFindOld() {
	d := p.d
	var infos []transcript.Info
	infos = append(infos, d.order...)
	var hits []findHit
	var gen atomic.Int64
	status := caption("Type words of the title, kmem name or folder; tick Full text to search what was said.")
	query := widget.NewEntry()
	query.SetPlaceHolder("Find old conversation")
	full := widget.NewCheck("Full text (slower)", nil)
	sel := -1
	list := widget.NewList(func() int { return len(hits) },
		func() fyne.CanvasObject { return container.NewVBox(widget.NewLabel("title"), caption("caption")) },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			box := o.(*fyne.Container)
			box.Objects[0].(*widget.Label).SetText(hits[i].label())
			box.Objects[1].(*widget.Label).SetText(hits[i].caption())
		})
	view := widget.NewButtonWithIcon("View read-only", theme.VisibilityIcon(), func() {
		if sel >= 0 && sel < len(hits) {
			p.viewOld(hits[sel].Info)
		}
	})
	view.Disable()
	list.OnSelected = func(i widget.ListItemID) { sel = i; view.Enable() }
	run := func() {
		q := query.Text
		n := gen.Add(1)
		sel = -1
		view.Disable()
		list.UnselectAll()
		if len(queryWords(q)) == 0 {
			hits = nil
			list.Refresh()
			status.SetText("Type words to search.")
			return
		}
		quick := findOld(infos, q)
		hits = hits[:0]
		for _, in := range quick {
			hits = append(hits, findHit{Info: in})
		}
		list.Refresh()
		status.SetText(fmt.Sprintf("%d match(es) in titles, kmem names and folders.", len(hits)))
		if !full.Checked {
			return
		}
		status.SetText("Searching full text...")
		words := queryWords(q)
		go func() {
			var found []findHit
			recent := append([]transcript.Info(nil), infos...)
			sort.SliceStable(recent, func(i, j int) bool { return recent[i].Last.After(recent[j].Last) })
			if len(recent) > findFullScanMax {
				recent = recent[:findFullScanMax]
			}
			for _, in := range recent {
				if gen.Load() != n {
					return
				}
				if in.Path == "" {
					continue
				}
				if sn, ok := fullTextHit(in.Path, words); ok {
					found = append(found, findHit{Info: in, Snippet: sn})
					if len(found) >= findMaxHits {
						break
					}
				}
			}
			d.do(func() {
				if gen.Load() != n {
					return
				}
				hits = found
				list.Refresh()
				status.SetText(fmt.Sprintf("%d match(es) in the full text of the %d most recent conversations.", len(hits), min(len(infos), findFullScanMax)))
			})
		}()
	}
	query.OnSubmitted = func(string) { run() }
	full.OnChanged = func(bool) {
		if strings.TrimSpace(query.Text) != "" {
			run()
		}
	}
	find := widget.NewButtonWithIcon("Find", theme.SearchIcon(), run)
	top := container.NewVBox(container.NewBorder(nil, nil, nil, find, query), full, status)
	body := container.NewBorder(top, container.NewHBox(view), nil, nil, list)
	dlg := dialog.NewCustom("Find old conversation", "Close", body, p.parent())
	dlg.Resize(fyne.NewSize(760, 560))
	dlg.Show()
}

// viewOld shows an old conversation read only, loaded off the UI thread.
// "Copy as kmem fact…" files the selected text (else the start) under a topic
// the user types; it is the only write and it is the user's click.
func (p *kmemPanel) viewOld(in transcript.Info) {
	d := p.d
	text := widget.NewMultiLineEntry()
	text.Wrapping = fyne.TextWrapWord
	text.SetText("Loading...")
	loaded := ""
	text.OnChanged = func(s string) { // read only: edits are put back
		if loaded != "" && s != loaded {
			text.SetText(loaded)
		}
	}
	copyBtn := widget.NewButtonWithIcon("Copy as kmem fact…", theme.ContentCopyIcon(), func() {
		if loaded == "" {
			return
		}
		p.copyAsFact(in, factText(text.SelectedText(), loaded))
	})
	title := firstNonEmpty(in.Title, "(no title)")
	head := container.NewVBox(boldLabel(title), caption(folderLabel(in.Cwd)+" · "+in.Last.Format(time.DateTime)+" · read only: this does not start or resume a session; select text first to copy just that part."))
	dlg := dialog.NewCustom("Old conversation", "Close", container.NewBorder(head, container.NewHBox(copyBtn), nil, nil, text), p.parent())
	dlg.Resize(fyne.NewSize(800, 600))
	dlg.Show()
	go func() {
		c, err := transcript.Load(in.Path)
		s := ""
		if c != nil {
			s = convText(c)
		}
		d.do(func() {
			switch {
			case s != "":
				loaded = s
				text.SetText(s)
			case err != nil:
				text.SetText("Couldn't read this conversation: " + err.Error())
			default:
				text.SetText("Nothing was said in this conversation.")
			}
		})
	}()
}

func (p *kmemPanel) copyAsFact(in transcript.Info, excerpt string) {
	d := p.d
	topic := widget.NewEntry()
	topic.SetText(topicForName(in.Kmem)) // editable; blank when unknown
	topic.SetPlaceHolder("kmem topic, e.g. quill/notes")
	body := widget.NewMultiLineEntry()
	body.Wrapping = fyne.TextWrapWord
	body.SetText(excerpt)
	body.SetMinRowsVisible(8)
	dialog.ShowForm("Copy as kmem fact", "Add to kmem", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Topic", topic), widget.NewFormItem("Text", body)},
		func(ok bool) {
			t, tx := strings.TrimSpace(topic.Text), strings.TrimSpace(body.Text)
			if !ok {
				return
			}
			if !kmemTopicRe.MatchString(t) || tx == "" {
				dialog.ShowError(fmt.Errorf("give a kmem topic (letters, digits, - and /) and some text"), p.parent())
				return
			}
			go func() {
				out, err := d.runKmem("kmem", addFactArgs(t, in.Title, tx)...)
				d.do(func() {
					if err != nil {
						dialog.ShowError(fmt.Errorf("kmem: %s", oneLine(firstNonEmpty(strings.TrimSpace(out), err.Error()), 300)), p.parent())
						return
					}
					dialog.ShowInformation("Added to kmem", oneLine(strings.TrimSpace(out), 300), p.parent())
					p.reload()
				})
			}()
		}, p.parent())
}
