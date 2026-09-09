package httplog

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"time"
)

// WrapDefaultTransport replaces http.DefaultTransport with one that logs
// every round-trip as a "dir":"out" line. This covers http.Get / the
// default client and any &http.Client{} that leaves Transport nil (which
// is how billing.go, report/generate.go, the OAuth and push code all make
// their calls). A client that sets its own Transport is not covered
// unless it is wrapped explicitly with Transport().
func WrapDefaultTransport() {
	if !OutboundEnabled() {
		return
	}
	http.DefaultTransport = Transport(http.DefaultTransport)
}

// Transport wraps a RoundTripper with outbound logging.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if !OutboundEnabled() {
		return base
	}
	return &loggingRoundTripper{base: base}
}

type loggingRoundTripper struct{ base http.RoundTripper }

func (t *loggingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if !sampled() {
		return t.base.RoundTrip(req)
	}
	start := time.Now()

	e := entry{
		Dir:    "out",
		Method: req.Method,
		URL:    req.URL.String(),
		Path:   req.URL.Path,
		Query:  req.URL.RawQuery,
		Host:   req.URL.Host,
	}
	e.ReqHeaders = redactHeaders(req.Header)

	// Request body: only when it's replayable (GetBody set - http.NewRequest
	// does this for bytes/strings bodies). A streaming body is left alone.
	if cfg.Bodies && req.Body != nil && req.GetBody != nil {
		if rc, err := req.GetBody(); err == nil {
			raw, _ := io.ReadAll(io.LimitReader(rc, int64(cfg.BodyMax)+1))
			rc.Close()
			e.ReqBytes = len(raw)
			e.ReqBody, e.ReqTrunc = redactBody(req.Header.Get("Content-Type"), capBytes(raw))
		}
	}

	resp, err := t.base.RoundTrip(req)
	e.DurationMs = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		e.Err = err.Error()
		write(e)
		return resp, err
	}

	e.Status = resp.StatusCode
	e.Proto = resp.Proto
	e.RespHeaders = redactHeaders(resp.Header)

	if cfg.Bodies && resp.Body != nil {
		ct := resp.Header.Get("Content-Type")
		done := func(captured []byte, total int) {
			e.RespBytes = total
			e.RespBody, e.RespTrunc = redactBody(ct, captured)
			write(e)
		}
		resp.Body = &respBodyTap{rc: resp.Body, max: cfg.BodyMax, done: done}
	} else {
		write(e)
	}
	return resp, nil
}

func capBytes(b []byte) []byte {
	if cfg.BodyMax > 0 && len(b) > cfg.BodyMax {
		return b[:cfg.BodyMax]
	}
	return b
}

// respBodyTap keeps the first `max` bytes of a response body as the caller
// reads it, then fires done() on EOF or Close - so streaming responses
// (e.g. LLM calls) are not buffered whole and still stream to the caller.
type respBodyTap struct {
	rc    io.ReadCloser
	buf   bytes.Buffer
	max   int
	total int
	once  sync.Once
	done  func(captured []byte, total int)
}

func (b *respBodyTap) Read(p []byte) (int, error) {
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
	if err == io.EOF {
		b.fire()
	}
	return n, err
}

func (b *respBodyTap) Close() error {
	b.fire()
	return b.rc.Close()
}

func (b *respBodyTap) fire() {
	b.once.Do(func() { b.done(b.buf.Bytes(), b.total) })
}
