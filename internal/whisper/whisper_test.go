package whisper

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func loadWav(t *testing.T, path string) []float32 {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	i := 12
	for i+8 <= len(b) {
		id, size := string(b[i:i+4]), int(binary.LittleEndian.Uint32(b[i+4:i+8]))
		if id == "data" {
			pcm := b[i+8 : min(len(b), i+8+size)]
			out := make([]float32, len(pcm)/2)
			for k := range out {
				out[k] = float32(int16(binary.LittleEndian.Uint16(pcm[2*k:]))) / 32768
			}
			return out
		}
		i += 8 + size
	}
	t.Fatal("no data chunk")
	return nil
}

func TestTranscribeJFK(t *testing.T) {
	home, _ := os.UserHomeDir()
	model := filepath.Join(home, ".local", "share", "quill", "models", "ggml-base.en-q5_1.bin")
	if _, err := os.Stat(model); err != nil {
		t.Skip("speech model not downloaded")
	}
	m, err := Load(model)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	pcm := loadWav(t, "testdata/jfk.wav")
	start := time.Now()
	text, err := m.Transcribe(pcm, "en")
	full := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(text), "what your country can do for you") {
		t.Fatalf("transcript: %q", text)
	}
	start = time.Now()
	part, _ := m.Transcribe(pcm[:3*SampleRate], "en")
	short := time.Since(start)
	t.Logf("11s clip: %v  3s partial: %v (%q)", full, short, part)
}

type fakeSource struct {
	pcm  []float32
	stop chan struct{}
}

func (f *fakeSource) Start(push func([]float32)) error {
	f.stop = make(chan struct{})
	go func() {
		chunk := SampleRate / 10
		for i := 0; i < len(f.pcm); i += chunk {
			select {
			case <-f.stop:
				return
			case <-time.After(25 * time.Millisecond):
			}
			push(f.pcm[i:min(len(f.pcm), i+chunk)])
		}
		silence := make([]float32, SampleRate/10)
		for {
			select {
			case <-f.stop:
				return
			case <-time.After(25 * time.Millisecond):
				push(silence)
			}
		}
	}()
	return nil
}

func (f *fakeSource) Stop() { close(f.stop) }

func TestLiveStreaming(t *testing.T) {
	home, _ := os.UserHomeDir()
	model := filepath.Join(home, ".local", "share", "quill", "models", "ggml-base.en-q5_1.bin")
	if _, err := os.Stat(model); err != nil {
		t.Skip("speech model not downloaded")
	}
	m, err := Load(model)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	lead := make([]float32, SampleRate)
	src := &fakeSource{pcm: append(lead, loadWav(t, "testdata/jfk.wav")...)}
	var mu sync.Mutex
	var partials int
	var last Update
	l, err := StartLive(m, src, "en", func(u Update) {
		mu.Lock()
		defer mu.Unlock()
		if u.Partial != "" {
			partials++
		}
		last = u
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Second)
	l.Stop()
	mu.Lock()
	defer mu.Unlock()
	if !last.Final || partials < 2 {
		t.Fatalf("expected live partials and a final update: partials=%d last=%+v", partials, last)
	}
	if !strings.Contains(strings.ToLower(last.Committed), "country") {
		t.Fatalf("final text: %q", last.Committed)
	}
	t.Logf("partials %d, final %q", partials, last.Committed)
}

func TestRealMicOpens(t *testing.T) {
	if os.Getenv("QUILL_MIC_TEST") == "" {
		t.Skip("set QUILL_MIC_TEST=1 to open the real microphone")
	}
	var mu sync.Mutex
	n := 0
	var peak float64
	m := NewMic()
	if err := m.Start(func(s []float32) {
		mu.Lock()
		n += len(s)
		peak = max(peak, rms(s))
		mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	m.Stop()
	mu.Lock()
	defer mu.Unlock()
	if n < SampleRate/2 {
		t.Fatalf("captured only %d samples", n)
	}
	t.Logf("captured %d samples in 1s, peak level %.4f", n, peak)
}

func TestLanguageAutoByDefault(t *testing.T) {
	if Language("") != "auto" || Language(AutoLanguage) != "auto" {
		t.Fatal("unset and auto must both reach whisper as \"auto\"")
	}
	if Language("de") != "de" || Language("en") != "en" {
		t.Fatal("an explicit language must pass through")
	}
}
