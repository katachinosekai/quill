package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/whisper"
)

const sttNotice = "Speech to text runs entirely on this computer.\n\n" +
	"Your voice and the text made from it are not collected, stored or sent to Anthropic or anyone else. " +
	"Audio is kept in memory only while you speak, and the text only goes into your message box. " +
	"Nothing is sent until you press Send.\n\nThis is just a convenience for you."

const (
	modelBaseURL = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/"
	modelIdle    = 5 * time.Minute
	minModelSize = 30 << 20
)

type sttState int

const (
	sttIdle sttState = iota
	sttRecording
	sttFinishing
)

type liveSession interface{ Stop() }

type speech struct {
	d     *desk
	btn   *widget.Button
	state sttState
	live  liveSession
	base  string
	ready func(lang string) error
	start func(lang string, update func(whisper.Update)) (liveSession, error)

	entry *sendEntry

	modelMu   sync.Mutex
	model     *whisper.Model
	modelPath string
	idle      *time.Timer
}

func sttHome() string {
	if d, err := os.UserHomeDir(); err == nil {
		return filepath.Join(d, ".local", "share", "quill")
	}
	return ""
}

// Multilingual model sizes offered for voice input; base is the default.
type modelSize struct{ id, label, mb string }

const prefSTTModel = "sttModelSize"

var modelSizes = []modelSize{
	{"base", "Base", "57 MB"},
	{"small", "Small", "181 MB"},
	{"medium", "Medium", "514 MB"},
}

func sizeInfo(id string) modelSize {
	for _, m := range modelSizes {
		if m.id == id {
			return m
		}
	}
	return modelSizes[0]
}

// modelFile picks the model file: English uses the English-only model of the
// chosen size, every other language and Auto-detect the multilingual one.
func modelFile(lang, size string) string {
	size = sizeInfo(size).id
	q := "q5_1"
	if size == "medium" {
		q = "q5_0"
	}
	if lang == "en" {
		return "ggml-" + size + ".en-" + q + ".bin"
	}
	return "ggml-" + size + "-" + q + ".bin"
}

func modelPath(lang, size string) string {
	if p := os.Getenv("QUILL_WHISPER_MODEL"); p != "" {
		return p
	}
	return filepath.Join(sttHome(), "models", modelFile(lang, size))
}

func modelReady(lang, size string) error {
	st, err := os.Stat(modelPath(lang, size))
	if err != nil || st.Size() < minModelSize {
		return fmt.Errorf("the %s speech model is not downloaded", languageName(lang))
	}
	return nil
}

func downloadModel(ctx context.Context, lang, size string, progress func(float64)) error {
	dst := modelPath(lang, size)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelBaseURL+modelFile(lang, size), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	total := resp.ContentLength
	var got int64
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(tmp)
				return werr
			}
			got += int64(n)
			if total > 0 {
				progress(float64(got) / float64(total))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(tmp)
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if got < minModelSize {
		os.Remove(tmp)
		return fmt.Errorf("download was incomplete")
	}
	return os.Rename(tmp, dst)
}

func newSpeech(d *desk) *speech {
	sp := &speech{d: d}
	sp.ready = func(lang string) error { return modelReady(lang, sp.modelSize()) }
	sp.start = sp.startLive
	sp.btn = widget.NewButtonWithIcon("", theme.MediaRecordIcon(), sp.click)
	sp.btn.Importance = widget.LowImportance
	sp.syncOff()
	return sp
}

// syncOff hides the mic while speech to text is off (Welcome's "Off" or the
// Settings switch); the language and model size live in Settings.
func (sp *speech) syncOff() {
	off := sp.d.app.Preferences().Bool(prefSTTOff)
	if off {
		sp.btn.Hide()
	} else {
		sp.btn.Show()
	}
}

func (sp *speech) target() *sendEntry {
	if sp.entry != nil {
		return sp.entry
	}
	return sp.d.input
}

func (sp *speech) lang() string {
	return sp.d.app.Preferences().StringWithFallback("sttLang", autoLang)
}

func (sp *speech) modelSize() string {
	return sizeInfo(sp.d.app.Preferences().StringWithFallback(prefSTTModel, "base")).id
}

func (sp *speech) setModelSize(id string) {
	sp.d.app.Preferences().SetString(prefSTTModel, sizeInfo(id).id)
}

func (sp *speech) setLangPref(code string) {
	sp.d.app.Preferences().SetString("sttLang", code)
}

// setLang also turns voice input on (used once a model is downloaded).
func (sp *speech) setLang(code string) {
	sp.setLangPref(code)
	sp.d.app.Preferences().SetBool(prefSTTOff, false)
	sp.syncOff()
}

// voiceSettings is the Settings section for voice input: on/off switch,
// language (Auto-detect by default) and model size.
func (d *desk) voiceSettings() fyne.CanvasObject {
	p := d.app.Preferences()
	// Settings is built before the speech object exists, so only read prefs here.
	lang := func() string { return p.StringWithFallback("sttLang", autoLang) }
	on := widget.NewCheck("Voice input (the mic button under the message box)", func(v bool) {
		p.SetBool(prefSTTOff, !v)
		if d.stt != nil {
			d.stt.syncOff()
		}
	})
	on.Checked = !p.Bool(prefSTTOff)
	langBtn := widget.NewButton(languageLabel(languageFor(lang())), nil)
	langBtn.OnTapped = func() {
		d.pickLanguage(lang(), func(code string) {
			p.SetString("sttLang", code)
			langBtn.SetText(languageLabel(languageFor(code)))
		})
	}
	var labels []string
	for _, m := range modelSizes {
		labels = append(labels, m.label+" ("+m.mb+")")
	}
	size := widget.NewSelect(labels, func(v string) {
		for _, m := range modelSizes {
			if v == m.label+" ("+m.mb+")" {
				p.SetString(prefSTTModel, m.id)
			}
		}
	})
	cur := sizeInfo(p.StringWithFallback(prefSTTModel, "base"))
	size.SetSelected(cur.label + " (" + cur.mb + ")")
	note := widget.NewLabel("Auto-detect uses the multilingual model and finds the spoken language itself. A larger size understands more languages better but is slower; it downloads the first time you use the mic. Voice input runs entirely on this computer.")
	note.Wrapping = fyne.TextWrapWord
	return container.NewVBox(on,
		container.NewBorder(nil, nil, widget.NewLabel("Language"), nil, langBtn),
		container.NewBorder(nil, nil, widget.NewLabel("Model size"), nil, size), note)
}

func (sp *speech) loadModel(lang string) (*whisper.Model, error) {
	sp.modelMu.Lock()
	defer sp.modelMu.Unlock()
	if sp.idle != nil {
		sp.idle.Stop()
	}
	path := modelPath(lang, sp.modelSize())
	if sp.model != nil && sp.modelPath == path {
		return sp.model, nil
	}
	if sp.model != nil {
		sp.model.Close()
		sp.model = nil
	}
	m, err := whisper.Load(path)
	if err != nil {
		return nil, err
	}
	sp.model, sp.modelPath = m, path
	return m, nil
}

func (sp *speech) releaseLater() {
	sp.modelMu.Lock()
	defer sp.modelMu.Unlock()
	if sp.idle != nil {
		sp.idle.Stop()
	}
	sp.idle = time.AfterFunc(modelIdle, func() {
		sp.modelMu.Lock()
		defer sp.modelMu.Unlock()
		if sp.model != nil {
			sp.model.Close()
			sp.model = nil
		}
	})
}

func (sp *speech) startLive(lang string, update func(whisper.Update)) (liveSession, error) {
	m, err := sp.loadModel(lang)
	if err != nil {
		return nil, err
	}
	return whisper.StartLive(m, whisper.NewMic(), lang, update)
}

func (sp *speech) click() {
	d := sp.d
	switch sp.state {
	case sttRecording:
		sp.finish()
		return
	case sttFinishing:
		return
	}
	if !d.app.Preferences().Bool("sttNoticeShown") {
		dialog.ShowCustomConfirm("Speech to text", "Continue", "Cancel", widget.NewLabelWithStyle(sttNotice, fyne.TextAlignLeading, fyne.TextStyle{}), func(ok bool) {
			if !ok {
				return
			}
			d.app.Preferences().SetBool("sttNoticeShown", true)
			sp.begin()
		}, d.win)
		return
	}
	sp.begin()
}

func (sp *speech) begin() {
	d := sp.d
	lang := sp.lang()
	if err := sp.ready(lang); err != nil {
		sp.setup(lang)
		return
	}
	sp.base = strings.TrimRight(sp.target().Text, " ")
	if sp.base != "" {
		sp.base += " "
	}
	sp.state = sttRecording
	sp.sync()
	go func() {
		live, err := sp.start(lang, func(u whisper.Update) { d.do(func() { sp.apply(u) }) })
		d.do(func() {
			if err != nil {
				sp.state = sttIdle
				sp.sync()
				dialog.ShowError(fmt.Errorf("could not start speech to text: %w", err), d.win)
				return
			}
			sp.live = live
			if sp.state != sttRecording {
				go live.Stop()
			}
		})
	}()
}

func (sp *speech) apply(u whisper.Update) {
	d := sp.d
	if u.Err != nil && u.Committed == "" && u.Partial == "" {
		return
	}
	text := u.Committed
	if u.Partial != "" {
		if text != "" {
			text += " "
		}
		text += u.Partial
	}
	if sp.state == sttIdle {
		return
	}
	full := sp.base + text
	in := sp.target()
	if in.Text != full {
		in.SetText(full)
		in.CursorRow = strings.Count(full, "\n")
		in.CursorColumn = len([]rune(full[strings.LastIndex(full, "\n")+1:]))
		in.Refresh()
	}
	if u.Final {
		sp.state = sttIdle
		sp.live = nil
		sp.sync()
		d.win.Canvas().Focus(in)
		sp.entry = nil
		sp.releaseLater()
	}
}

func (sp *speech) finish() {
	live := sp.live
	sp.state = sttFinishing
	sp.sync()
	if live == nil {
		return
	}
	go live.Stop()
}

func (sp *speech) setup(lang string) {
	d := sp.d
	chosen := lang
	chosenSize := sp.modelSize()
	pick := widget.NewButton(languageName(lang), nil)
	size := widget.NewLabel("")
	info := func() {
		pick.SetText(languageName(chosen))
		size.SetText(fmt.Sprintf("Downloads the %s speech model once (about %s) into ~/.local/share/quill. After that it works offline and nothing leaves this computer. Larger sizes understand more languages better but are slower.", languageName(chosen), sizeInfo(chosenSize).mb))
	}
	size.Wrapping = fyne.TextWrapWord
	pick.OnTapped = func() {
		d.pickLanguage(chosen, func(code string) {
			chosen = code
			info()
		})
	}
	var sizeLabels []string
	for _, m := range modelSizes {
		sizeLabels = append(sizeLabels, m.label+" ("+m.mb+")")
	}
	sizePick := widget.NewSelect(sizeLabels, func(v string) {
		for _, m := range modelSizes {
			if v == m.label+" ("+m.mb+")" {
				chosenSize = m.id
			}
		}
		info()
	})
	sizePick.SetSelected(sizeInfo(chosenSize).label + " (" + sizeInfo(chosenSize).mb + ")")
	info()
	bar := widget.NewProgressBar()
	bar.Hide()
	var dlg dialog.Dialog
	ctx, cancel := context.WithCancel(context.Background())
	get := widget.NewButtonWithIcon("Download and start", theme.DownloadIcon(), nil)
	get.Importance = widget.HighImportance
	get.OnTapped = func() {
		get.Disable()
		pick.Disable()
		sizePick.Disable()
		bar.Show()
		code, sz := chosen, chosenSize
		go func() {
			err := downloadModel(ctx, code, sz, func(f float64) { d.do(func() { bar.SetValue(f) }) })
			d.do(func() {
				if err != nil {
					get.Enable()
					pick.Enable()
					sizePick.Enable()
					bar.Hide()
					if ctx.Err() == nil {
						dialog.ShowError(err, d.win)
					}
					return
				}
				dlg.Hide()
				sp.setLang(code)
				sp.setModelSize(sz)
				sp.begin()
			})
		}()
	}
	body := container.NewVBox(container.NewHBox(widget.NewLabel("Language"), pick), container.NewHBox(widget.NewLabel("Model size"), sizePick), size, bar, get)
	dlg = dialog.NewCustom("Set up speech to text", "Cancel", body, d.win)
	dlg.SetOnClosed(cancel)
	dlg.Resize(fyne.NewSize(520, 300))
	dlg.Show()
}

func (d *desk) pickLanguage(current string, onPick func(code string)) {
	search := &escEntry{}
	search.ExtendBaseWidget(search)
	search.SetPlaceHolder("Search languages")
	shown := languages
	list := widget.NewList(
		func() int { return len(shown) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			l := shown[i]
			o.(*widget.Label).SetText(languageLabel(l))
		},
	)
	var pop *widget.PopUp
	list.OnSelected = func(i widget.ListItemID) {
		if i < len(shown) {
			onPick(shown[i].code)
			pop.Hide()
		}
	}
	selectCurrent := func() {
		for i, l := range shown {
			if l.code == current {
				list.Select(i)
				list.ScrollTo(i)
				return
			}
		}
		list.UnselectAll()
	}
	search.OnChanged = func(q string) {
		shown = filterLanguages(q)
		list.Refresh()
		sel := list.OnSelected
		list.OnSelected = nil
		selectCurrent()
		list.OnSelected = sel
	}
	search.OnSubmitted = func(string) {
		if len(shown) > 0 {
			onPick(shown[0].code)
			pop.Hide()
		}
	}
	box := container.NewBorder(search, nil, nil, nil, list)
	pop = widget.NewModalPopUp(container.NewPadded(sized(320, 380, box)), d.win.Canvas())
	sel := list.OnSelected
	list.OnSelected = nil
	selectCurrent()
	list.OnSelected = sel
	search.onEsc = pop.Hide
	pop.Show()
	d.escClose(pop, pop.Hide)
	d.win.Canvas().Focus(search)
}

func (sp *speech) sync() {
	switch sp.state {
	case sttRecording:
		sp.btn.SetIcon(theme.MediaStopIcon())
		sp.btn.Importance = widget.DangerImportance
		sp.d.chat.setLive("● Listening (" + languageName(sp.lang()) + ")... click the mic again to stop")
	case sttFinishing:
		sp.btn.SetIcon(theme.MediaRecordIcon())
		sp.btn.Importance = widget.MediumImportance
		sp.btn.Disable()
		sp.d.chat.setLive("Finishing the last words...")
	default:
		sp.btn.SetIcon(theme.MediaRecordIcon())
		sp.btn.Importance = widget.LowImportance
		sp.btn.Enable()
		sp.d.chat.setLive(liveText(sp.d.current))
	}
	sp.btn.Refresh()
}
