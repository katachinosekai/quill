package folders

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"
)

const (
	APIBodyMax   = 64 << 10
	idemTTL      = 24 * time.Hour
	idemPerKey   = 500
	writesPerSec = 10
)

type phoneKeyCtx struct{}

func PhoneID(r *http.Request) string {
	id, _ := r.Context().Value(phoneKeyCtx{}).(string)
	return id
}

func (s *Server) SetAPI(h http.Handler) {
	s.mu.Lock()
	s.api = h
	s.mu.Unlock()
}

func (s *Server) Revoked() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revoked
}

func (s *Server) PhoneOK(id string) bool { return s.phoneOK(id) }

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func WriteError(w http.ResponseWriter, status int, code, msg string) {
	WriteJSON(w, status, map[string]any{"v": 1, "error": map[string]string{"code": code, "message": msg}})
}

var idemKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

type idemEntry struct {
	hash    [32]byte
	at      time.Time
	done    bool
	status  int
	body    []byte
	headers http.Header
}

type idemStore struct {
	mu     sync.Mutex
	phones map[string]map[string]*idemEntry
	writes map[string][]time.Time
}

func newIdemStore() *idemStore {
	return &idemStore{phones: map[string]map[string]*idemEntry{}, writes: map[string][]time.Time{}}
}

type recorder struct {
	header http.Header
	status int
	buf    bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }
func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.buf.Write(b)
}
func (r *recorder) WriteHeader(s int) {
	if r.status == 0 {
		r.status = s
	}
}

func (st *idemStore) rateOK(phone string, now time.Time) bool {
	recent := st.writes[phone][:0]
	for _, t := range st.writes[phone] {
		if now.Sub(t) < time.Second {
			recent = append(recent, t)
		}
	}
	if len(recent) >= writesPerSec {
		st.writes[phone] = recent
		return false
	}
	st.writes[phone] = append(recent, now)
	return true
}

func (st *idemStore) serve(w http.ResponseWriter, r *http.Request, phone string, h http.Handler) {
	key := r.Header.Get("Idempotency-Key")
	if !idemKeyRe.MatchString(key) {
		WriteError(w, http.StatusBadRequest, "idempotency_key_missing", "POST needs an Idempotency-Key of 16-64 [A-Za-z0-9_-]")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, APIBodyMax))
	if err != nil {
		WriteError(w, http.StatusRequestEntityTooLarge, "too_large", "body over 64 KiB")
		return
	}
	hash := sha256.Sum256(append([]byte(r.Method+" "+r.URL.RequestURI()+"\n"), body...))
	now := time.Now()
	st.mu.Lock()
	m := st.phones[phone]
	if m == nil {
		m = map[string]*idemEntry{}
		st.phones[phone] = m
	}
	for k, e := range m {
		if now.Sub(e.at) > idemTTL {
			delete(m, k)
		}
	}
	if e := m[key]; e != nil {
		st.mu.Unlock()
		switch {
		case e.hash != hash:
			WriteError(w, http.StatusUnprocessableEntity, "idempotency_mismatch", "key reused with a different request")
		case !e.done:
			WriteError(w, http.StatusConflict, "in_progress", "the first request with this key is still running")
		default:
			for k, v := range e.headers {
				w.Header()[k] = v
			}
			w.Header().Set("Idempotent-Replay", "true")
			w.WriteHeader(e.status)
			w.Write(e.body)
		}
		return
	}
	if !st.rateOK(phone, now) {
		st.mu.Unlock()
		w.Header().Set("Retry-After", "1")
		WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many writes")
		return
	}
	if len(m) >= idemPerKey {
		var oldest string
		for k, e := range m {
			if oldest == "" || e.at.Before(m[oldest].at) {
				oldest = k
			}
		}
		delete(m, oldest)
	}
	e := &idemEntry{hash: hash, at: now}
	m[key] = e
	st.mu.Unlock()
	r.Body = io.NopCloser(bytes.NewReader(body))
	rec := &recorder{header: http.Header{}}
	h.ServeHTTP(rec, r)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	st.mu.Lock()
	if rec.status == http.StatusTooManyRequests || rec.status == http.StatusServiceUnavailable {
		delete(m, key)
	} else {
		e.done, e.status, e.body, e.headers = true, rec.status, rec.buf.Bytes(), rec.header.Clone()
	}
	st.mu.Unlock()
	for k, v := range rec.header {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.status)
	w.Write(rec.buf.Bytes())
}
