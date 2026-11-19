package live

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Process struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	ProcStart string `json:"procStart"`
	Entry     string `json:"entrypoint"`
}

func DefaultDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "sessions")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "sessions")
}

type Finder struct {
	Dir     string
	ProcDir string
}

func NewFinder() *Finder {
	return &Finder{Dir: DefaultDir(), ProcDir: "/proc"}
}

func (f *Finder) Running(exclude map[int]bool) map[string]Process {
	out := map[string]Process{}
	files, err := filepath.Glob(filepath.Join(f.Dir, "*.json"))
	if err != nil {
		return out
	}
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var p Process
		if json.Unmarshal(b, &p) != nil || p.PID <= 0 || p.SessionID == "" {
			continue
		}
		if exclude[p.PID] || !f.alive(p) {
			continue
		}
		out[p.SessionID] = p
	}
	return out
}

func (f *Finder) alive(p Process) bool {
	stat, err := os.ReadFile(filepath.Join(f.ProcDir, strconv.Itoa(p.PID), "stat"))
	if err != nil {
		return false
	}
	s := string(stat)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return false
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 20 {
		return false
	}
	if fields[0] == "Z" {
		return false
	}
	if p.ProcStart != "" && fields[19] != p.ProcStart {
		return false
	}
	return true
}

func (p Process) Background() bool {
	return p.Kind == "bg"
}

func (p Process) Busy() bool {
	return p.Status == "busy"
}

func (f *Finder) Release(p Process, claudeBin string, timeout time.Duration) error {
	if !f.alive(p) {
		return nil
	}
	if p.Background() && claudeBin != "" {
		_ = exec.Command(claudeBin, "stop", p.SessionID).Run()
		if f.waitGone(p, timeout) {
			return nil
		}
	}
	_ = syscall.Kill(p.PID, syscall.SIGTERM)
	if f.waitGone(p, timeout) {
		return nil
	}
	_ = syscall.Kill(p.PID, syscall.SIGKILL)
	if f.waitGone(p, 2*time.Second) {
		return nil
	}
	return fmt.Errorf("pid %d did not exit", p.PID)
}

func (f *Finder) waitGone(p Process, timeout time.Duration) bool {
	end := time.Now().Add(timeout)
	for {
		if !f.alive(p) {
			return true
		}
		if time.Now().After(end) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}
