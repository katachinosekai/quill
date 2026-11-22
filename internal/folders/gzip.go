package folders

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Replies over gzipMin bytes are gzipped when the request accepts gzip
// (Quill Android measured 238 KB -> 70 KB for a 60-turn session, user
// 2026-11-22). Smaller replies, and clients that don't ask, get them as
// before. The live endpoints (/events, /phone/stream) stay uncompressed.
const gzipMin = 1024

var gzipPool = sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression); return w }}

// acceptsGzip: the Accept-Encoding header lists gzip without q=0.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		params = strings.ReplaceAll(strings.ToLower(params), " ", "")
		if q, ok := strings.CutPrefix(params, "q="); ok {
			if v, err := strconv.ParseFloat(q, 64); err == nil && v == 0 {
				return false
			}
		}
		return true
	}
	return false
}

// gzipWriter holds a reply until it is gzipMin bytes long, then gzips the
// rest; a shorter reply goes out as it is when the handler returns.
type gzipWriter struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
	gz     *gzip.Writer
	plain  bool
}

func (g *gzipWriter) WriteHeader(status int) {
	if g.status == 0 {
		g.status = status
	}
}

func (g *gzipWriter) Write(p []byte) (int, error) {
	if g.status == 0 {
		g.status = http.StatusOK
	}
	switch {
	case g.plain:
		return g.ResponseWriter.Write(p)
	case g.gz != nil:
		return g.gz.Write(p)
	}
	g.buf.Write(p)
	if g.buf.Len() >= gzipMin {
		if err := g.start(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (g *gzipWriter) start() error {
	h := g.Header()
	if h.Get("Content-Encoding") != "" || g.status < 200 || g.status == http.StatusNoContent || g.status == http.StatusNotModified {
		g.plain = true
		g.ResponseWriter.WriteHeader(g.status)
		_, err := g.ResponseWriter.Write(g.buf.Bytes())
		return err
	}
	h.Set("Content-Encoding", "gzip")
	h.Add("Vary", "Accept-Encoding")
	h.Del("Content-Length")
	g.ResponseWriter.WriteHeader(g.status)
	g.gz = gzipPool.Get().(*gzip.Writer)
	g.gz.Reset(g.ResponseWriter)
	_, err := g.gz.Write(g.buf.Bytes())
	return err
}

// finish sends what is held: a short reply plain, a long one's gzip end.
func (g *gzipWriter) finish() {
	switch {
	case g.gz != nil:
		g.gz.Close()
		gzipPool.Put(g.gz)
		g.gz = nil
	case g.plain:
	case g.status == 0 && g.buf.Len() == 0:
	default:
		if g.status == 0 {
			g.status = http.StatusOK
		}
		g.Header().Add("Vary", "Accept-Encoding")
		g.Header().Set("Content-Length", strconv.Itoa(g.buf.Len()))
		g.ResponseWriter.WriteHeader(g.status)
		g.ResponseWriter.Write(g.buf.Bytes())
	}
}

// Unwrap lets http.ResponseController reach the connection (write
// deadlines of long replies).
func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

// liveEndpoint: replies that wait for changes and stay uncompressed.
func liveEndpoint(path string) bool { return path == "/events" || path == "/phone/stream" }
