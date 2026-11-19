package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type mediaKind int

const (
	mediaImage mediaKind = iota + 1
	mediaAudio
	mediaVideo
)

const (
	maxMediaRefs  = 6
	maxImageBytes = 25 << 20
	previewW      = 420
	previewH      = 280
)

var mediaExt = map[string]mediaKind{
	".png": mediaImage, ".jpg": mediaImage, ".jpeg": mediaImage, ".gif": mediaImage, ".webp": mediaImage, ".bmp": mediaImage, ".svg": mediaImage,
	".mp3": mediaAudio, ".wav": mediaAudio, ".ogg": mediaAudio, ".oga": mediaAudio, ".flac": mediaAudio, ".m4a": mediaAudio, ".opus": mediaAudio, ".aac": mediaAudio,
	".mp4": mediaVideo, ".webm": mediaVideo, ".mkv": mediaVideo, ".mov": mediaVideo, ".avi": mediaVideo, ".m4v": mediaVideo,
}

type mediaRef struct {
	src  string
	kind mediaKind
}

func (m mediaRef) remote() bool {
	return strings.HasPrefix(m.src, "http://") || strings.HasPrefix(m.src, "https://")
}

func (m mediaRef) name() string {
	p := m.src
	if u, err := url.Parse(p); err == nil && m.remote() {
		p = u.Path
	}
	if b := filepath.Base(p); b != "" && b != "." && b != "/" {
		return b
	}
	return m.src
}

var mediaRe = regexp.MustCompile("https?://[^\\s<>\"'`()\\[\\]]+|(?:~|/)[^\\s<>\"'`()\\[\\]*]+")

func mediaKindOf(src string) mediaKind {
	p := src
	if u, err := url.Parse(src); err == nil && u.Scheme != "" {
		p = u.Path
	}
	return mediaExt[strings.ToLower(filepath.Ext(p))]
}

func findMedia(text string) []mediaRef {
	var out []mediaRef
	seen := map[string]bool{}
	for _, m := range mediaRe.FindAllString(text, -1) {
		m = strings.TrimRight(m, ".,;:!?")
		k := mediaKindOf(m)
		if k == 0 {
			continue
		}
		if strings.HasPrefix(m, "~") {
			home, _ := os.UserHomeDir()
			m = home + m[1:]
		}
		if !strings.HasPrefix(m, "http") {
			if st, err := os.Stat(m); err != nil || st.IsDir() {
				continue
			}
		}
		if seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, mediaRef{src: m, kind: k})
		if len(out) == maxMediaRefs {
			break
		}
	}
	return out
}

func mediaCacheDir() string {
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "quill", "media")
}

var (
	fetchMu   sync.Mutex
	fetchWait = map[string][]func(string, error){}
)

func cachedFetch(src string, done func(string, error)) {
	sum := sha256.Sum256([]byte(src))
	ext := strings.ToLower(filepath.Ext(mediaRef{src: src}.name()))
	dst := filepath.Join(mediaCacheDir(), hex.EncodeToString(sum[:12])+ext)
	if _, err := os.Stat(dst); err == nil {
		done(dst, nil)
		return
	}
	fetchMu.Lock()
	if waiters, ok := fetchWait[src]; ok {
		fetchWait[src] = append(waiters, done)
		fetchMu.Unlock()
		return
	}
	fetchWait[src] = []func(string, error){done}
	fetchMu.Unlock()
	go func() {
		err := download(src, dst)
		fetchMu.Lock()
		waiters := fetchWait[src]
		delete(fetchWait, src)
		fetchMu.Unlock()
		for _, w := range waiters {
			w(dst, err)
		}
	}()
}

func download(src, dst string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxImageBytes+1))
	f.Close()
	if err == nil && n > maxImageBytes {
		err = fmt.Errorf("larger than %d MB", maxImageBytes>>20)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func videoThumb(src string) (string, error) {
	st, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", src, st.Size(), st.ModTime().UnixNano())))
	dst := filepath.Join(mediaCacheDir(), "thumb-"+hex.EncodeToString(sum[:12])+".png")
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error", "-y", "-ss", "1", "-i", src, "-frames:v", "1", "-vf", fmt.Sprintf("scale=%d:-2", previewW*2), dst)
	if b, err := cmd.CombinedOutput(); err != nil {
		cmd = exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error", "-y", "-i", src, "-frames:v", "1", "-vf", fmt.Sprintf("scale=%d:-2", previewW*2), dst)
		if b2, err2 := cmd.CombinedOutput(); err2 != nil {
			return "", fmt.Errorf("%s %s", strings.TrimSpace(string(b)), strings.TrimSpace(string(b2)))
		}
	}
	return dst, nil
}

type player struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	src  string
	btns map[string][]*widget.Button
}

var audioPlayer = &player{btns: map[string][]*widget.Button{}}

func (p *player) playing(src string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cmd != nil && p.src == src
}

func (p *player) toggle(d *desk, src string) {
	p.mu.Lock()
	if p.cmd != nil {
		_ = p.cmd.Process.Kill()
		was := p.src
		p.cmd, p.src = nil, ""
		p.mu.Unlock()
		p.sync(d, was)
		if was == src {
			return
		}
		p.mu.Lock()
	}
	cmd := exec.Command("mpv", "--no-video", "--really-quiet", "--no-terminal", src)
	if err := cmd.Start(); err != nil {
		p.mu.Unlock()
		d.app.OpenURL(fileURL(src))
		return
	}
	p.cmd, p.src = cmd, src
	p.mu.Unlock()
	p.sync(d, src)
	go func() {
		_ = cmd.Wait()
		p.mu.Lock()
		if p.cmd == cmd {
			p.cmd, p.src = nil, ""
		}
		p.mu.Unlock()
		d.do(func() { p.sync(d, src) })
	}()
}

func (p *player) sync(d *desk, src string) {
	on := p.playing(src)
	for _, b := range p.btns[src] {
		if on {
			b.SetIcon(theme.MediaStopIcon())
			b.SetText("Stop")
		} else {
			b.SetIcon(theme.MediaPlayIcon())
			b.SetText("Play")
		}
	}
}

func fileURL(src string) *url.URL {
	if u, err := url.Parse(src); err == nil && u.Scheme != "" {
		return u
	}
	return &url.URL{Scheme: "file", Path: src}
}

func (d *desk) openMedia(m mediaRef) {
	if m.kind == mediaVideo {
		if err := exec.Command("mpv", "--force-window=yes", "--no-terminal", m.src).Start(); err == nil {
			return
		}
	}
	d.app.OpenURL(fileURL(m.src))
}

func (v *chatView) mediaPreviews(text string) fyne.CanvasObject {
	refs := findMedia(text)
	if len(refs) == 0 {
		return nil
	}
	var cards []fyne.CanvasObject
	for _, m := range refs {
		cards = append(cards, v.mediaCard(m))
	}
	return container.New(&flowLayout{gap: 10, center: v.d.center}, cards...)
}

func (v *chatView) mediaCard(m mediaRef) fyne.CanvasObject {
	d := v.d
	caption := mutedText(oneLine(m.name(), 48), 11)
	switch m.kind {
	case mediaAudio:
		btn := widget.NewButtonWithIcon("Play", theme.MediaPlayIcon(), nil)
		btn.OnTapped = func() { audioPlayer.toggle(d, m.src) }
		audioPlayer.btns[m.src] = append(audioPlayer.btns[m.src], btn)
		audioPlayer.sync(d, m.src)
		open := widget.NewButtonWithIcon("", theme.MediaMusicIcon(), func() { d.openMedia(m) })
		open.Importance = widget.LowImportance
		row := container.NewHBox(btn, container.NewCenter(caption), open)
		return bg(colCard, 10, container.NewPadded(row))
	}
	img := canvas.NewImageFromResource(nil)
	img.FillMode = canvas.ImageFillContain
	img.ScaleMode = canvas.ImageScaleSmooth
	img.SetMinSize(fyne.NewSize(previewW, previewH))
	status := mutedText("Loading preview...", 11)
	set := func(path string, err error) {
		d.do(func() {
			if err != nil {
				status.Text = "No preview: " + oneLine(err.Error(), 60)
				status.Refresh()
				return
			}
			img.File = path
			img.Refresh()
			status.Text = ""
			status.Refresh()
		})
	}
	switch {
	case m.kind == mediaImage && m.remote():
		cachedFetch(m.src, set)
	case m.kind == mediaImage:
		img.File = m.src
		status.Text = ""
	case m.kind == mediaVideo && m.remote():
		status.Text = "Click to play"
	default:
		go func() { set(videoThumb(m.src)) }()
	}
	stack := []fyne.CanvasObject{canvas.NewRectangle(colCard), img, container.NewCenter(status)}
	if m.kind == mediaVideo {
		play := widget.NewIcon(theme.MediaPlayIcon())
		ring := canvas.NewCircle(fade(colText, 0.25))
		stack = append(stack, container.NewCenter(container.NewStack(sized(56, 56, ring), container.NewCenter(sized(36, 36, play)))))
	}
	card := container.NewStack(stack...)
	foot := container.NewHBox(caption, layout.NewSpacer())
	return newTapRow(container.NewVBox(card, foot), func() { d.openMedia(m) })
}
