package whisper

import (
	"runtime"
	"strings"
)

const SampleRate = 16000

func Threads() int {
	return max(1, min(runtime.NumCPU()/2, 6))
}

func Clean(s string) string {
	var out []string
	for _, w := range strings.Fields(s) {
		if strings.HasPrefix(w, "[") && strings.HasSuffix(w, "]") || strings.HasPrefix(w, "(") && strings.HasSuffix(w, ")") {
			continue
		}
		out = append(out, w)
	}
	return strings.Join(out, " ")
}

// AutoLanguage asks whisper to detect the spoken language (multilingual
// models only).
const AutoLanguage = "auto"

// Language is the code handed to whisper: an unset language means
// auto-detect, never a silent English.
func Language(lang string) string {
	if lang == "" {
		return AutoLanguage
	}
	return lang
}
