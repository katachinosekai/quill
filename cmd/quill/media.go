package main

import (
	"context"
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
	"syscall"
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
	previewW      = 200
	previewH      = 124
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

const memCacheBytes = 96 << 20

type memEntry struct {
	src  string
	data []byte
}

var (
	fetchMu   sync.Mutex
	fetchWait = map[string][]func(fyne.Resource, error){}
	memCache  []memEntry
	memSize   int
)

func memGet(src string) fyne.Resource {
	for i, e := range memCache {
		if e.src == src {
			memCache = append(append(memCache[:i:i], memCache[i+1:]...), e)
			return fyne.NewStaticResource(mediaRef{src: src}.name(), e.data)
		}
	}
	return nil
}

func memPut(src string, data []byte) {
	memCache = append(memCache, memEntry{src, data})
	memSize += len(data)
	for memSize > memCacheBytes && len(memCache) > 1 {
		memSize -= len(memCache[0].data)
		memCache = memCache[1:]
	}
}

func fetchImage(src string, done func(fyne.Resource, error)) {
	fetchMu.Lock()
	if r := memGet(src); r != nil {
		fetchMu.Unlock()
		done(r, nil)
		return
	}
	if waiters, ok := fetchWait[src]; ok {
		fetchWait[src] = append(waiters, done)
		fetchMu.Unlock()
		return
	}
	fetchWait[src] = []func(fyne.Resource, error){done}
	fetchMu.Unlock()
	go func() {
		data, err := download(src)
		fetchMu.Lock()
		var r fyne.Resource
		if err == nil {
			memPut(src, data)
			r = fyne.NewStaticResource(mediaRef{src: src}.name(), data)
		}
		waiters := fetchWait[src]
		delete(fetchWait, src)
		fetchMu.Unlock()
		for _, w := range waiters {
			w(r, err)
		}
	}()
}

func download(src string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, err
	}
	resp, err := previewClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("larger than %d MB", maxImageBytes>>20)
	}
	return data, nil
}

func videoThumb(src string) (fyne.Resource, error) {
	st, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("thumb|%s|%d|%d", src, st.Size(), st.ModTime().UnixNano())
	fetchMu.Lock()
	r := memGet(key)
	fetchMu.Unlock()
	if r != nil {
		return r, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	grab := func(seek bool) ([]byte, error) {
		args := []string{"-loglevel", "error"}
		if seek {
			args = append(args, "-ss", "1")
		}
		args = append(args, "-i", src, "-frames:v", "1", "-vf", "scale=640:-2", "-f", "image2pipe", "-vcodec", "png", "-")
		return exec.CommandContext(ctx, "ffmpeg", args...).Output()
	}
	data, err := grab(true)
	if err != nil || len(data) == 0 {
		if data, err = grab(false); err != nil {
			return nil, err
		}
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("no video frame")
	}
	fetchMu.Lock()
	memPut(key, data)
	fetchMu.Unlock()
	return fyne.NewStaticResource(filepath.Base(src)+".png", data), nil
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
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

func (p *player) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil {
		_ = p.cmd.Process.Kill()
		p.cmd, p.src = nil, ""
	}
}

func (d *desk) stopMedia() {
	if d.lightboxClose != nil {
		d.lightboxClose()
	}
	audioPlayer.stop()
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
	caption := mutedText(oneLine(m.name(), 30), 11)
	switch m.kind {
	case mediaAudio:
		btn := widget.NewButtonWithIcon("Play", theme.MediaPlayIcon(), nil)
		btn.OnTapped = func() { audioPlayer.toggle(d, m.src) }
		bs := append(audioPlayer.btns[m.src], btn)
		if len(bs) > 4 {
			bs = bs[len(bs)-4:]
		}
		audioPlayer.btns[m.src] = bs
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
	fail := func(err error) {
		status.Text = "No preview: " + oneLine(err.Error(), 60)
		status.Refresh()
	}
	var loaded fyne.Resource
	set := func(r fyne.Resource, err error) {
		d.do(func() {
			if err != nil {
				fail(err)
				return
			}
			loaded = r
			img.Resource = r
			img.Refresh()
			status.Text = ""
			status.Refresh()
		})
	}
	switch {
	case m.kind == mediaImage && m.remote():
		fetchImage(m.src, set)
	case m.kind == mediaImage:
		img.File = m.src
		status.Text = ""
	case m.kind == mediaVideo && m.remote():
		status.Text = "Play"
	default:
		go func() { set(videoThumb(m.src)) }()
	}
	stack := []fyne.CanvasObject{canvas.NewRectangle(colCard), img, container.NewCenter(status)}
	if m.kind == mediaVideo {
		play := widget.NewIcon(theme.MediaPlayIcon())
		ring := canvas.NewCircle(fade(colText, 0.25))
		stack = append(stack, container.NewCenter(container.NewStack(sized(40, 40, ring), container.NewCenter(sized(26, 26, play)))))
	}
	card := container.NewStack(stack...)
	foot := container.NewHBox(caption, layout.NewSpacer())
	return newTapRow(container.NewVBox(card, foot), func() { d.viewMedia(m, loaded) })
}
