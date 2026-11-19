package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"io"
	"os/exec"
	"strconv"
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

const (
	scrimAlpha = 115
	videoFPS   = 30
	videoMaxW  = 1280
)

type lightbox struct {
	widget.BaseWidget
	scrim   *canvas.Rectangle
	content fyne.CanvasObject
	onClose func()
}

func (l *lightbox) CreateRenderer() fyne.WidgetRenderer {
	return &lightboxRenderer{l: l}
}

func (l *lightbox) Tapped(*fyne.PointEvent) { l.onClose() }

type lightboxRenderer struct{ l *lightbox }

func (r *lightboxRenderer) Layout(size fyne.Size) {
	r.l.scrim.Resize(size)
	r.l.scrim.Move(fyne.NewPos(0, 0))
	m := r.l.content.MinSize()
	w := min(max(m.Width, size.Width*0.8), size.Width-32)
	h := min(max(m.Height, size.Height*0.8), size.Height-32)
	r.l.content.Resize(fyne.NewSize(w, h))
	r.l.content.Move(fyne.NewPos((size.Width-w)/2, (size.Height-h)/2))
}

func (r *lightboxRenderer) MinSize() fyne.Size { return fyne.NewSize(0, 0) }
func (r *lightboxRenderer) Refresh()           { r.l.scrim.Refresh(); r.l.content.Refresh() }
func (r *lightboxRenderer) Destroy()           {}
func (r *lightboxRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.l.scrim, r.l.content}
}

type absorb struct {
	widget.BaseWidget
	content fyne.CanvasObject
}

func newAbsorb(c fyne.CanvasObject) *absorb {
	a := &absorb{content: c}
	a.ExtendBaseWidget(a)
	return a
}

func (a *absorb) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(a.content) }
func (a *absorb) Tapped(*fyne.PointEvent)             {}

func (d *desk) showLightbox(content fyne.CanvasObject, onClose func()) func() {
	c := d.win.Canvas()
	if d.lightboxClose != nil {
		d.lightboxClose()
	}
	c.Unfocus()
	lb := &lightbox{scrim: canvas.NewRectangle(color.NRGBA{0, 0, 0, scrimAlpha}), content: newAbsorb(content)}
	lb.ExtendBaseWidget(lb)
	prev := c.OnTypedKey()
	closed := false
	closeFn := func() {
		if closed {
			return
		}
		closed = true
		c.Overlays().Remove(lb)
		c.SetOnTypedKey(prev)
		if d.input != nil && !d.hidden {
			c.Focus(d.input)
		}
		if d.lightboxClose != nil {
			d.lightboxClose = nil
		}
		if onClose != nil {
			onClose()
		}
	}
	lb.onClose = closeFn
	c.SetOnTypedKey(func(k *fyne.KeyEvent) {
		if k.Name == fyne.KeyEscape {
			closeFn()
			return
		}
		if prev != nil {
			prev(k)
		}
	})
	d.lightboxClose = closeFn
	c.Overlays().Add(lb)
	lb.Resize(c.Size())
	return closeFn
}

func (d *desk) viewMedia(m mediaRef, preview fyne.Resource) {
	var closeFn func()
	head := func(extra ...fyne.CanvasObject) fyne.CanvasObject {
		name := canvas.NewText(m.name(), colText)
		name.TextStyle.Bold = true
		ext := widget.NewButtonWithIcon("", theme.ComputerIcon(), func() {
			if closeFn != nil {
				closeFn()
			}
			d.app.OpenURL(fileURL(m.src))
		})
		ext.Importance = widget.LowImportance
		x := widget.NewButtonWithIcon("", theme.CancelIcon(), func() { closeFn() })
		x.Importance = widget.LowImportance
		items := append([]fyne.CanvasObject{container.NewCenter(name), layout.NewSpacer()}, extra...)
		return container.NewHBox(append(items, ext, x)...)
	}
	switch m.kind {
	case mediaVideo:
		vp := newVideoPlayer(d, m.src, preview)
		body := container.NewBorder(head(), vp.controls(), nil, nil, vp.view())
		closeFn = d.showLightbox(bg(colCard, 12, container.NewPadded(body)), vp.close)
		vp.play()
	default:
		img := canvas.NewImageFromResource(preview)
		if preview == nil {
			img.File = m.src
		}
		img.FillMode = canvas.ImageFillContain
		img.ScaleMode = canvas.ImageScaleSmooth
		body := container.NewBorder(head(), nil, nil, nil, img)
		closeFn = d.showLightbox(bg(colCard, 12, container.NewPadded(body)), nil)
	}
}

type videoInfo struct {
	w, h     int
	duration float64
	audio    bool
}

func probeVideo(ctx context.Context, src string) (videoInfo, error) {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "stream=codec_type,width,height:format=duration", "-of", "json", src).Output()
	if err != nil {
		return videoInfo{}, fmt.Errorf("ffprobe: %w", err)
	}
	var p struct {
		Streams []struct {
			Type   string `json:"codec_type"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return videoInfo{}, err
	}
	var vi videoInfo
	for _, s := range p.Streams {
		switch s.Type {
		case "video":
			if vi.w == 0 {
				vi.w, vi.h = s.Width, s.Height
			}
		case "audio":
			vi.audio = true
		}
	}
	if vi.w == 0 || vi.h == 0 {
		return vi, fmt.Errorf("no video stream")
	}
	vi.duration, _ = strconv.ParseFloat(p.Format.Duration, 64)
	if vi.w > videoMaxW {
		vi.h = vi.h * videoMaxW / vi.w
		vi.w = videoMaxW
	}
	vi.w, vi.h = vi.w&^1, vi.h&^1
	return vi, nil
}

type videoPlayer struct {
	d       *desk
	src     string
	img     *canvas.Image
	status  *canvas.Text
	btn     *widget.Button
	seek    *widget.Slider
	timeLbl *canvas.Text

	mu      sync.Mutex
	info    videoInfo
	probed  bool
	pos     float64
	playing bool
	gen     int
	cancel  context.CancelFunc
	audio   *exec.Cmd
	closed  bool
	seeking bool
}

func newVideoPlayer(d *desk, src string, preview fyne.Resource) *videoPlayer {
	vp := &videoPlayer{d: d, src: src}
	vp.img = canvas.NewImageFromResource(preview)
	vp.img.FillMode = canvas.ImageFillContain
	vp.img.ScaleMode = canvas.ImageScaleFastest
	vp.img.SetMinSize(fyne.NewSize(320, 180))
	vp.status = mutedText("", 12)
	vp.btn = widget.NewButtonWithIcon("", theme.MediaPlayIcon(), vp.toggle)
	vp.seek = widget.NewSlider(0, 1)
	vp.seek.Step = 0.01
	vp.seek.OnChanged = func(float64) { vp.seeking = true }
	vp.seek.OnChangeEnded = func(v float64) {
		vp.seeking = false
		vp.seekTo(v)
	}
	vp.timeLbl = mutedText("0:00", 12)
	return vp
}

func (vp *videoPlayer) view() fyne.CanvasObject {
	return container.NewStack(canvas.NewRectangle(color.Black), vp.img, container.NewCenter(vp.status))
}

func (vp *videoPlayer) controls() fyne.CanvasObject {
	return container.NewBorder(nil, nil, vp.btn, container.NewCenter(vp.timeLbl), vp.seek)
}

func clock(sec float64) string {
	s := int(sec + 0.5)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func (vp *videoPlayer) toggle() {
	vp.mu.Lock()
	on := vp.playing
	vp.mu.Unlock()
	if on {
		vp.pause()
	} else {
		vp.play()
	}
}

func (vp *videoPlayer) seekTo(v float64) {
	vp.mu.Lock()
	vp.pos = v
	on := vp.playing
	vp.mu.Unlock()
	if on {
		vp.stopStreams()
		vp.play()
	} else {
		vp.syncUI()
	}
}

func (vp *videoPlayer) stopStreams() {
	vp.mu.Lock()
	defer vp.mu.Unlock()
	vp.gen++
	if vp.cancel != nil {
		vp.cancel()
		vp.cancel = nil
	}
	if vp.audio != nil && vp.audio.Process != nil {
		_ = vp.audio.Process.Kill()
		go func(c *exec.Cmd) { _ = c.Wait() }(vp.audio)
		vp.audio = nil
	}
	vp.playing = false
}

func (vp *videoPlayer) pause() {
	vp.stopStreams()
	vp.syncUI()
}

func (vp *videoPlayer) close() {
	vp.stopStreams()
	vp.mu.Lock()
	vp.closed = true
	vp.mu.Unlock()
}

func (vp *videoPlayer) syncUI() {
	vp.mu.Lock()
	on, pos, dur := vp.playing, vp.pos, vp.info.duration
	vp.mu.Unlock()
	if on {
		vp.btn.SetIcon(theme.MediaPauseIcon())
	} else {
		vp.btn.SetIcon(theme.MediaPlayIcon())
	}
	if dur > 0 {
		if vp.seek.Max != dur {
			vp.seek.Max = dur
		}
		if !vp.seeking {
			vp.seek.Value = min(pos, dur)
			vp.seek.Refresh()
		}
		setText(vp.timeLbl, clock(pos)+" / "+clock(dur))
	} else {
		setText(vp.timeLbl, clock(pos))
	}
}

func (vp *videoPlayer) play() {
	vp.mu.Lock()
	if vp.closed || vp.playing {
		vp.mu.Unlock()
		return
	}
	if vp.info.duration > 0 && vp.pos >= vp.info.duration-0.05 {
		vp.pos = 0
	}
	vp.gen++
	gen := vp.gen
	ctx, cancel := context.WithCancel(context.Background())
	vp.cancel = cancel
	vp.playing = true
	start := vp.pos
	probed, info := vp.probed, vp.info
	vp.mu.Unlock()
	vp.syncUI()
	go func() {
		if !probed {
			setStatus := func(s string) { vp.d.do(func() { setText(vp.status, s) }) }
			setStatus("Loading...")
			pctx, pcancel := context.WithTimeout(ctx, 15*time.Second)
			i, err := probeVideo(pctx, vp.src)
			pcancel()
			if err != nil {
				if ctx.Err() == nil {
					setStatus("Cannot play: " + oneLine(err.Error(), 60))
					vp.stopStreams()
					vp.d.do(vp.syncUI)
				}
				return
			}
			vp.mu.Lock()
			vp.info, vp.probed = i, true
			vp.mu.Unlock()
			info = i
			setStatus("")
		}
		vp.stream(ctx, gen, start, info)
	}()
}

func (vp *videoPlayer) stream(ctx context.Context, gen int, start float64, info videoInfo) {
	args := []string{"-loglevel", "error", "-re", "-ss", strconv.FormatFloat(start, 'f', 2, 64), "-i", vp.src,
		"-an", "-vf", fmt.Sprintf("scale=%d:%d,fps=%d", info.w, info.h, videoFPS), "-f", "rawvideo", "-pix_fmt", "rgba", "-"}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		vp.d.do(func() { setText(vp.status, "Cannot play: ffmpeg is not installed") })
		vp.stopStreams()
		vp.d.do(vp.syncUI)
		return
	}
	if info.audio {
		a := exec.Command("mpv", "--no-video", "--really-quiet", "--no-terminal", "--start="+strconv.FormatFloat(start, 'f', 2, 64), vp.src)
		a.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
		if a.Start() == nil {
			vp.mu.Lock()
			if vp.gen == gen {
				vp.audio = a
			} else {
				_ = a.Process.Kill()
				go func() { _ = a.Wait() }()
			}
			vp.mu.Unlock()
		}
	}
	frame := info.w * info.h * 4
	r := bufio.NewReaderSize(out, frame)
	n := 0
	var pending sync.Mutex
	busy := false
	scratch := make([]byte, frame)
	for {
		pending.Lock()
		skip := busy
		pending.Unlock()
		buf := scratch
		if !skip {
			buf = make([]byte, frame)
		}
		if _, err := io.ReadFull(r, buf); err != nil {
			break
		}
		n++
		pos := start + float64(n)/videoFPS
		if skip {
			continue
		}
		pending.Lock()
		busy = true
		pending.Unlock()
		img := &image.RGBA{Pix: buf, Stride: info.w * 4, Rect: image.Rect(0, 0, info.w, info.h)}
		vp.d.do(func() {
			pending.Lock()
			busy = false
			pending.Unlock()
			vp.mu.Lock()
			live := vp.gen == gen
			if live {
				vp.pos = pos
			}
			vp.mu.Unlock()
			if !live {
				return
			}
			vp.img.Resource = nil
			vp.img.File = ""
			vp.img.Image = img
			vp.img.Refresh()
			vp.syncUI()
		})
	}
	_ = cmd.Wait()
	vp.mu.Lock()
	ended := vp.gen == gen && ctx.Err() == nil
	vp.mu.Unlock()
	if ended && n == 0 {
		vp.stopStreams()
		vp.d.do(func() {
			setText(vp.status, "Cannot play this video")
			vp.syncUI()
		})
		return
	}
	if ended {
		vp.stopStreams()
		vp.mu.Lock()
		if vp.info.duration > 0 {
			vp.pos = vp.info.duration
		}
		vp.mu.Unlock()
		vp.d.do(vp.syncUI)
	}
}
