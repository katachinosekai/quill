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
