package folders

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

const (
	EventRing    = 200
	EventWaitMax = 25
	EventWaiters = 4
)

type Event struct {
	ID     int64  `json:"id"`
	Time   int64  `json:"time"`
	Type   string `json:"type"`
	RC     string `json:"rc"`
	Quill  string `json:"quill"`
	Title  string `json:"title"`
	Folder string `json:"folder"`
}

type EventPage struct {
	Version int     `json:"version"`
	Boot    string  `json:"boot"`
	Next    int64   `json:"next"`
	Events  []Event `json:"events"`
}

var eventTypes = map[string]bool{"approval": true, "question": true, "plan": true, "finished": true, "error": true}

func newBoot() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) SetEvents(on bool) {
	s.mu.Lock()
	s.eventsOn = on
	s.mu.Unlock()
}

func (s *Server) Emit(e Event) {
	if !eventTypes[e.Type] {
		return
	}
	s.mu.Lock()
	if !s.eventsOn {
		s.mu.Unlock()
		return
	}
	e.ID = s.next
	s.next++
	if e.Time == 0 {
		e.Time = time.Now().UnixMilli()
	}
	s.ring = append(s.ring, e)
	if len(s.ring) > EventRing {
		s.ring = append([]Event(nil), s.ring[len(s.ring)-EventRing:]...)
	}
	wake := s.wake
	s.wake = make(chan struct{})
	s.mu.Unlock()
	close(wake)
}

func (s *Server) page(since int64) (EventPage, chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := EventPage{Version: 1, Boot: s.boot, Next: s.next, Events: []Event{}}
	for _, e := range s.ring {
		if e.ID >= since {
			p.Events = append(p.Events, e)
		}
	}
	return p, s.wake
}

func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	on := s.eventsOn
	s.mu.Unlock()
	if !on {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	since, err := strconv.ParseInt(q.Get("since"), 10, 64)
	if err != nil || since < 0 {
		since = 0
	}
	wait, err := strconv.Atoi(q.Get("wait"))
	if err != nil || wait < 0 {
		wait = 0
	}
	wait = min(wait, EventWaitMax)
	p, wake := s.page(since)
	if len(p.Events) == 0 && wait > 0 {
		s.mu.Lock()
		if s.waiters >= EventWaiters {
			s.mu.Unlock()
			http.Error(w, "too many waiters", http.StatusTooManyRequests)
			return
		}
		s.waiters++
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			s.waiters--
			s.mu.Unlock()
		}()
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Duration(wait+5) * time.Second))
		t := time.NewTimer(time.Duration(wait) * time.Second)
		defer t.Stop()
		select {
		case <-wake:
		case <-t.C:
		case <-r.Context().Done():
			return
		}
		p, _ = s.page(since)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(p)
}
