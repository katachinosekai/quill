package main

import (
	"time"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/transcript"
)

type sessMeta struct {
	Cwd         string          `json:"cwd"`
	Model       string          `json:"model"`
	Mode        string          `json:"mode"`
	Persisted   bool            `json:"persisted"`
	KmemNew     bool            `json:"kmemNew,omitempty"`
	RotateAsk   int             `json:"rotateAsk,omitempty"`
	RotateTried int             `json:"rotateTried,omitempty"`
	LastClaude  string          `json:"lastClaude,omitempty"`
	RestoreMode string          `json:"restoreMode,omitempty"`
	RetryCmd    string          `json:"retryCmd,omitempty"`
	RetryRules  []protocol.Rule `json:"retryRules,omitempty"`
	RetryDest   string          `json:"retryDest,omitempty"`
	CompactFrom string          `json:"compactFrom,omitempty"`
	KmemSave    *savedKmem      `json:"kmemSave,omitempty"`
}

type savedKmem struct {
	Name  string    `json:"name"`
	Asked time.Time `json:"asked"`
	Spare int       `json:"spare"`
}

func (d *desk) saveSessions(st *restoreState) {
	for id, s := range d.sessions {
		m := sessMeta{Cwd: s.cwd, Model: s.model, Mode: s.mode, Persisted: s.persisted, KmemNew: s.kmemNew,
			RotateAsk: s.rotateAsk, RotateTried: s.rotateTried, LastClaude: s.lastClaude, RestoreMode: s.restoreMode,
			RetryCmd: s.retryCmd, RetryRules: s.retryRules, RetryDest: s.retryDest, CompactFrom: s.compactFrom}
		if ks := s.kmemSave; ks != nil {
			m.KmemSave = &savedKmem{Name: ks.name, Asked: ks.asked, Spare: ks.spare}
		}
		if st.Meta == nil {
			st.Meta = map[string]sessMeta{}
		}
		st.Meta[id] = m
	}
	for id, text := range d.strip.drafts {
		if text == "" {
			continue
		}
		if st.Drafts == nil {
			st.Drafts = map[string]string{}
		}
		st.Drafts[id] = text
	}
}

func (d *desk) restoreSessions(st *restoreState) {
	for id, m := range st.Meta {
		s := d.session(id)
		if s == nil {
			if m.Persisted || m.Cwd == "" {
				continue
			}
			s = &deskSession{id: id, cwd: m.Cwd, conv: chat.New(), loaded: true, created: time.Now()}
			d.sessions[id] = s
			d.created[id] = true
		}
		if m.Model != "" {
			s.model = m.Model
		}
		if m.Mode != "" {
			s.mode = m.Mode
		}
		s.kmemNew, s.rotateAsk, s.rotateTried, s.lastClaude = m.KmemNew, m.RotateAsk, m.RotateTried, m.LastClaude
		s.restoreMode, s.retryCmd, s.retryRules, s.retryDest = m.RestoreMode, m.RetryCmd, m.RetryRules, m.RetryDest
		s.compactFrom = m.CompactFrom
		if k := m.KmemSave; k != nil {
			s.kmemSave = &kmemSave{name: k.Name, asked: k.Asked, spare: k.Spare}
		}
	}
	for id, text := range st.Drafts {
		d.strip.keepDraft(id, text)
	}
}

func (d *desk) resumeInterrupted(st *restoreState) {
	for id, m := range st.Meta {
		s := d.session(id)
		if s == nil {
			continue
		}
		if m.CompactFrom != "" && !s.compacting {
			d.resumeCompaction(s, m.CompactFrom)
		}
		if s.kmemSave != nil && s.running() && !s.conv.Busy {
			d.kmemAfterTurn(s)
		}
	}
}

func (d *desk) resumeCompaction(ns *deskSession, oldID string) {
	path := ""
	if info, ok := d.infos[oldID]; ok {
		path = info.Path
	}
	conv := chat.New()
	if path != "" {
		if c, err := transcript.Load(path); err == nil && c != nil {
			conv = c
		}
	}
	title, _, _ := d.rowText(ns.id)
	d.compactInto(ns, oldID, path, title, conv)
}
