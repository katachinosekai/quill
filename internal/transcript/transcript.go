package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
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
	Named      bool
	Last       time.Time
	Messages   int
	Entrypoint string
	Kmem       string
	Usage      Tokens
	Days       map[string]Tokens
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
	scans map[string]*fileScan
}

func NewScanner(root string) *Scanner {
	return &Scanner{Root: root, cache: map[string]Info{}, scans: map[string]*fileScan{}}
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
			s.mu.Lock()
			fs := s.scans[j.path]
			s.mu.Unlock()
			if fs == nil || j.fi.Size() < fs.offset || j.fi.Size() < c.Size {
				fs = newFileScan(j.path)
			}
			if err := fs.feed(); err != nil {
				return
			}
			s.mu.Lock()
			s.scans[j.path] = fs
			s.mu.Unlock()
			info := fs.result()
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

type msgUse struct {
	day string
	t   Tokens
}

type fileScan struct {
	info                                       Info
	firstPrompt, aiTitle, customTitle, summary string
	seenAssistant                              map[string]bool
	uses                                       map[string]msgUse
	offset                                     int64
}

func newFileScan(path string) *fileScan {
	return &fileScan{
		info:          Info{ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Path: path},
		seenAssistant: map[string]bool{},
		uses:          map[string]msgUse{},
	}
}

func ScanFile(path string) (Info, error) {
	fs := newFileScan(path)
	err := fs.feed()
	return fs.result(), err
}

func (fs *fileScan) feed() error {
	f, err := os.Open(fs.info.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(fs.offset, io.SeekStart); err != nil {
		return err
	}
	info := &fs.info
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		fs.offset += int64(len(line))
		fs.line(line, info)
	}
}

func (fs *fileScan) line(line []byte, info *Info) {
	hit := false
	for _, p := range interesting {
		if bytes.Contains(line, p) {
			hit = true
			break
		}
	}
	if !hit {
		return
	}
	var rec scanRec
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	switch rec.Type {
	case "ai-title":
		fs.aiTitle = rec.AiTitle
	case "custom-title":
		fs.customTitle = rec.CustomTitle
	case "summary":
		fs.summary = rec.Summary
	case "user", "assistant":
		if rec.IsSidechain || rec.IsMeta {
			return
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
			return
		}
		if rec.Type == "assistant" && m.Usage != nil && m.ID != "" {
			day := ""
			if t, e := time.Parse(time.RFC3339Nano, rec.Timestamp); e == nil {
				day = t.Local().Format("2006-01-02")
			}
			fs.uses[m.ID] = msgUse{day, Tokens{In: int64(m.Usage.InputTokens), Out: int64(m.Usage.OutputTokens), CacheRead: int64(m.Usage.CacheReadInputTokens), CacheWrite: int64(m.Usage.CacheCreationInputTokens)}}
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
				if fs.firstPrompt == "" && !strings.HasPrefix(t, "/") {
					fs.firstPrompt = t
				}
			}
		} else {
			for _, b := range m.Blocks() {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" && !fs.seenAssistant[m.ID] {
					fs.seenAssistant[m.ID] = true
					info.Messages++
				}
			}
		}
	}
}

func (fs *fileScan) result() Info {
	info := fs.info
	info.Usage = Tokens{}
	info.Days = nil
	for _, u := range fs.uses {
		info.Usage = info.Usage.Add(u.t)
		if u.day != "" {
			if info.Days == nil {
				info.Days = map[string]Tokens{}
			}
			info.Days[u.day] = info.Days[u.day].Add(u.t)
		}
	}
	info.Kmem = KmemName(fs.firstPrompt)
	info.Named = true
	switch {
	case fs.customTitle != "":
		info.Title = fs.customTitle
	case fs.aiTitle != "":
		info.Title = fs.aiTitle
	case fs.summary != "":
		info.Title = fs.summary
	default:
		info.Title = oneLine(fs.firstPrompt, 80)
		info.Named = false
	}
	if info.Title == "" {
		info.Title = "(untitled)"
	}
	return info
}

var kmemPrompt = regexp.MustCompile(`(?:kmem handoff get |\bkmem-resume-)([A-Za-z0-9._-]+)`)

func KmemName(firstPrompt string) string {
	if m := kmemPrompt.FindStringSubmatch(firstPrompt); m != nil {
		return strings.TrimRight(m[1], ".")
	}
	return ""
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
			if bytes.Contains(line, []byte("<task-notification>")) {
				c.NoteNotifications(string(line))
			}
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

type Tokens struct {
	In, Out, CacheRead, CacheWrite int64
}

func (t Tokens) Add(o Tokens) Tokens {
	return Tokens{t.In + o.In, t.Out + o.Out, t.CacheRead + o.CacheRead, t.CacheWrite + o.CacheWrite}
}

func (t Tokens) Total() int64 {
	return t.In + t.Out + t.CacheRead + t.CacheWrite
}
