package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"image/color"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
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
	localFallback = []string{"ollama:qwen3:8b", "ollama:gpt-oss:20b"}
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

const backendRemote = "Remote"

// setBackendButton reports whether the text changed (callers re-lay out).
// Remote Control shows in the header and as the ticked Remote in the menu,
// not on the button: a wider button pushes Voice out of the row.
func setBackendButton(b *widget.Button, s *deskSession) bool {
	text, imp := backendOnline, widget.MediumImportance
	if isLocal(s.model) {
		text, imp = backendLocal, widget.WarningImportance
	}
	if b.Text != text || b.Importance != imp {
		grew := b.Text != text
		b.Importance = imp
		b.SetText(text)
		return grew
	}
	return false
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
		d.localAgain = true // settings changed meanwhile: list again afterwards
		return
	}
	d.localBusy = true
	fetch := d.fetchLocal
	if fetch == nil {
		fetch = fetchOllamaModels
	}
	fetchSrv := d.fetchServer
	if fetchSrv == nil {
		fetchSrv = fetchServerModels
	}
	servers := d.localServers()
	go func() {
		infos := fetch(session.OllamaHost())
		infos = append(infos, serverModelInfos(servers, fetchSrv)...)
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
			if d.localAgain {
				d.localAgain = false
				d.refreshLocalModels()
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
	host := session.LocalBaseURL(s.model)
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

func ollamaReachable(host string) bool {
	u, err := url.Parse(host)
	if err != nil || u.Host == "" {
		return false
	}
	addr := u.Host
	if u.Port() == "" {
		port := "80"
		if u.Scheme == "https" {
			port = "443"
		}
		addr = net.JoinHostPort(u.Hostname(), port)
	}
	c, err := net.DialTimeout("tcp", addr, 700*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func (d *desk) checkOllama(model string) error {
	if !isLocal(model) {
		return nil
	}
	ping := d.pingLocal
	if ping == nil {
		ping = ollamaReachable
	}
	if strings.HasPrefix(model, session.ServerPrefix) {
		if host := session.LocalBaseURL(model); host == "" || !ping(host) {
			return fmt.Errorf("the local server for %s isn't answering at %s. Start it, fix it under Settings → Setup → Local models, or switch this session to Online", session.LocalModel(model), firstNonEmpty(host, "(no address)"))
		}
		return nil
	}
	if host := session.OllamaHost(); !ping(host) {
		return fmt.Errorf("Ollama isn't running at %s (GameMode stops it while a game runs). Start it or switch this session to Online", host)
	}
	return nil
}

type boxedTheme struct{ deskTheme }

func (t boxedTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if n == theme.ColorNameInputBackground {
		return t.deskTheme.Color(theme.ColorNameButton, v)
	}
	return t.deskTheme.Color(n, v)
}

func boxed(o fyne.CanvasObject) fyne.CanvasObject {
	return container.NewThemeOverride(o, boxedTheme{})
}

type tasksChip struct {
	widget.BaseWidget
	label *widget.Label
	box   fyne.CanvasObject
	onTap func()
}

func newTasksButton(session func() *deskSession, open func(*deskSession)) *tasksChip {
	c := &tasksChip{label: widget.NewLabel("")}
	c.label.Truncation = fyne.TextTruncateEllipsis
	c.label.TextStyle = fyne.TextStyle{Bold: true}
	r := canvas.NewRectangle(theme.Color(theme.ColorNameButton))
	r.CornerRadius = theme.Size(theme.SizeNameInputRadius)
	icon := widget.NewIcon(theme.MediaPlayIcon())
	c.box = container.NewStack(r, container.NewBorder(nil, nil, container.NewCenter(sized(16, 16, icon)), nil, c.label))
	c.onTap = func() {
		if s := session(); s != nil {
			open(s)
		}
	}
	c.ExtendBaseWidget(c)
	c.Hide()
	return c
}

func (c *tasksChip) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.box)
}

func (c *tasksChip) Tapped(*fyne.PointEvent) {
	if c.onTap != nil {
		c.onTap()
	}
}

func (c *tasksChip) MinSize() fyne.Size {
	return fyne.NewSize(c.natural(), c.BaseWidget.MinSize().Height)
}

func (c *tasksChip) Text() string { return c.label.Text }

func (c *tasksChip) natural() float32 {
	text := fyne.MeasureText(c.label.Text, theme.TextSize(), c.label.TextStyle).Width
	return text + 2*theme.InnerPadding() + 16 + 2*theme.Padding()
}

func (c *tasksChip) Resize(s fyne.Size) {
	c.BaseWidget.Resize(fyne.NewSize(max(s.Width, 1), max(s.Height, 1)))
}

func tasksLabel(s *deskSession) string {
	if s == nil || !s.running() {
		return ""
	}
	tasks := s.conv.BackgroundTasks()
	if len(tasks) == 0 {
		return ""
	}
	if len(tasks) == 1 {
		return "Background Task"
	}
	return "Background Tasks"
}

func syncTasksButton(b *tasksChip, s *deskSession) bool {
	if b == nil {
		return false
	}
	was := b.Visible()
	label := tasksLabel(s)
	if label == "" {
		b.Hide()
		return was
	}
	if b.label.Text != label {
		b.label.SetText(label)
	}
	b.Show()
	return !was
}

func relayoutTree(o fyne.CanvasObject) {
	c, ok := o.(*fyne.Container)
	if !ok {
		return
	}
	if c.Layout != nil {
		c.Layout.Layout(c.Objects, c.Size())
	}
	for _, x := range c.Objects {
		relayoutTree(x)
	}
	canvas.Refresh(c)
}

// backendItems is the menu of the Online/Local button under the message box:
// where the model runs (Online = Claude, Local = Ollama) and Remote, which
// turns Claude Code's Remote Control on or off for the session (control it
// from claude.ai or the Claude app; it works with either backend).
func (d *desk) backendItems(s *deskSession, after func()) []*fyne.MenuItem {
	pick := func(backend string) *fyne.MenuItem {
		label := backend + " (Claude models)"
		if backend == backendLocal {
			label = backend + " (Ollama on this PC)"
		}
		it := fyne.NewMenuItem(label, func() {
			d.setBackend(s, backend)
			after()
		})
		it.Checked = backendOf(s.model) == backend
		return it
	}
	remote := fyne.NewMenuItem(backendRemote+" (Remote Control through claude.ai)", func() {
		d.setRemoteControl(s, !s.rcOn)
		after()
	})
	remote.Checked = s.rcOn
	return []*fyne.MenuItem{pick(backendOnline), pick(backendLocal), fyne.NewMenuItemSeparator(), remote}
}

func (d *desk) showBackendMenu(s *deskSession, b *widget.Button, after func()) {
	if s == nil {
		return
	}
	if s.remote != nil {
		d.notify(s, "This session runs on another PC; change Online, Local and Remote in Quill Desktop there.")
		return
	}
	// The button sits at the bottom of the window: open the menu above it.
	menuAbove(b, d.backendItems(s, after))
}
