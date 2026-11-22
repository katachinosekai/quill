package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"

	"github.com/katachinosekai/quill/internal/chat"
)

// TestDocShots renders the README previews from the demo sessions only:
// DOC_SHOTS=1 go test -tags wayland -run TestDocShots ./cmd/quill
// writes docs/screenshots/{landscape,portrait,plan-card}.png.
func TestDocShots(t *testing.T) {
	if os.Getenv("DOC_SHOTS") == "" {
		t.Skip("set DOC_SHOTS=1 to render the README screenshots")
	}
	dir, _ := filepath.Abs(filepath.Join("..", "..", "docs", "screenshots"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	var other *deskSession
	h.ui(func() {
		h.d.app.Preferences().SetBool(prefAnimateText, false)
		other = h.d.createSession(h.proj, "default", "default")
		h.d.renameSession(other.id, "Release notes")
		h.d.open(editsID)
	})
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	h.ui(func() {
		s := h.d.sessions[editsID]
		r := s.conv.AddNotice("")
		r.Kind = chat.Assistant
		r.Text = "## Done\n\nThe greeting now says **hello**, and the test in `main_test.go` passes.\n\n- `main.go`: greeting renamed\n- `main_test.go`: expectation updated"
		h.d.chat.show(s)
		h.d.chat.update([]*chat.Item{r})
	})
	snap := func(name string, size fyne.Size) {
		h.ui(func() { h.w.Resize(size) })
		time.Sleep(400 * time.Millisecond)
		h.ui(func() {
			h.d.chat.scrollBottom()
			for _, o := range h.w.Canvas().Overlays().List() {
				h.w.Canvas().Overlays().Remove(o)
			}
		})
		time.Sleep(200 * time.Millisecond)
		h.ui(func() { shot(t, h.w, dir, name) })
	}
	h.ui(func() { h.d.setLayoutMode(layoutHorizontal) })
	snap("landscape.png", fyne.NewSize(1400, 880))
	h.ui(func() { h.d.setLayoutMode(layoutVertical) })
	snap("portrait.png", fyne.NewSize(1400, 880))

	h.ui(func() {
		h.d.setLayoutMode(layoutHorizontal)
		s := h.d.sessions[editsID]
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, InPlan: true, Text: "Plan:\n1. Bundle the new release.\n2. Run the tests.\n3. Publish it.\n\n" +
			"- a. Release notes\n- b. Screenshots\n- c. Changelog\n\n" +
			"1. Should it ship now (recommended), or wait for the Android app?\n2. Should the old download links stay?\n3. Which of a–c should I update?\n4. What should the release be called?\n5. Do you approve steps 1–3?"})
		s.conv.AddPlan()
		h.d.chat.show(s)
	})
	snap("plan-card.png", fyne.NewSize(1400, 1100))
}
