package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/session"
)

// Open-weight models (user 2026-11-21, Quill Desktop only): Ollama (pull
// suggested tool-capable models, make a larger-context copy) and any other
// server with an Anthropic-compatible API (llama.cpp, vLLM, LM Studio,
// LiteLLM...). Their models show under Local in the Online/Local menu.

type localServerConf struct {
	Name   string   `json:"name"`
	URL    string   `json:"url"`
	Key    string   `json:"key,omitempty"`
	Models []string `json:"models,omitempty"` // typed by hand; /v1/models adds to them
	Window int      `json:"window,omitempty"` // context window to tell Claude Code (0 = its default)
}

const prefLocalServers = "localServers"

func (d *desk) localServers() []localServerConf {
	var out []localServerConf
	_ = json.Unmarshal([]byte(d.app.Preferences().String(prefLocalServers)), &out)
	return out
}

func (d *desk) saveLocalServers(list []localServerConf) {
	b, _ := json.Marshal(list)
	d.app.Preferences().SetString(prefLocalServers, string(b))
	d.applyLocalServers()
	d.refreshLocalModels()
}

func (d *desk) applyLocalServers() {
	var ss []session.LocalServer
	for _, c := range d.localServers() {
		ss = append(ss, session.LocalServer{Name: c.Name, URL: c.URL, Key: c.Key})
	}
	session.SetLocalServers(ss)
}

func serverModelID(server, model string) string { return session.ServerPrefix + server + "/" + model }

// fetchServerModels asks a server for its models (GET /v1/models, the
// Anthropic and OpenAI list shape {data:[{id}]}).
func fetchServerModels(c localServerConf) []string {
	cl := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(c.URL, "/")+"/v1/models", nil)
	if err != nil {
		return nil
	}
	if c.Key != "" {
		req.Header.Set("x-api-key", c.Key)
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := cl.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, ollamaBodyLimit)).Decode(&body) != nil {
		return nil
	}
	var out []string
	for _, m := range body.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	return out
}

// serverModelInfos: every model of every local server, for the Local list.
func serverModelInfos(servers []localServerConf, fetch func(localServerConf) []string) []localInfo {
	var out []localInfo
	for _, c := range servers {
		seen := map[string]bool{}
		names := append(append([]string{}, c.Models...), fetch(c)...)
		for _, m := range names {
			m = strings.TrimSpace(m)
			if m == "" || seen[m] {
				continue
			}
			seen[m] = true
			out = append(out, localInfo{Name: serverModelID(c.Name, m), Window: c.Window})
		}
	}
	return out
}

// suggestedModels: tool-capable open-weight models for Ollama, by size. Tags
// are Ollama library names; a tag that is gone just fails to pull.
type suggestedModel struct {
	Tag, Size, RAM, Note string
}

var suggestedModels = []suggestedModel{
	{"qwen3:8b", "about 5 GB", "16 GB RAM or an 8 GB GPU", "small and quick; fine for simple edits"},
	{"qwen3:14b", "about 9 GB", "24 GB RAM or a 12 GB GPU", "better reasoning, still light"},
	{"gpt-oss:20b", "about 14 GB", "24 GB RAM or a 16 GB GPU", "good at tool use"},
	{"qwen3-coder:30b", "about 19 GB", "32 GB RAM or a 24 GB GPU", "strongest for code of these"},
}

// ollamaPull pulls a model with progress (POST /api/pull, streamed JSON).
func ollamaPull(host, tag string, progress func(status string, frac float64)) error {
	body, _ := json.Marshal(map[string]any{"model": tag, "stream": true})
	resp, err := http.Post(host+"/api/pull", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("ollama: %s %s", resp.Status, strings.TrimSpace(string(b)))
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var ev struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if ev.Error != "" {
			return fmt.Errorf("ollama: %s", ev.Error)
		}
		frac := -1.0
		if ev.Total > 0 {
			frac = float64(ev.Completed) / float64(ev.Total)
		}
		progress(ev.Status, frac)
	}
	return sc.Err()
}

// contextCopyName: "qwen3:8b" + 32 → "qwen3:8b-32k".
func contextCopyName(tag string, k int) string {
	base, size, ok := strings.Cut(tag, ":")
	if !ok {
		size = "latest"
	}
	return fmt.Sprintf("%s:%s-%dk", base, size, k)
}

// ollamaContextCopy makes a copy of a model with num_ctx k*1024 (POST /api/create).
func ollamaContextCopy(host, tag string, k int) (string, error) {
	name := contextCopyName(tag, k)
	body, _ := json.Marshal(map[string]any{"model": name, "from": tag, "parameters": map[string]any{"num_ctx": k * 1024}, "stream": false})
	resp, err := http.Post(host+"/api/create", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("ollama: %s %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return name, nil
}

// localModelsDialog: Settings → Setup → Local models.
func (d *desk) localModelsDialog() {
	host := session.OllamaHost()
	box := container.NewVBox()
	var fill func()
	fill = func() {
		box.Objects = nil
		box.Add(boldLabel("Ollama"))
		installed := map[string]bool{}
		for _, m := range d.localModels {
			if t, ok := strings.CutPrefix(m, session.LocalPrefix); ok {
				installed[t] = true
			}
		}
		if !ollamaReachable(host) {
			get := widget.NewButtonWithIcon("Get Ollama...", theme.DownloadIcon(), func() {
				if u, err := url.Parse("https://ollama.com/download"); err == nil {
					_ = d.app.OpenURL(u)
				}
			})
			msg := widget.NewLabel("Not running at " + host + ". Install it from ollama.com (or start it), then open this again.")
			msg.Wrapping = fyne.TextWrapWord
			box.Add(container.NewBorder(nil, nil, nil, get, msg))
		} else {
			box.Add(caption("Running at " + host + ". Pick its models under Local in the Online menu; only models that can use tools are listed."))
			for _, sm := range suggestedModels {
				sm := sm
				info := widget.NewLabel(sm.Tag + " · " + sm.Size + " · " + sm.RAM + " · " + sm.Note)
				info.Wrapping = fyne.TextWrapWord
				var act fyne.CanvasObject
				if installed[sm.Tag] {
					act = widget.NewLabel("installed")
				} else {
					bar := widget.NewProgressBar()
					bar.Hide()
					status := caption("")
					var pull *widget.Button
					pull = widget.NewButtonWithIcon("Pull", theme.DownloadIcon(), func() {
						pull.Disable()
						bar.Show()
						go func() {
							err := ollamaPull(host, sm.Tag, func(st string, frac float64) {
								d.do(func() {
									status.SetText(st)
									if frac >= 0 {
										bar.SetValue(frac)
									}
								})
							})
							d.do(func() {
								if err != nil {
									pull.Enable()
									bar.Hide()
									dialog.ShowError(err, d.win)
									return
								}
								d.refreshLocalModels()
								time.AfterFunc(2*time.Second, func() { d.do(fill) })
							})
						}()
					})
					act = container.NewVBox(pull, bar, status)
				}
				box.Add(container.NewBorder(nil, nil, nil, act, info))
			}
			var tags []string
			for t := range installed {
				tags = append(tags, t)
			}
			sort.Strings(tags)
			if len(tags) > 0 {
				pick := widget.NewSelect(tags, nil)
				pick.SetSelected(tags[0])
				ctx := widget.NewSelect([]string{"32k", "64k", "128k"}, nil)
				ctx.SetSelected("32k")
				mk := widget.NewButtonWithIcon("Make copy", theme.ContentCopyIcon(), func() {
					k := map[string]int{"32k": 32, "64k": 64, "128k": 128}[ctx.Selected]
					go func() {
						name, err := ollamaContextCopy(host, pick.Selected, k)
						d.do(func() {
							if err != nil {
								dialog.ShowError(err, d.win)
								return
							}
							d.refreshLocalModels()
							dialog.ShowInformation("Context copy", "Made "+name+": the same model with a "+ctx.Selected+" context window (it needs more memory).", d.win)
						})
					}()
				})
				box.Add(caption("Ollama models start with a small context window; Claude Code needs a larger one for real work."))
				box.Add(container.NewHBox(widget.NewLabel("Copy"), pick, widget.NewLabel("with a context window of"), ctx, mk))
			}
		}
		box.Add(widget.NewSeparator())
		box.Add(boldLabel("Other local servers (Anthropic-compatible API)"))
		servers := d.localServers()
		if len(servers) == 0 {
			box.Add(caption("None. llama.cpp's server, vLLM, LM Studio or LiteLLM work when they offer Claude's Messages API (/v1/messages) with tool use."))
		}
		for _, c := range servers {
			c := c
			l := widget.NewLabel(fmt.Sprintf("%s · %s · %d model(s) typed in, more from /v1/models", c.Name, c.URL, len(c.Models)))
			l.Truncation = fyne.TextTruncateEllipsis
			edit := widget.NewButtonWithIcon("Edit", theme.DocumentCreateIcon(), func() { d.localServerDialog(&c, fill) })
			rm := widget.NewButtonWithIcon("Remove", theme.DeleteIcon(), func() {
				var keep []localServerConf
				for _, x := range d.localServers() {
					if x.Name != c.Name {
						keep = append(keep, x)
					}
				}
				d.saveLocalServers(keep)
				fill()
			})
			edit.Importance, rm.Importance = widget.LowImportance, widget.LowImportance
			box.Add(container.NewBorder(nil, nil, nil, container.NewHBox(edit, rm), l))
		}
		add := widget.NewButtonWithIcon("Add server...", theme.ContentAddIcon(), func() { d.localServerDialog(nil, fill) })
		add.Importance = widget.MediumImportance
		box.Add(container.NewHBox(add))
		box.Refresh()
	}
	fill()
	dlg := dialog.NewCustom("Local models", "Close", container.NewVScroll(box), d.win)
	dlg.Resize(fyne.NewSize(820, 560))
	dlg.Show()
}

// saveLocalServer validates and stores one server (replacing old by name).
func (d *desk) saveLocalServer(oldName string, nc localServerConf) error {
	nc.Name = strings.TrimSpace(nc.Name)
	nc.URL = strings.TrimRight(strings.TrimSpace(nc.URL), "/")
	u, err := url.Parse(nc.URL)
	if nc.Name == "" || strings.ContainsAny(nc.Name, "/: ") || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("give a name without spaces, / or :, and an http(s) address")
	}
	var list []localServerConf
	for _, x := range d.localServers() {
		if x.Name == oldName || x.Name == nc.Name {
			continue
		}
		list = append(list, x)
	}
	d.saveLocalServers(append(list, nc))
	return nil
}

func (d *desk) localServerDialog(old *localServerConf, done func()) {
	c := localServerConf{}
	oldName := ""
	if old != nil {
		c, oldName = *old, old.Name
	}
	name, addr, key, models, window := widget.NewEntry(), widget.NewEntry(), widget.NewPasswordEntry(), widget.NewEntry(), widget.NewEntry()
	name.SetText(c.Name)
	name.SetPlaceHolder("llama")
	addr.SetText(c.URL)
	addr.SetPlaceHolder("http://localhost:8080")
	key.SetText(c.Key)
	key.SetPlaceHolder("(none)")
	models.SetText(strings.Join(c.Models, ", "))
	models.SetPlaceHolder("model names, if the server doesn't list them")
	if c.Window > 0 {
		window.SetText(fmt.Sprint(c.Window))
	}
	window.SetPlaceHolder("e.g. 65536 (optional)")
	dlg := dialog.NewForm("Local server", "Save", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Name", name), widget.NewFormItem("Address", addr), widget.NewFormItem("API key", key),
		widget.NewFormItem("Models", models), widget.NewFormItem("Context window", window),
		widget.NewFormItem("", caption("Claude Code talks to it as if it were Claude (ANTHROPIC_BASE_URL); the server needs the Messages API and tool use.")),
	}, func(ok bool) {
		if !ok {
			return
		}
		nc := localServerConf{Name: name.Text, URL: addr.Text, Key: key.Text}
		for _, m := range strings.Split(models.Text, ",") {
			if m = strings.TrimSpace(m); m != "" {
				nc.Models = append(nc.Models, m)
			}
		}
		fmt.Sscan(strings.TrimSpace(window.Text), &nc.Window)
		if err := d.saveLocalServer(oldName, nc); err != nil {
			dialog.ShowError(err, d.win)
			return
		}
		done()
	}, d.win)
	dlg.Resize(fyne.NewSize(620, 420))
	dlg.Show()
}
