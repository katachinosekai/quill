package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

func TestRestartTargetSkipsMissingFile(t *testing.T) {
	dir := t.TempDir()
	prog := filepath.Join(dir, "quill")
	if err := os.WriteFile(prog, []byte("\x7fELF rest of a program"), 0o755); err != nil {
		t.Fatal(err)
	}
	text := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(text, []byte("not a program"), 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "deleted-quill")
	if got := restartTarget(gone, text, prog); got != prog {
		t.Fatalf("restart target = %q, want the start path %q", got, prog)
	}
	if got := restartTarget(prog, gone); got != prog {
		t.Fatalf("restart target = %q, want the program file %q", got, prog)
	}
	if got := restartTarget(gone, ""); got != "" {
		t.Fatalf("no program file: got %q", got)
	}
	if !filepath.IsAbs(startPath) {
		t.Fatalf("start path must be absolute: %q", startPath)
	}
}

func TestRestartEnvStartsOwnKeeper(t *testing.T) {
	env := restartEnv([]string{"HOME=/home/user", keeperEnv + "=/run/user/1000/k.sock", restoreEnv + "=/tmp/old.json", "QUILL_UNLOCKS_FD=7"}, "/tmp/new.json")
	want := []string{"HOME=/home/user", restoreEnv + "=/tmp/new.json"}
	if !slices.Equal(env, want) {
		t.Fatalf("env = %q, want %q", env, want)
	}
}

type restartFakes struct {
	mu        sync.Mutex
	execs     []string
	builds    []string
	handed    []string
	spawned   []string
	resumed   []string
	spawnEnv  []string
	execErr   error
	spawnErr  error
	drafts    map[string]string
	restoreOK bool
}

func fakeRestart(t *testing.T, execErr, spawnErr error) *restartFakes {
	f := &restartFakes{execErr: execErr, spawnErr: spawnErr}
	readRestore := func(env []string) *restoreState {
		for _, kv := range env {
			if p, ok := strings.CutPrefix(kv, restoreEnv+"="); ok {
				b, err := os.ReadFile(p)
				if err != nil {
					return nil
				}
				var st restoreState
				if json.Unmarshal(b, &st) != nil {
					return nil
				}
				return &st
			}
		}
		return nil
	}
	oldExec, oldSpawn := execQuill, spawnQuill
	execQuill = func(target string, args, env []string) error {
		st := readRestore(env)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.execs = append(f.execs, target)
		if st != nil {
			f.builds = append(f.builds, st.Build)
			for _, hs := range st.Handed {
				f.handed = append(f.handed, hs.ID)
			}
		}
		return f.execErr
	}
	spawnQuill = func(path string, args, env []string) error {
		st := readRestore(env)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.spawned = append(f.spawned, path)
		f.spawnEnv = env
		if st != nil {
			f.resumed = st.Running
			f.drafts = st.Drafts
			f.restoreOK = len(st.Handed) == 0
		}
		return f.spawnErr
	}
	t.Cleanup(func() { execQuill, spawnQuill = oldExec, oldSpawn })
	return f
}

func (f *restartFakes) get(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// The tray restart hands the running session over to the running program
// file at once, although the user just typed.
func TestTrayRestartHandsOver(t *testing.T) {
	h := newHarness(t)
	f := fakeRestart(t, nil, nil)
	f.execErr = errors.New("test: stay in this process")
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("hello")
	})
	h.eventually("reply", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		h.d.input.SetText("half a thought")
		h.d.lastTyped = time.Now()
		h.d.restartQuill()
		if !h.d.swapping {
			t.Fatal("the tray restart must not wait for typing to pause")
		}
	})
	h.eventually("handed over", func() bool {
		n := 0
		f.get(func() { n = len(f.execs) })
		return n > 0
	})
	want := restartTarget(restartTargets()...)
	if runtime.GOOS == "linux" && want != "/proc/self/exe" {
		t.Fatalf("the restart must run the exact running build: %q", want)
	}
	f.get(func() {
		if f.execs[0] != want {
			t.Fatalf("restarted %q, want the running program file %q", f.execs[0], want)
		}
		if !slices.Contains(f.handed, s.id) {
			t.Fatalf("session not handed over: %v", f.handed)
		}
	})
}

// When the handover fails, the sessions stop and a new Quill Desktop starts
// that resumes them, with the drafts.
func TestTrayRestartFallsBackToCleanRestart(t *testing.T) {
	h := newHarness(t)
	f := fakeRestart(t, errors.New("exec failed"), nil)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("hello")
	})
	h.eventually("reply", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		h.d.input.SetText("unsent words")
		h.d.restartQuill()
	})
	h.eventually("new Quill Desktop started", func() bool {
		n := 0
		f.get(func() { n = len(f.spawned) })
		return n > 0
	})
	f.get(func() {
		if len(f.execs) == 0 {
			t.Fatal("the handover must be tried first")
		}
		if !slices.Contains(f.resumed, s.id) || !f.restoreOK {
			t.Fatalf("the new Quill Desktop must resume the session: running %v", f.resumed)
		}
		if f.drafts[s.id] != "unsent words" {
			t.Fatalf("draft lost: %v", f.drafts)
		}
		for _, kv := range f.spawnEnv {
			if strings.HasPrefix(kv, keeperEnv+"=") {
				t.Fatal("the new Quill Desktop must start its own keeper")
			}
		}
	})
	h.eventually("session stopped for the restart", func() bool { return !s.running() })
}

// A failed clean restart runs the sessions here again.
func TestTrayRestartFailureResumesHere(t *testing.T) {
	h := newHarness(t)
	fakeRestart(t, errors.New("exec failed"), errors.New("spawn failed"))
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("hello")
	})
	h.eventually("reply", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() { h.d.restartQuill() })
	h.eventually("restart gave up", func() bool { return strings.Contains(h.d.updText.Text, "could not restart") })
	h.eventually("session runs again", func() bool { return s.running() && !h.d.restarting })
}

func writeProgram(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "quill")
	if err := os.WriteFile(p, []byte("\x7fELF a newer build"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// A new build the user accepted stays pending: the restart runs the build
// that runs now and offers the new one again afterwards.
func TestTrayRestartKeepsNewBuildPending(t *testing.T) {
	h := newHarness(t)
	f := fakeRestart(t, errors.New("test: stay in this process"), errors.New("test: no new process"))
	build := writeProgram(t)
	h.ui(func() {
		h.d.pendingBuild = build
		h.d.swapOK, h.d.swapTo = true, build
		h.d.signingIn = func() {} // holds the accepted switch
		h.d.trySwap()
		if h.d.swapping {
			t.Fatal("sign-in must hold the switch")
		}
		h.d.signingIn = nil
		h.d.restartQuill()
	})
	h.eventually("handed over", func() bool {
		n := 0
		f.get(func() { n = len(f.execs) })
		return n > 0
	})
	f.get(func() {
		if f.execs[0] == build {
			t.Fatal("the restart must not pick up the new build")
		}
		if f.builds[0] != build {
			t.Fatalf("the new build must stay pending across the restart: %q", f.builds[0])
		}
	})
	h.eventually("offered again", func() bool { return h.d.pendingBuild == build && !h.d.restarting })

	h.ui(func() {
		h.d.pendingBuild = ""
		h.d.applyRestore(&restoreState{Build: build})
		if h.d.pendingBuild != build || !strings.Contains(h.d.updText.Text, "new Quill Desktop build") {
			t.Fatalf("restored build not offered: %q %q", h.d.pendingBuild, h.d.updText.Text)
		}
	})
}

// The clean restart waits for busy turns at most restartTurnWait, with a
// countdown, then goes ahead.
func TestTrayRestartFallbackStopsWaiting(t *testing.T) {
	old := restartTurnWait
	restartTurnWait = 3 * time.Second
	t.Cleanup(func() { restartTurnWait = old })
	h := newHarness(t)
	f := fakeRestart(t, errors.New("exec failed"), nil)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("slow") // a turn that doesn't end
	})
	h.eventually("streaming", func() bool { return s.running() && s.conv.Busy })
	h.ui(func() { h.d.restartQuill() })
	h.eventually("countdown", func() bool {
		x := h.d.updText.Text
		return strings.Contains(x, "at the latest in 0:0") && lineButtons(h.d) == "Restart now|Cancel"
	})
	h.eventuallyFor("went ahead after the wait", 15*time.Second, func() bool {
		n := 0
		f.get(func() { n = len(f.spawned) })
		return n > 0
	})
	f.get(func() {
		if !slices.Contains(f.resumed, s.id) {
			t.Fatalf("session not resumed: %v", f.resumed)
		}
	})
}

func lineButtons(d *desk) string {
	var out []string
	for _, o := range d.updActs.Objects {
		if b, ok := o.(interface{ MinSize() fyne.Size }); ok {
			if w, ok := b.(*widget.Button); ok {
				out = append(out, w.Text)
			}
		}
	}
	return strings.Join(out, "|")
}
