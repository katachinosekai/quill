package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/katachinosekai/quill/internal/session"
)

func TestLocalServerModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "sekrit" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"data":[{"id":"glm-4.6"},{"id":"qwen3-coder"}]}`))
	}))
	defer srv.Close()
	c := localServerConf{Name: "llama", URL: srv.URL, Key: "sekrit", Models: []string{"qwen3-coder", "hand-typed"}, Window: 65536}
	if got := fetchServerModels(c); strings.Join(got, ",") != "glm-4.6,qwen3-coder" {
		t.Fatalf("/v1/models: %v", got)
	}
	infos := serverModelInfos([]localServerConf{c}, fetchServerModels)
	var names []string
	for _, in := range infos {
		names = append(names, in.Name)
		if in.Window != 65536 {
			t.Fatal("the server's window goes with its models")
		}
	}
	if strings.Join(names, ",") != "local:llama/qwen3-coder,local:llama/hand-typed,local:llama/glm-4.6" {
		t.Fatalf("typed and listed models, once each: %v", names)
	}

	session.SetLocalServers([]session.LocalServer{{Name: "llama", URL: srv.URL + "/", Key: "sekrit"}})
	defer session.SetLocalServers(nil)
	env := strings.Join(session.LocalEnv("local:llama/glm-4.6"), " ")
	for _, want := range []string{"ANTHROPIC_BASE_URL=" + srv.URL + " ", "ANTHROPIC_AUTH_TOKEN=sekrit", "ANTHROPIC_SMALL_FAST_MODEL=glm-4.6"} {
		if !strings.Contains(env+" ", want) {
			t.Fatalf("server model env misses %q: %s", want, env)
		}
	}
	if session.LocalModel("local:llama/glm-4.6") != "glm-4.6" || session.LocalModel("opus") != "" || !isLocal("local:llama/glm-4.6") {
		t.Fatal("server models are local models")
	}
	if !strings.Contains(strings.Join(session.LocalEnv("ollama:qwen3:8b"), " "), "ANTHROPIC_BASE_URL="+session.OllamaHost()) {
		t.Fatal("Ollama models still go to Ollama")
	}
}

func TestOllamaPullAndContextCopy(t *testing.T) {
	var created map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pull":
			w.Write([]byte("{\"status\":\"pulling manifest\"}\n{\"status\":\"downloading\",\"total\":100,\"completed\":40}\n{\"status\":\"success\"}\n"))
		case "/api/create":
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &created)
			w.Write([]byte(`{"status":"success"}`))
		}
	}))
	defer srv.Close()
	var steps []string
	var fracs []float64
	if err := ollamaPull(srv.URL, "qwen3:8b", func(st string, f float64) { steps = append(steps, st); fracs = append(fracs, f) }); err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 || fracs[1] != 0.4 || fracs[0] != -1 {
		t.Fatalf("progress: %v %v", steps, fracs)
	}
	name, err := ollamaContextCopy(srv.URL, "qwen3:8b", 64)
	if err != nil || name != "qwen3:8b-64k" || created["from"] != "qwen3:8b" || created["parameters"].(map[string]any)["num_ctx"].(float64) != 65536 {
		t.Fatalf("context copy: %q %v %v", name, err, created)
	}
	if contextCopyName("llama3", 32) != "llama3:latest-32k" {
		t.Fatal("untagged models get :latest")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":"pull model manifest: file does not exist"}` + "\n"))
	}))
	defer bad.Close()
	if err := ollamaPull(bad.URL, "nope:1b", func(string, float64) {}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("a missing tag reports Ollama's error: %v", err)
	}
}

func TestLocalServersInSettingsAndList(t *testing.T) {
	h := newHarness(t)
	h.d.fetchLocal = func(string) []localInfo { return []localInfo{{Name: "ollama:qwen3:8b"}} }
	h.d.fetchServer = func(c localServerConf) []string { return []string{"glm-4.6"} }
	h.ui(func() {
		if err := h.d.saveLocalServer("", localServerConf{Name: "bad name", URL: "http://x"}); err == nil {
			t.Fatal("names without spaces")
		}
		if err := h.d.saveLocalServer("", localServerConf{Name: "lm", URL: "ftp://x"}); err == nil {
			t.Fatal("http(s) addresses only")
		}
		if err := h.d.saveLocalServer("", localServerConf{Name: "lm", URL: "http://127.0.0.1:1234/", Window: 32768}); err != nil {
			t.Fatal(err)
		}
		if err := h.d.saveLocalServer("lm", localServerConf{Name: "studio", URL: "http://127.0.0.1:1234"}); err != nil || len(h.d.localServers()) != 1 || h.d.localServers()[0].Name != "studio" {
			t.Fatalf("edit replaces by old name: %+v", h.d.localServers())
		}
	})
	h.eventually("server models listed", func() bool {
		return contains(h.d.localModels, "local:studio/glm-4.6") && contains(h.d.localModels, "ollama:qwen3:8b")
	})
	h.ui(func() {
		if session.LocalBaseURL("local:studio/glm-4.6") != "http://127.0.0.1:1234" {
			t.Fatalf("settings reach the session layer: %q", session.LocalBaseURL("local:studio/glm-4.6"))
		}
		h.d.pingLocal = func(host string) bool { return false }
		if err := h.d.checkOllama("local:studio/glm-4.6"); err == nil || !strings.Contains(err.Error(), "127.0.0.1:1234") {
			t.Fatalf("a server model checks its own server: %v", err)
		}
	})
	session.SetLocalServers(nil)
}

func TestSetupRows(t *testing.T) {
	h := newHarness(t)
	oldLook, oldTerm := lookPath, openTerminal
	defer func() { lookPath, openTerminal = oldLook, oldTerm }()
	have := map[string]bool{}
	lookPath = func(n string) string {
		if have[n] {
			return "/usr/bin/" + n
		}
		return ""
	}
	var ran []string
	openTerminal = func(title, script string) (*exec.Cmd, error) {
		ran = append(ran, script)
		return exec.Command("true"), nil
	}
	h.ui(func() {
		h.d.cfg.ClaudeBin = "claude"
		rows := map[string]setupRow{}
		for _, r := range h.d.setupRows(nil) {
			rows[r.title] = r
		}
		c := rows["Claude Code"]
		if c.ok || c.action != "Install..." || c.optional {
			t.Fatalf("missing Claude Code is required with Install: %+v", c)
		}
		if rows["Claude account"].state != "install Claude Code first" {
			t.Fatal("sign-in waits for Claude Code")
		}
		if !h.d.setupMissing() {
			t.Fatal("setup is missing something")
		}
		c.do()
		if overlayButton(h, "Run it") == nil {
			t.Fatal("installers ask first")
		}
	})
	h.ui(func() { test.Tap(overlayButton(h, "Run it")) })
	h.eventually("installer ran", func() bool { return len(ran) == 1 })
	if ran[0] != claudeInstallLinux {
		t.Fatalf("the official installer: %q", ran[0])
	}
	have["claude"] = true
	h.ui(func() {
		h.d.acct.LoggedIn = true
		h.d.app.Preferences().SetString(prefPCPassword, "x")
		if h.d.setupMissing() {
			t.Fatal("Claude Code + sign-in is a complete default setup")
		}
	})
	if cmd, ok := pkgInstall(map[string]string{"nonexistent-pm-xyz": "a"}); ok || cmd != "" {
		t.Fatal("unknown package managers aren't used")
	}
}

func TestSetupShots(t *testing.T) {
	if os.Getenv("DESK_SHOTS") == "" {
		t.Skip("set DESK_SHOTS=1 to render Setup and Local models to .shots/")
	}
	dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
	os.MkdirAll(dir, 0o755)
	t.Setenv("HOME", t.TempDir())
	h := newHarness(t)
	h.d.fetchServer = func(localServerConf) []string { return nil }
	h.ui(func() {
		h.d.saveLocalServer("", localServerConf{Name: "llama", URL: "http://127.0.0.1:8080", Models: []string{"glm-4.6"}})
	})
	snap := func(name string, open func()) {
		h.ui(open)
		time.Sleep(300 * time.Millisecond)
		h.ui(func() { shot(t, h.w, dir, name) })
		h.ui(func() {
			for _, o := range h.w.Canvas().Overlays().List() {
				h.w.Canvas().Overlays().Remove(o)
			}
		})
	}
	snap("setup.png", h.d.showSetup)
	snap("local-models.png", h.d.localModelsDialog)
}
