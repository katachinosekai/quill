package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

type Info struct {
	ID         string
	Path       string
	Cwd        string
	Title      string
	Last       time.Time
	Messages   int
	Entrypoint string
	ModTime    time.Time
	Size       int64
}

func DefaultRoot() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "projects")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

func Sanitize(cwd string) string {
	var b strings.Builder
	for _, r := range cwd {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

type Scanner struct {
	Root  string
	mu    sync.Mutex
	cache map[string]Info
}

func NewScanner(root string) *Scanner {
	return &Scanner{Root: root, cache: map[string]Info{}}
}

func (s *Scanner) Scan() ([]Info, error) {
	dirs, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, err
	}
	type job struct {
		path, dir string
		fi        os.FileInfo
	}
	var jobs []job
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(s.Root, d.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			fi, err := f.Info()
			if err != nil {
				continue
			}
			jobs = append(jobs, job{filepath.Join(dir, f.Name()), d.Name(), fi})
		}
	}
	out := make([]Info, len(jobs))
	keep := make([]bool, len(jobs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	for i, j := range jobs {
		s.mu.Lock()
		c, ok := s.cache[j.path]
		s.mu.Unlock()
		if ok && c.ModTime.Equal(j.fi.ModTime()) && c.Size == j.fi.Size() {
			out[i], keep[i] = c, c.Messages > 0
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, j job) {
			defer func() { <-sem; wg.Done() }()
			info, err := ScanFile(j.path)
			if err != nil {
				return
			}
			if info.Cwd == "" {
				info.Cwd = j.dir
			}
			info.ModTime, info.Size = j.fi.ModTime(), j.fi.Size()
			if info.Last.IsZero() {
				info.Last = info.ModTime
			}
			s.mu.Lock()
			s.cache[j.path] = info
			s.mu.Unlock()
			out[i], keep[i] = info, info.Messages > 0
		}(i, j)
	}
	wg.Wait()
	res := out[:0]
	for i, o := range out {
		if keep[i] {
			res = append(res, o)
		}
	}
	sort.Slice(res, func(a, b int) bool { return res[a].Last.After(res[b].Last) })
	return res, nil
}

type scanRec struct {
	Type        string          `json:"type"`
	IsMeta      bool            `json:"isMeta"`
	IsSidechain bool            `json:"isSidechain"`
	Timestamp   string          `json:"timestamp"`
	Cwd         string          `json:"cwd"`
	Entrypoint  string          `json:"entrypoint"`
	AiTitle     string          `json:"aiTitle"`
	CustomTitle string          `json:"customTitle"`
	Summary     string          `json:"summary"`
	Message     json.RawMessage `json:"message"`
}

var interesting = [][]byte{
	[]byte(`"type":"user"`), []byte(`"type":"assistant"`), []byte(`"type":"ai-title"`),
	[]byte(`"type":"custom-title"`), []byte(`"type":"summary"`),
}

func ScanFile(path string) (Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	info := Info{ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Path: path}
	var firstPrompt, aiTitle, customTitle, summary string
	seenAssistant := map[string]bool{}
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			hit := false
			for _, p := range interesting {
				if bytes.Contains(line, p) {
					hit = true
					break
				}
			}
			if hit {
				var rec scanRec
				if json.Unmarshal(line, &rec) == nil {
					switch rec.Type {
					case "ai-title":
						aiTitle = rec.AiTitle
					case "custom-title":
						customTitle = rec.CustomTitle
					case "summary":
						summary = rec.Summary
					case "user", "assistant":
						if rec.IsSidechain || rec.IsMeta {
							break
						}
						if rec.Cwd != "" && info.Cwd == "" {
							info.Cwd = rec.Cwd
						}
						if rec.Entrypoint != "" && info.Entrypoint == "" {
							info.Entrypoint = rec.Entrypoint
						}
						if t, e := time.Parse(time.RFC3339Nano, rec.Timestamp); e == nil && t.After(info.Last) {
							info.Last = t
						}
						var m protocol.Message
						if json.Unmarshal(rec.Message, &m) != nil {
							break
						}
						if rec.Type == "user" {
							for _, b := range m.Blocks() {
								if b.Type != "text" {
									continue
								}
								t, ok := chat.CleanUserText(b.Text)
								if !ok || strings.HasPrefix(t, "[Request interrupted") {
									continue
								}
								info.Messages++
								if firstPrompt == "" && !strings.HasPrefix(t, "/") {
									firstPrompt = t
								}
							}
						} else {
							for _, b := range m.Blocks() {
								if b.Type == "text" && strings.TrimSpace(b.Text) != "" && !seenAssistant[m.ID] {
									seenAssistant[m.ID] = true
									info.Messages++
								}
							}
						}
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return info, err
		}
	}
	switch {
	case customTitle != "":
		info.Title = customTitle
	case aiTitle != "":
		info.Title = aiTitle
	case summary != "":
		info.Title = summary
	default:
		info.Title = oneLine(firstPrompt, 80)
	}
	if info.Title == "" {
		info.Title = "(untitled)"
	}
	return info, nil
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

func Load(path string) (*chat.Conversation, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	c := chat.New()
	c.SessionID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if ev, perr := protocol.ParseLine(line); perr == nil {
				switch ev.Type {
				case "user", "assistant":
					if ev.Cwd != "" && c.Cwd == "" {
						c.Cwd = ev.Cwd
					}
					c.Apply(ev)
				case "permission-mode":
					var pm struct {
						PermissionMode string `json:"permissionMode"`
					}
					if json.Unmarshal(line, &pm) == nil {
						c.Mode = pm.PermissionMode
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return c, err
		}
	}
	return c, nil
}
