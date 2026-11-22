package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	d       *desk
	btn     *widget.Button
	langBtn *widget.Button
	state   sttState
	live    liveSession
	base    string
	ready   func(lang string) error
	start   func(lang string, update func(whisper.Update)) (liveSession, error)

	entry *sendEntry

	modelMu   sync.Mutex
	model     *whisper.Model
	modelPath string
	idle      *time.Timer
}

type language struct{ code, name string }

var languages = []language{
	{"en", "English"}, {"zh", "Chinese"}, {"de", "German"}, {"es", "Spanish"}, {"ru", "Russian"}, {"ko", "Korean"},
	{"fr", "French"}, {"ja", "Japanese"}, {"pt", "Portuguese"}, {"tr", "Turkish"}, {"pl", "Polish"}, {"ca", "Catalan"},
	{"nl", "Dutch"}, {"ar", "Arabic"}, {"sv", "Swedish"}, {"it", "Italian"}, {"id", "Indonesian"}, {"hi", "Hindi"},
	{"fi", "Finnish"}, {"vi", "Vietnamese"}, {"he", "Hebrew"}, {"uk", "Ukrainian"}, {"el", "Greek"}, {"ms", "Malay"},
	{"cs", "Czech"}, {"ro", "Romanian"}, {"da", "Danish"}, {"hu", "Hungarian"}, {"ta", "Tamil"}, {"no", "Norwegian"},
	{"th", "Thai"}, {"ur", "Urdu"}, {"hr", "Croatian"}, {"bg", "Bulgarian"}, {"lt", "Lithuanian"}, {"la", "Latin"},
	{"mi", "Maori"}, {"ml", "Malayalam"}, {"cy", "Welsh"}, {"sk", "Slovak"}, {"te", "Telugu"}, {"fa", "Persian"},
	{"lv", "Latvian"}, {"bn", "Bengali"}, {"sr", "Serbian"}, {"az", "Azerbaijani"}, {"sl", "Slovenian"}, {"kn", "Kannada"},
	{"et", "Estonian"}, {"mk", "Macedonian"}, {"br", "Breton"}, {"eu", "Basque"}, {"is", "Icelandic"}, {"hy", "Armenian"},
	{"ne", "Nepali"}, {"mn", "Mongolian"}, {"bs", "Bosnian"}, {"kk", "Kazakh"}, {"sq", "Albanian"}, {"sw", "Swahili"},
	{"gl", "Galician"}, {"mr", "Marathi"}, {"pa", "Punjabi"}, {"si", "Sinhala"}, {"km", "Khmer"}, {"sn", "Shona"},
	{"yo", "Yoruba"}, {"so", "Somali"}, {"af", "Afrikaans"}, {"oc", "Occitan"}, {"ka", "Georgian"}, {"be", "Belarusian"},
	{"tg", "Tajik"}, {"sd", "Sindhi"}, {"gu", "Gujarati"}, {"am", "Amharic"}, {"yi", "Yiddish"}, {"lo", "Lao"},
	{"uz", "Uzbek"}, {"fo", "Faroese"}, {"ht", "Haitian Creole"}, {"ps", "Pashto"}, {"tk", "Turkmen"}, {"nn", "Nynorsk"},
	{"mt", "Maltese"}, {"sa", "Sanskrit"}, {"lb", "Luxembourgish"}, {"my", "Myanmar"}, {"bo", "Tibetan"}, {"tl", "Tagalog"},
	{"mg", "Malagasy"}, {"as", "Assamese"}, {"tt", "Tatar"}, {"haw", "Hawaiian"}, {"ln", "Lingala"}, {"ha", "Hausa"},
	{"ba", "Bashkir"}, {"jw", "Javanese"}, {"su", "Sundanese"}, {"yue", "Cantonese"},
}

func init() {
	rest := languages[1:]
	sort.Slice(rest, func(i, j int) bool { return rest[i].name < rest[j].name })
}

func languageName(code string) string {
	for _, l := range languages {
		if l.code == code {
			return l.name
		}
	}
	return code
}

func sttHome() string {
	if d, err := os.UserHomeDir(); err == nil {
		return filepath.Join(d, ".local", "share", "quill")
	}
	return ""
}

func modelFile(lang string) string {
	if lang == "en" {
		return "ggml-base.en-q5_1.bin"
	}
	return "ggml-base-q5_1.bin"
}

func modelPath(lang string) string {
	if p := os.Getenv("QUILL_WHISPER_MODEL"); p != "" {
		return p
	}
	return filepath.Join(sttHome(), "models", modelFile(lang))
}

func modelReady(lang string) error {
	st, err := os.Stat(modelPath(lang))
	if err != nil || st.Size() < minModelSize {
		return fmt.Errorf("the %s speech model is not downloaded", languageName(lang))
	}
	return nil
}

func downloadModel(ctx context.Context, lang string, progress func(float64)) error {
	dst := modelPath(lang)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelBaseURL+modelFile(lang), nil)
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
	sp := &speech{d: d, ready: modelReady}
	sp.start = sp.startLive
	sp.btn = widget.NewButtonWithIcon("", theme.MediaRecordIcon(), sp.click)
	sp.btn.Importance = widget.LowImportance
	sp.langBtn = widget.NewButton("Voice: "+strings.ToUpper(sp.lang()), func() {
		d.pickLanguage(sp.lang(), func(code string) { sp.setLang(code) })
	})
	sp.langBtn.Importance = widget.MediumImportance
	sp.syncOff()
	return sp
}

// syncOff hides the mic and language buttons while speech to text is off
// (Welcome's "Off"); picking a language turns it on again.
func (sp *speech) syncOff() {
	off := sp.d.app.Preferences().Bool(prefSTTOff)
	for _, b := range []*widget.Button{sp.btn, sp.langBtn} {
		if off {
			b.Hide()
		} else {
			b.Show()
		}
	}
}

func (sp *speech) target() *sendEntry {
	if sp.entry != nil {
		return sp.entry
	}
	return sp.d.input
}

func (sp *speech) lang() string {
	return sp.d.app.Preferences().StringWithFallback("sttLang", "en")
}

func (sp *speech) setLang(code string) {
	sp.d.app.Preferences().SetString("sttLang", code)
	sp.d.app.Preferences().SetBool(prefSTTOff, false)
	sp.langBtn.SetText("Voice: " + strings.ToUpper(code))
	sp.syncOff()
}

func (sp *speech) loadModel(lang string) (*whisper.Model, error) {
	sp.modelMu.Lock()
	defer sp.modelMu.Unlock()
	if sp.idle != nil {
		sp.idle.Stop()
	}
	path := modelPath(lang)
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
	pick := widget.NewButton(languageName(lang), nil)
	size := widget.NewLabel("")
	info := func() {
		pick.SetText(languageName(chosen))
		size.SetText(fmt.Sprintf("Downloads the %s speech model once (about 57 MB) into ~/.local/share/quill. After that it works offline and nothing leaves this computer.", languageName(chosen)))
	}
	size.Wrapping = fyne.TextWrapWord
	pick.OnTapped = func() {
		d.pickLanguage(chosen, func(code string) {
			chosen = code
			info()
		})
	}
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
		bar.Show()
		code := chosen
		go func() {
			err := downloadModel(ctx, code, func(f float64) { d.do(func() { bar.SetValue(f) }) })
			d.do(func() {
				if err != nil {
					get.Enable()
					pick.Enable()
					bar.Hide()
					if ctx.Err() == nil {
						dialog.ShowError(err, d.win)
					}
					return
				}
				dlg.Hide()
				sp.setLang(code)
				sp.begin()
			})
		}()
	}
	body := container.NewVBox(container.NewHBox(widget.NewLabel("Language"), pick), size, bar, get)
	dlg = dialog.NewCustom("Set up speech to text", "Cancel", body, d.win)
	dlg.SetOnClosed(cancel)
	dlg.Resize(fyne.NewSize(520, 260))
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
			o.(*widget.Label).SetText(l.name + "  (" + l.code + ")")
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
		q = strings.ToLower(strings.TrimSpace(q))
		shown = nil
		for _, l := range languages {
			if q == "" || strings.Contains(strings.ToLower(l.name), q) || strings.HasPrefix(l.code, q) {
				shown = append(shown, l)
			}
		}
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
