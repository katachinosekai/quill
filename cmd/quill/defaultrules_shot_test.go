package main

import (
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
)

func TestDefaultRulesShot(t *testing.T) {
	if os.Getenv("DESK_SHOTS") == "" {
		t.Skip("set DESK_SHOTS=1")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KMEM_CONFIG", "")
	dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
	os.MkdirAll(dir, 0o755)
	h := newHarness(t)
	h.ui(func() {
		dlg := dialog.NewCustom("Memory (kmem)", "Close", h.d.kmemSettings(), h.w)
		dlg.Resize(fyne.NewSize(760, 360))
		dlg.Show()
	})
	h.ui(func() { shot(t, h.w, dir, "default-rules-option.png") })
}
