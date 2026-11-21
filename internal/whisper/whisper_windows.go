//go:build windows

package whisper

import "errors"

// The Windows build has no whisper.cpp (it is linked statically from Linux
// builds only), so voice input reports itself unavailable.

var ErrUnavailable = errors.New("voice input is not available in the Windows build")

type Model struct{}

func Load(path string) (*Model, error) { return nil, ErrUnavailable }

func (m *Model) Close() {}

func (m *Model) Transcribe(samples []float32, lang string) (string, error) {
	return "", ErrUnavailable
}
