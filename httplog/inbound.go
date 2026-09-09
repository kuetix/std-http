package httplog

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Middleware wraps an http.Handler, logging each request + response as one
// "dir":"in" line. A no-op passthrough when inbound logging is disabled.
// WebSocket upgrades / hijacked connections are logged as the handshake
// only (status, headers), never the framed traffic after.
func Middleware(next http.Handler) http.Handler {
	if !InboundEnabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skipPath(r.URL.Path) || !sampled() {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()

		e := entry{
			Dir:      "in",
			Method:   r.Method,
			URL:      r.URL.RequestURI(),
			Path:     r.URL.Path,
			Query:    r.URL.RawQuery,
			RemoteIP: clientIP(r),
			Host:     r.Host,
			Proto:    r.Proto,
		}
		e.ReqHeaders = redactHeaders(r.Header)

		var reqTap *bodyTap
		if cfg.Bodies && r.Body != nil && r.Body != http.NoBody {
			reqTap = newBodyTap(r.Body, cfg.BodyMax)
			r.Body = reqTap
		}

		cw := &captureWriter{ResponseWriter: w, bodyMax: cfg.BodyMax}
		if cfg.Bodies {
			cw.body = &bytes.Buffer{}
		}

		next.ServeHTTP(cw, r)

		e.DurationMs = float64(time.Since(start).Microseconds()) / 1000
		e.Status = cw.statusOr200()
		e.RespBytes = cw.written
		if h := cw.Header(); h != nil {
			e.RespHeaders = redactHeaders(h)
		}
		if reqTap != nil {
			e.ReqBytes = reqTap.total
			e.ReqBody, e.ReqTrunc = redactBody(r.Header.Get("Content-Type"), reqTap.captured())
		}
		if cw.body != nil && !cw.hijacked {
			e.RespBody, e.RespTrunc = redactBody(cw.Header().Get("Content-Type"), cw.body.Bytes())
		}
		if cw.hijacked {
			e.Err = "connection hijacked (websocket/upgrade) - response body not captured"
		}
		write(e)
	})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i != -1 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		return strings.TrimSpace(xr)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- capturing ResponseWriter ---------------------------------------

type captureWriter struct {
	http.ResponseWriter
	status   int
	written  int
	body     *bytes.Buffer // nil when bodies disabled
	bodyMax  int
	hijacked bool
}

func (c *captureWriter) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
	c.ResponseWriter.WriteHeader(status)
}

func (c *captureWriter) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	if c.body != nil && c.bodyMax > 0 && c.body.Len() < c.bodyMax {
		room := c.bodyMax - c.body.Len()
		if room > len(p) {
			room = len(p)
		}
		c.body.Write(p[:room])
	}
	n, err := c.ResponseWriter.Write(p)
	c.written += n
	return n, err
}

func (c *captureWriter) statusOr200() int {
	if c.status == 0 {
		return http.StatusOK
	}
	return c.status
}

// Hijack passes through so WebSocket upgrades still work; we just note it.
func (c *captureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := c.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("httplog: underlying ResponseWriter is not a Hijacker")
	}
	c.hijacked = true
	return h.Hijack()
}

func (c *captureWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// ---- body tap --------------------------------------------------------

// bodyTap wraps an io.ReadCloser, keeping the first `max` bytes read
// through it without buffering the whole stream (so streaming request
// bodies still stream).
type bodyTap struct {
	rc    io.ReadCloser
	buf   bytes.Buffer
	max   int
	total int
}

func newBodyTap(rc io.ReadCloser, max int) *bodyTap {
	return &bodyTap{rc: rc, max: max}
}

func (b *bodyTap) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.total += n
		if b.buf.Len() < b.max {
			room := b.max - b.buf.Len()
			if room > n {
				room = n
			}
			b.buf.Write(p[:room])
		}
	}
	return n, err
}

func (b *bodyTap) Close() error     { return b.rc.Close() }
func (b *bodyTap) captured() []byte { return b.buf.Bytes() }
