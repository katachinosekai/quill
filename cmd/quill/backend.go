package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
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

type localInfo struct {
	Name   string
	Window int
}

const ollamaBodyLimit = 1 << 20

var numCtx = regexp.MustCompile(`(?m)^num_ctx\s+(\d+)`)

func ollamaJSON(c *http.Client, req *http.Request, v any) bool {
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	return json.NewDecoder(io.LimitReader(resp.Body, ollamaBodyLimit)).Decode(v) == nil
}

func fetchOllamaModels(host string) []localInfo {
	c := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodGet, host+"/api/tags", nil)
	if err != nil {
		return nil
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if !ollamaJSON(c, req, &tags) {
		return nil
	}
	var out []localInfo
	for _, m := range tags.Models {
		if m.Name == "" {
			continue
		}
		info := localInfo{Name: session.LocalPrefix + m.Name}
		body, _ := json.Marshal(map[string]string{"model": m.Name})
		req, err := http.NewRequest(http.MethodPost, host+"/api/show", bytes.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		var show struct {
			Capabilities *[]string `json:"capabilities"`
			Parameters   string    `json:"parameters"`
		}
		if ollamaJSON(c, req, &show) {
			if show.Capabilities != nil && !contains(*show.Capabilities, "tools") {
				continue
			}
			if m := numCtx.FindStringSubmatch(show.Parameters); m != nil {
				info.Window, _ = strconv.Atoi(m[1])
			}
		}
		out = append(out, info)
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
		infos := fetch(session.OllamaHost())
		d.do(func() {
			d.localBusy = false
			d.localModels = nil
			for _, in := range infos {
				d.localModels = append(d.localModels, in.Name)
				if in.Window > 0 {
					d.localWindows[in.Name] = in.Window
				}
			}
			d.updateChrome()
			for _, p := range d.panes {
				d.syncPaneControls(p)
			}
		})
	}()
}

func (d *desk) defaultLocal() string {
	list := d.localModelList()
	for _, m := range localFallback {
		if contains(list, m) {
			return m
		}
	}
	return list[0]
}

func (d *desk) localEnv(model string) []string {
	n := d.localWindows[model]
	if !isLocal(model) || n <= 0 {
		return nil
	}
	return []string{fmt.Sprintf("CLAUDE_CODE_MAX_CONTEXT_TOKENS=%d", n), fmt.Sprintf("CLAUDE_CODE_AUTO_COMPACT_WINDOW=%d", n)}
}

func (d *desk) warnInsecureOllama(s *deskSession) {
	host := session.OllamaHost()
	if !isLocal(s.model) || d.warnedOllama || !session.OllamaInsecure(host) {
		return
	}
	d.warnedOllama = true
	d.notify(s, "This local session talks to "+host+" over plain http: code, file contents and tool output cross the network unencrypted. Use https or a local Ollama if that network isn't trusted.")
}

func (d *desk) rcForLocal() bool {
	return d.app.Preferences().BoolWithFallback("rcLocal", true)
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
		model = d.defaultLocal()
		if d.autoRC && d.rcForLocal() {
			d.notify(s, "Remote Control is on, so this local session is still relayed through claude.ai. Turn off \"Remote Control for local sessions\" in the sidebar settings to keep it on this machine.")
		}
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
