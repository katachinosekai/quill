package folders

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const FileName = "quill-folders.json"

type File struct {
	Version   int               `json:"version"`
	UpdatedMs int64             `json:"updatedMs"`
	Updated   string            `json:"updated"`
	Host      string            `json:"host"`
	Order     []string          `json:"order"`
	Sessions  map[string]string `json:"sessions"`
}

func Build(host string, order []string, sessions map[string]string, now time.Time) File {
	if order == nil {
		order = []string{}
	}
	if sessions == nil {
		sessions = map[string]string{}
	}
	return File{Version: 1, UpdatedMs: now.UnixMilli(), Updated: now.UTC().Format(time.RFC3339), Host: host, Order: order, Sessions: sessions}
}

func (f File) SameContent(o File) bool {
	if f.Host != o.Host || len(f.Order) != len(o.Order) || len(f.Sessions) != len(o.Sessions) {
		return false
	}
	for i := range f.Order {
		if f.Order[i] != o.Order[i] {
			return false
		}
	}
	for k, v := range f.Sessions {
		if o.Sessions[k] != v {
			return false
		}
	}
	return true
}

func (f File) JSON() []byte {
	b, _ := json.MarshalIndent(f, "", "  ")
	return append(b, '\n')
}

func WriteAtomic(dir string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".quill-folders-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, filepath.Join(dir, FileName))
}

func NewKey() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func SessionID(sessionURL string) string {
	u, err := url.Parse(sessionURL)
	if err != nil {
		return ""
	}
	last := u.Path[strings.LastIndex(u.Path, "/")+1:]
	if !strings.HasPrefix(last, "session_") || len(last) <= len("session_") {
		return ""
	}
	for _, r := range last[len("session_"):] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	return last
}

func LoadOrCreateCert(dir string) (tls.Certificate, string, error) {
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil && len(c.Certificate) > 0 {
		return c, Fingerprint(c.Certificate[0]), nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, "", err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Quill phone sync"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(20, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return tls.Certificate{}, "", err
	}
	c, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return c, Fingerprint(der), nil
}

func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

type Server struct {
	mu       sync.Mutex
	phones   []Phone
	revoked  chan struct{}
	seen     func(id string)
	gate     func(id string, r *http.Request) (ok bool, msg string)
	api      http.Handler
	idem     *idemStore
	data     []byte
	ln       net.Listener
	srv      *http.Server
	Port     int
	eventsOn bool
	boot     string
	next     int64
	ring     []Event
	wake     chan struct{}
	waiters  int

	usageSrc  func() []byte
	usageData []byte
	usageAt   time.Time
}

func Listen(cert tls.Certificate, port int, phones []Phone) (*Server, error) {
	// h2 first: phones that speak HTTP/2 share one connection for all
	// requests (user 2026-11-22); others keep HTTP/1.1.
	ln, err := tls.Listen("tcp", net.JoinHostPort("", strconv.Itoa(port)), &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}})
	if err != nil {
		return nil, err
	}
	s := &Server{idem: newIdemStore(), phones: append([]Phone(nil), phones...), revoked: make(chan struct{}), ln: ln, Port: ln.Addr().(*net.TCPAddr).Port, boot: newBoot(), next: 1, wake: make(chan struct{}), eventsOn: true}
	// IdleTimeout keeps a phone's connection open between requests (without
	// it, ReadTimeout's 10 s closed it); long replies get more write time
	// in ServeHTTP (slowReply).
	s.srv = &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: idleTimeout, MaxHeaderBytes: 8 << 10}
	go s.srv.Serve(ln)
	return s, nil
}

func (s *Server) Set(data []byte) {
	s.mu.Lock()
	s.data = data
	s.mu.Unlock()
}

// SetGate decides, per request of a known device, whether it is unlocked.
// /phone/hello and /phone/unlock always pass (a device must be able to
// introduce itself and unlock); everything else of a locked device, the
// folders file, events and usage included, gets 401 "locked".
func (s *Server) SetGate(g func(id string, r *http.Request) (bool, string)) {
	s.mu.Lock()
	s.gate = g
	s.mu.Unlock()
}

func (s *Server) Close() error {
	return s.srv.Close()
}

// Slow links (user 2026-11-22): a 238 KB items page took 11 s on EDGE, past
// the 10 s WriteTimeout; the session list and items pages get slowWrite.
const (
	idleTimeout = 90 * time.Second
	slowWrite   = 60 * time.Second
)

// slowReply: the replies that can be large (session list, items pages).
func slowReply(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if r.URL.Path == "/phone/sessions" {
		return true
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/phone/"), "/")
	return len(parts) == 3 && parts[0] == "session" && parts[2] == "items"
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if slowReply(r) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(slowWrite))
	}
	if acceptsGzip(r) && !liveEndpoint(r.URL.Path) {
		g := &gzipWriter{ResponseWriter: w}
		defer g.finish()
		w = g
	}
	phone := strings.HasPrefix(r.URL.Path, "/phone/")
	if phone {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			WriteError(w, http.StatusNotFound, "not_found", "unknown path")
			return
		}
	} else if r.Method != http.MethodGet || r.URL.Path != "/folders.json" && r.URL.Path != "/events" && r.URL.Path != "/usage.json" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	data, seen, api, gate := s.data, s.seen, s.api, s.gate
	id := matchPhone(s.phones, r.Header.Get("X-Quill-Key"))
	s.mu.Unlock()
	if id == "" {
		if phone {
			WriteError(w, http.StatusForbidden, "forbidden", "unknown key")
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if seen != nil {
		seen(id)
	}
	if gate != nil && r.URL.Path != "/phone/hello" && r.URL.Path != "/phone/unlock" {
		if ok, msg := gate(id, r); !ok {
			WriteError(w, http.StatusUnauthorized, "locked", msg)
			return
		}
	}
	if phone {
		if api == nil {
			WriteError(w, http.StatusNotFound, "not_found", "no phone API")
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), phoneKeyCtx{}, id))
		if r.Method == http.MethodPost {
			s.idem.serve(w, r, id, api)
			return
		}
		api.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/events" {
		s.serveEvents(w, r, id)
		return
	}
	if r.URL.Path == "/usage.json" {
		s.serveUsage(w)
		return
	}
	if data == nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

func phoneInterface(name string, flags net.Flags) bool {
	if flags&net.FlagUp == 0 || flags&net.FlagRunning == 0 || flags&net.FlagLoopback != 0 {
		return false
	}
	for _, p := range []string{"docker", "br-", "veth", "virbr"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

func LocalHosts() []string {
	var private, tailnet, other []string
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if !phoneInterface(i.Name, i.Flags) {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil {
				continue
			}
			ip := ipn.IP.To4()
			if ip.IsLinkLocalUnicast() {
				continue
			}
			if ip.IsPrivate() {
				private = append(private, ip.String())
			} else if ip[0] == 100 && ip[1]&0xc0 == 64 {
				tailnet = append(tailnet, ip.String())
			} else {
				other = append(other, ip.String())
			}
		}
	}
	sort.Strings(private)
	sort.Strings(tailnet)
	out := append(append(private, tailnet...), other...)
	if h, err := os.Hostname(); err == nil && h != "" && !strings.Contains(h, ".") {
		out = append(out, h+".local")
	}
	return out
}

type Pair struct {
	Hosts       []string
	Port        int
	Key         string
	Fingerprint string
	File        string
	Name        string
	ID          string
}

func (p Pair) Link() string {
	v := url.Values{}
	if len(p.Hosts) > 0 && p.Port > 0 && p.Key != "" && p.Fingerprint != "" {
		v.Set("h", strings.Join(p.Hosts, ","))
		v.Set("p", strconv.Itoa(p.Port))
		v.Set("k", p.Key)
		v.Set("fp", p.Fingerprint)
		if p.ID != "" {
			v.Set("i", p.ID)
			v.Set("v", "2")
		}
	}
	if p.File != "" {
		v.Set("f", p.File)
	}
	if p.Name != "" {
		v.Set("n", p.Name)
	}
	return "quillpair://pair?" + v.Encode()
}
