package folders

import (
	"net/http"
	"time"
)

type UsageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resetsAt"`
}

type UsageTotals struct {
	At       int64        `json:"at"`
	FiveHour *UsageWindow `json:"fiveHour,omitempty"`
	Weekly   *UsageWindow `json:"weekly,omitempty"`
	Credits  string       `json:"credits"`
	Today    int64        `json:"today"`
	Week     int64        `json:"week"`
}

type SessionMeta struct {
	Last       int64  `json:"last"`
	Msgs       int    `json:"msgs"`
	Status     string `json:"status"`
	Tokens     int64  `json:"tokens"`
	Context    int    `json:"context"`
	ContextMax int    `json:"contextMax"`
	Kmem       string `json:"kmem,omitempty"`
}

type Usage struct {
	Version int                    `json:"version"`
	Usage   UsageTotals            `json:"usage"`
	Meta    map[string]SessionMeta `json:"meta"`
}

const usageCache = 2 * time.Second

func (s *Server) SetUsageSource(f func() []byte) {
	s.mu.Lock()
	s.usageSrc = f
	s.usageAt = time.Time{}
	s.mu.Unlock()
}

func (s *Server) serveUsage(w http.ResponseWriter) {
	s.mu.Lock()
	src, data, at := s.usageSrc, s.usageData, s.usageAt
	s.mu.Unlock()
	if src == nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	if data == nil || time.Since(at) > usageCache {
		data = src()
		s.mu.Lock()
		s.usageData, s.usageAt = data, time.Now()
		s.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}
