//go:build !windows

package whisper

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/whisper.cpp/include -I${SRCDIR}/../../third_party/whisper.cpp/ggml/include
#cgo LDFLAGS: -L${SRCDIR}/../../third_party/whisper.cpp/build/src -L${SRCDIR}/../../third_party/whisper.cpp/build/ggml/src -lwhisper -lggml -lggml-cpu -lggml-base -lstdc++ -lm -lpthread
#include <stdlib.h>
#include <whisper.h>

static void quiet_log(enum ggml_log_level level, const char * text, void * data) {}
static void quill_quiet(void) { whisper_log_set(quiet_log, NULL); }
*/
import "C"

import (
	"errors"
	"strings"
	"sync"
	"unsafe"
)

type Model struct {
	mu  sync.Mutex
	ctx *C.struct_whisper_context
}

var quietOnce sync.Once

func Load(path string) (*Model, error) {
	quietOnce.Do(func() { C.quill_quiet() })
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	params := C.whisper_context_default_params()
	params.use_gpu = false
	ctx := C.whisper_init_from_file_with_params(cpath, params)
	if ctx == nil {
		return nil, errors.New("could not load the speech model")
	}
	return &Model{ctx: ctx}, nil
}

func (m *Model) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx != nil {
		C.whisper_free(m.ctx)
		m.ctx = nil
	}
}

func (m *Model) Transcribe(samples []float32, lang string) (string, error) {
	if len(samples) < SampleRate/4 {
		return "", nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx == nil {
		return "", errors.New("speech model is closed")
	}
	p := C.whisper_full_default_params(C.WHISPER_SAMPLING_GREEDY)
	p.n_threads = C.int(Threads())
	p.translate = false
	p.no_context = true
	p.single_segment = true
	p.no_timestamps = true
	p.print_special = false
	p.print_progress = false
	p.print_realtime = false
	p.print_timestamps = false
	p.suppress_blank = true
	p.temperature_inc = 0
	secs := float64(len(samples)) / SampleRate
	if secs < 28 {
		p.audio_ctx = C.int(min(1500, int(secs*50)+128))
	}
	clang := C.CString(Language(lang))
	defer C.free(unsafe.Pointer(clang))
	p.language = clang
	if C.whisper_full(m.ctx, p, (*C.float)(unsafe.Pointer(&samples[0])), C.int(len(samples))) != 0 {
		return "", errors.New("speech recognition failed")
	}
	var b strings.Builder
	n := int(C.whisper_full_n_segments(m.ctx))
	for i := 0; i < n; i++ {
		b.WriteString(C.GoString(C.whisper_full_get_segment_text(m.ctx, C.int(i))))
	}
	return Clean(b.String()), nil
}
