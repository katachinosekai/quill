package whisper

import (
	"errors"
	"math"
	"sync"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
)

const (
	liveTick      = 400 * time.Millisecond
	silenceCommit = 700 * time.Millisecond
	maxPhrase     = 20 * time.Second
	keepLead      = 300 * time.Millisecond
	frameLen      = SampleRate / 20
)

type Update struct {
	Committed string
	Partial   string
	Final     bool
	Err       error
}

type Live struct {
	model  *Model
	lang   string
	update func(Update)

	mu      sync.Mutex
	buf     []float32
	floor   float64
	heard   bool
	stopped chan struct{}
	done    chan struct{}
	stopMu  sync.Once

	committed string
}

type Source interface {
	Start(push func([]float32)) error
	Stop()
}

type mic struct {
	ctx *malgo.AllocatedContext
	dev *malgo.Device
}

func NewMic() Source { return &mic{} }

func (m *mic) Start(push func([]float32)) error {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return err
	}
	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.Capture.Format = malgo.FormatF32
	cfg.Capture.Channels = 1
	cfg.SampleRate = SampleRate
	cb := malgo.DeviceCallbacks{Data: func(_, in []byte, frames uint32) {
		if len(in) < 4 {
			return
		}
		n := min(int(frames), len(in)/4)
		src := unsafe.Slice((*float32)(unsafe.Pointer(&in[0])), n)
		out := make([]float32, n)
		copy(out, src)
		push(out)
	}}
	dev, err := malgo.InitDevice(ctx.Context, cfg, cb)
	if err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		return err
	}
	if err := dev.Start(); err != nil {
		dev.Uninit()
		_ = ctx.Uninit()
		ctx.Free()
		return err
	}
	m.ctx, m.dev = ctx, dev
	return nil
}

func (m *mic) Stop() {
	if m.dev != nil {
		_ = m.dev.Stop()
		m.dev.Uninit()
		m.dev = nil
	}
	if m.ctx != nil {
		_ = m.ctx.Uninit()
		m.ctx.Free()
		m.ctx = nil
	}
}

func StartLive(model *Model, src Source, lang string, update func(Update)) (*Live, error) {
	if model == nil {
		return nil, errors.New("no speech model")
	}
	l := &Live{model: model, lang: lang, update: update, stopped: make(chan struct{}), done: make(chan struct{}), floor: 0.003}
	if err := src.Start(l.push); err != nil {
		return nil, err
	}
	go l.run(src)
	return l, nil
}

func (l *Live) push(s []float32) {
	l.mu.Lock()
	l.buf = append(l.buf, s...)
	l.mu.Unlock()
}

func (l *Live) Stop() {
	l.stopMu.Do(func() { close(l.stopped) })
	<-l.done
}

func rms(s []float32) float64 {
	if len(s) == 0 {
		return 0
	}
	var sum float64
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(len(s)))
}

func (l *Live) speechIn(s []float32) bool {
	thresh := max(0.012, l.floor*3)
	for i := 0; i+frameLen <= len(s); i += frameLen {
		r := rms(s[i : i+frameLen])
		if r < l.floor || l.floor == 0 {
			l.floor = l.floor*0.9 + r*0.1
		}
		if r > thresh {
			return true
		}
	}
	return false
}

func samplesFor(d time.Duration) int {
	return int(d.Seconds() * SampleRate)
}

func (l *Live) run(src Source) {
	defer close(l.done)
	t := time.NewTicker(liveTick)
	defer t.Stop()
	partial := ""
	for {
		select {
		case <-l.stopped:
			src.Stop()
			l.mu.Lock()
			rest := append([]float32(nil), l.buf...)
			l.buf = nil
			heard := l.heard
			l.mu.Unlock()
			if heard {
				if text, err := l.model.Transcribe(rest, l.lang); err == nil {
					partial = text
				}
			}
			l.commit(partial)
			l.update(Update{Committed: l.committed, Final: true})
			return
		case <-t.C:
		}
		l.mu.Lock()
		snap := append([]float32(nil), l.buf...)
		l.mu.Unlock()
		if !l.heard {
			if l.speechIn(snap) {
				l.heard = true
			} else {
				if drop := len(snap) - samplesFor(keepLead); drop > 0 {
					l.mu.Lock()
					l.buf = l.buf[drop:]
					l.mu.Unlock()
				}
				continue
			}
		}
		text, err := l.model.Transcribe(snap, l.lang)
		if err != nil {
			l.update(Update{Committed: l.committed, Err: err})
			continue
		}
		partial = text
		tail := samplesFor(silenceCommit)
		quiet := len(snap) > tail && !l.speechIn(snap[len(snap)-tail:])
		if quiet || len(snap) >= samplesFor(maxPhrase) {
			l.commit(partial)
			partial = ""
			l.heard = false
			l.mu.Lock()
			l.buf = l.buf[min(len(snap), len(l.buf)):]
			l.mu.Unlock()
		}
		l.update(Update{Committed: l.committed, Partial: partial})
	}
}

func (l *Live) commit(text string) {
	if text == "" {
		return
	}
	if l.committed != "" {
		l.committed += " "
	}
	l.committed += text
}
