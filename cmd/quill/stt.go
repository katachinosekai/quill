package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const sttNotice = "Speech to text runs entirely on this computer (whisper.cpp).\n\n" +
	"Your voice recording and the text made from it are not collected, stored or sent to Anthropic or anyone else. " +
	"The recording is deleted right after it is turned into text, and the text only goes into your message box. " +
	"Nothing is sent until you press Send.\n\nThis is just a convenience for you."

type sttState int

const (
	sttIdle sttState = iota
	sttRecording
	sttTranscribing
)

type speech struct {
	d         *desk
	btn       *widget.Button
	state     sttState
	stop      func() (string, error)
	record    func() (func() (string, error), error)
	recognize func(ctx context.Context, wav string) (string, error)
	ready     func() error
}

func sttHome() string {
	if d, err := os.UserHomeDir(); err == nil {
		return filepath.Join(d, ".local", "share", "quill")
	}
	return ""
}

func whisperBin() string {
	if p := os.Getenv("QUILL_WHISPER"); p != "" {
		return p
	}
	local := filepath.Join(sttHome(), "whisper.cpp", "build", "bin", "whisper-cli")
	if st, err := os.Stat(local); err == nil && st.Mode()&0o111 != 0 {
		return local
	}
	if p, err := exec.LookPath("whisper-cli"); err == nil {
		return p
	}
	return local
}

func whisperModel() string {
	if p := os.Getenv("QUILL_WHISPER_MODEL"); p != "" {
		return p
	}
	return filepath.Join(sttHome(), "models", "ggml-base.en.bin")
}

func sttSetupCommand() string {
	return `D=~/.local/share/quill; mkdir -p $D/models && cd $D && git clone --depth 1 --branch v1.7.6 https://github.com/ggml-org/whisper.cpp.git && cd whisper.cpp && cmake -B build -DCMAKE_BUILD_TYPE=Release && cmake --build build -j --target whisper-cli && curl -L -o $D/models/ggml-base.en.bin https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin`
}

func sttReady() error {
	if _, err := exec.LookPath("pw-record"); err != nil {
		return errors.New("pw-record (PipeWire) is not installed")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return errors.New("ffmpeg is not installed")
	}
	if st, err := os.Stat(whisperBin()); err != nil || st.Mode()&0o111 == 0 {
		return errors.New("whisper.cpp is not installed")
	}
	if _, err := os.Stat(whisperModel()); err != nil {
		return errors.New("the speech model is not downloaded")
	}
	return nil
}

func pwRecord() (func() (string, error), error) {
	dir, err := os.MkdirTemp("", "quill-stt-")
	if err != nil {
		return nil, err
	}
	raw := filepath.Join(dir, "rec.wav")
	cmd := exec.Command("pw-record", "--rate", "16000", "--channels", "1", "--format", "s16", raw)
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return func() (string, error) {
		_ = cmd.Process.Signal(syscall.SIGINT)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		out := filepath.Join(dir, "clean.wav")
		conv := exec.Command("ffmpeg", "-loglevel", "error", "-y", "-i", raw, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", out)
		if b, err := conv.CombinedOutput(); err != nil {
			os.RemoveAll(dir)
			return "", fmt.Errorf("could not read the recording: %s", strings.TrimSpace(string(b)))
		}
		os.Remove(raw)
		return out, nil
	}, nil
}

func whisperRecognize(ctx context.Context, wav string) (string, error) {
	defer os.RemoveAll(filepath.Dir(wav))
	cmd := exec.CommandContext(ctx, whisperBin(), "-m", whisperModel(), "-f", wav, "-nt", "-np", "-t", "8")
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("whisper.cpp failed: %v", err)
	}
	return cleanTranscript(string(b)), nil
}

func cleanTranscript(s string) string {
	var parts []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "[BLANK_AUDIO]" || strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			continue
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, " ")
}

func newSpeech(d *desk) *speech {
	sp := &speech{d: d, record: pwRecord, recognize: whisperRecognize, ready: sttReady}
	sp.btn = widget.NewButtonWithIcon("", theme.MediaRecordIcon(), sp.click)
	sp.btn.Importance = widget.LowImportance
	return sp
}

func (sp *speech) click() {
	d := sp.d
	switch sp.state {
	case sttRecording:
		sp.finish()
		return
	case sttTranscribing:
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
	if err := sp.ready(); err != nil {
		cmd := widget.NewEntry()
		cmd.SetText(sttSetupCommand())
		cmd.MultiLine = true
		cmd.Wrapping = fyne.TextWrapBreak
		msg := widget.NewLabel("Speech to text needs a local speech engine: " + err.Error() + ".\n\nRun this once in a terminal (it builds whisper.cpp and downloads a 142 MB English model into ~/.local/share/quill). Everything stays on this computer.")
		msg.Wrapping = fyne.TextWrapWord
		copyBtn := widget.NewButtonWithIcon("Copy command", theme.ContentCopyIcon(), func() {
			d.app.Clipboard().SetContent(sttSetupCommand())
		})
		dlg := dialog.NewCustom("Set up speech to text", "Close", container.NewVBox(msg, cmd, copyBtn), d.win)
		dlg.Resize(fyne.NewSize(620, 340))
		dlg.Show()
		return
	}
	stop, err := sp.record()
	if err != nil {
		dialog.ShowError(fmt.Errorf("could not start recording: %w", err), d.win)
		return
	}
	sp.stop = stop
	sp.state = sttRecording
	sp.sync()
}

func (sp *speech) finish() {
	d := sp.d
	stop := sp.stop
	sp.stop = nil
	sp.state = sttTranscribing
	sp.sync()
	go func() {
		text, err := "", error(nil)
		wav, err := stop()
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			text, err = sp.recognize(ctx, wav)
			cancel()
		}
		d.do(func() {
			sp.state = sttIdle
			sp.sync()
			if err != nil {
				dialog.ShowError(err, d.win)
				return
			}
			if text == "" {
				return
			}
			cur := strings.TrimRight(d.input.Text, " ")
			if cur != "" {
				cur += " "
			}
			d.input.SetText(cur + text)
			d.input.CursorRow = strings.Count(d.input.Text, "\n")
			d.input.CursorColumn = len([]rune(d.input.Text[strings.LastIndex(d.input.Text, "\n")+1:]))
			d.input.Refresh()
			d.win.Canvas().Focus(d.input)
		})
	}()
}

func (sp *speech) sync() {
	switch sp.state {
	case sttRecording:
		sp.btn.SetIcon(theme.MediaStopIcon())
		sp.btn.Importance = widget.DangerImportance
		sp.d.chat.setLive("● Listening... click the mic again to stop")
	case sttTranscribing:
		sp.btn.SetIcon(theme.MediaRecordIcon())
		sp.btn.Importance = widget.MediumImportance
		sp.btn.Disable()
		sp.d.chat.setLive("Turning speech into text (on this computer)...")
	default:
		sp.btn.SetIcon(theme.MediaRecordIcon())
		sp.btn.Importance = widget.LowImportance
		sp.btn.Enable()
		sp.d.chat.setLive(liveText(sp.d.current))
	}
	sp.btn.Refresh()
}
