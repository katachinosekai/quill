package main

import (
	"encoding/json"
	"net/http"
	"time"

	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/session"
)

const (
	backendOnline = "Online"
	backendLocal  = "Local"
)

var (
	claudeModels  = modelChoices
	localFallback = []string{"ollama:qwen3.8:27b-80k", "ollama:qwen3-coder:30b-48k"}
	localModes    = []string{"default", "acceptEdits", "plan"}
)

func isLocal(model string) bool {
	return session.LocalModel(model) != ""
}

func backendOf(model string) string {
	if isLocal(model) {
		return backendLocal
	}
	return backendOnline
}

func otherBackend(model string) string {
	if isLocal(model) {
		return backendOnline
	}
	return backendLocal
}

func setBackendButton(b *widget.Button, model string) {
	text, imp := backendOnline, widget.MediumImportance
	if isLocal(model) {
		text, imp = backendLocal, widget.WarningImportance
	}
	if b.Text != text || b.Importance != imp {
		b.Importance = imp
		b.SetText(text)
	}
}

func fetchOllamaModels(host string) []string {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(host + "/api/tags")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.NewDecoder(resp.Body).Decode(&tags) != nil {
		return nil
	}
	var out []string
	for _, m := range tags.Models {
		if m.Name != "" {
			out = append(out, session.LocalPrefix+m.Name)
		}
	}
	return out
}

func (d *desk) refreshLocalModels() {
	if d.localBusy {
		return
	}
	d.localBusy = true
	fetch := d.fetchLocal
	if fetch == nil {
		fetch = fetchOllamaModels
	}
	go func() {
		names := fetch(session.OllamaHost())
		d.do(func() {
			d.localBusy = false
			d.localModels = names
			d.updateChrome()
			for _, p := range d.panes {
				d.syncPaneControls(p)
			}
		})
	}()
}

func (d *desk) localModelList() []string {
	if len(d.localModels) > 0 {
		return d.localModels
	}
	return localFallback
}

func (d *desk) modelOptions(s *deskSession) []string {
	opts := claudeModels
	if isLocal(s.model) {
		opts = d.localModelList()
	}
	if !contains(opts, s.model) {
		opts = append(append([]string{}, opts...), s.model)
	}
	return opts
}

func modeOptions(s *deskSession) []string {
	if isLocal(s.model) {
		return localModes
	}
	return modeChoices
}

func setOptions(sel *widget.Select, opts []string) {
	if len(sel.Options) == len(opts) {
		same := true
		for i := range opts {
			same = same && sel.Options[i] == opts[i]
		}
		if same {
			return
		}
	}
	sel.Options = opts
	sel.Refresh()
}

func (d *desk) setBackend(s *deskSession, backend string) {
	if s == nil || backendOf(s.model) == backend {
		return
	}
	prefs := d.app.Preferences()
	var model string
	if backend == backendLocal {
		s.lastClaude = s.model
		prefs.SetString("lastClaudeModel", s.model)
		model = d.localModelList()[0]
		d.refreshLocalModels()
	} else {
		model = s.lastClaude
		if model == "" {
			model = prefs.StringWithFallback("lastClaudeModel", "default")
		}
	}
	d.setModel(s, model)
	if s.model == model {
		prefs.SetString("newModel", model)
	}
}

func (d *desk) localModeGuard(s *deskSession) {
	if isLocal(s.model) && !contains(localModes, s.mode) {
		old := s.mode
		d.setMode(s, "default")
		d.notify(s, "Permission mode changed from "+old+" to default: Claude Code's "+old+" mode doesn't work with local models.")
	}
}
