package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/katachinosekai/quill/internal/protocol"
)

const approvalTail = 256 << 10

func (d *desk) transcriptPath(s *deskSession) string {
	if info, ok := d.infos[s.id]; ok && info.Path != "" {
		return info.Path
	}
	if m, _ := filepath.Glob(filepath.Join(d.cfg.ProjectsRoot, "*", s.id+".jsonl")); len(m) == 1 {
		return m[0]
	}
	return ""
}

func lastHumanMessage(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", false
	}
	off := max(0, st.Size()-approvalTail)
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return "", false
	}
	lines := strings.Split(string(buf), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var o struct {
			Type   string `json:"type"`
			IsMeta bool   `json:"isMeta"`
			Origin *struct {
				Kind string `json:"kind"`
			} `json:"origin"`
			Message *protocol.Message `json:"message"`
		}
		if json.Unmarshal([]byte(lines[i]), &o) != nil || o.Type != "user" || o.IsMeta || o.Message == nil {
			continue
		}
		if o.Origin != nil && o.Origin.Kind != "human" {
			continue
		}
		var text []string
		human := false
		for _, b := range o.Message.Blocks() {
			if b.Type == "text" {
				text = append(text, b.Text)
				human = true
			}
		}
		if !human {
			continue
		}
		return strings.Join(text, "\n"), true
	}
	return "", false
}

func (d *desk) approvedInTranscript(s *deskSession) bool {
	path := d.transcriptPath(s)
	if path == "" {
		return false
	}
	text, ok := lastHumanMessage(path)
	return ok && (isApproval(text) || text == planGoMessage)
}
