package httplog

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// entry is one logged HTTP transaction (a single JSON line).
type entry struct {
	Time        string            `json:"time"`
	Dir         string            `json:"dir"` // "in" | "out"
	Method      string            `json:"method"`
	URL         string            `json:"url"`
	Path        string            `json:"path,omitempty"`
	Query       string            `json:"query,omitempty"`
	RemoteIP    string            `json:"remoteIp,omitempty"`
	Host        string            `json:"host,omitempty"`
	Proto       string            `json:"proto,omitempty"`
	Status      int               `json:"status,omitempty"`
	DurationMs  float64           `json:"durationMs"`
	ReqBytes    int               `json:"reqBytes,omitempty"`
	RespBytes   int               `json:"respBytes,omitempty"`
	ReqHeaders  map[string]string `json:"reqHeaders,omitempty"`
	RespHeaders map[string]string `json:"respHeaders,omitempty"`
	ReqBody     string            `json:"reqBody,omitempty"`
	RespBody    string            `json:"respBody,omitempty"`
	ReqTrunc    bool              `json:"reqBodyTruncated,omitempty"`
	RespTrunc   bool              `json:"respBodyTruncated,omitempty"`
	Err         string            `json:"error,omitempty"`
}

var (
	initOnce  sync.Once
	closeOnce sync.Once
	sink      io.Writer
	logCh     chan []byte
	writerWG  sync.WaitGroup
	dropped   int64
	closer    io.Closer
)

// Init reads the env config and, when enabled, opens the (rotating) sink
// and starts the single writer goroutine. Idempotent - safe to call from
// every binary's startup.
func Init() {
	initOnce.Do(func() {
		cfg = loadConfig()
		if !cfg.Enabled {
			return
		}
		w, err := openSink(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "httplog: disabled - could not open sink %q: %v\n", cfg.File, err)
			cfg.Enabled = false
			return
		}
		sink = w
		logCh = make(chan []byte, cfg.QueueSize)
		writerWG.Add(1)
		go writerLoop()
		go dropReporter()
		fmt.Fprintf(os.Stderr, "httplog: enabled -> %s (inbound=%v outbound=%v bodies=%v bodyMax=%d)\n",
			cfg.File, cfg.Inbound, cfg.Outbound, cfg.Bodies, cfg.BodyMax)
	})
}

func openSink(c Config) (io.Writer, error) {
	switch strings.ToLower(strings.TrimSpace(c.File)) {
	case "", "stdout", "-":
		return os.Stdout, nil
	case "stderr":
		return os.Stderr, nil
	}
	rw, err := newRotatingWriter(c)
	if err != nil {
		return nil, err
	}
	closer = rw
	return rw, nil
}

func writerLoop() {
	defer writerWG.Done()
	for b := range logCh {
		_, _ = sink.Write(b)
	}
}

// Close flushes pending entries and closes the rotating file. Safe to call
// on shutdown even when logging is disabled or was never started.
func Close() {
	closeOnce.Do(func() {
		if logCh != nil {
			close(logCh)
			writerWG.Wait()
		}
		if closer != nil {
			_ = closer.Close()
		}
	})
}

// dropReporter periodically emits a synthetic line noting how many entries
// were dropped because the queue was full (backpressure without ever
// blocking a request).
func dropReporter() {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for range t.C {
		if n := atomic.SwapInt64(&dropped, 0); n > 0 {
			line, _ := json.Marshal(map[string]interface{}{
				"time":           time.Now().UTC().Format(time.RFC3339Nano),
				"dir":            "meta",
				"note":           "httplog queue full",
				"droppedEntries": n,
			})
			emit(append(line, '\n'))
		}
	}
}

// emit queues a marshalled line, never blocking; a full queue drops the
// line and bumps the counter.
func emit(line []byte) {
	select {
	case logCh <- line:
	default:
		atomic.AddInt64(&dropped, 1)
	}
}

func write(e entry) {
	e.Time = time.Now().UTC().Format(time.RFC3339Nano)
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	emit(append(b, '\n'))
}

func sampled() bool {
	if cfg.Sample >= 1 {
		return true
	}
	if cfg.Sample <= 0 {
		return false
	}
	return rand.Float64() < cfg.Sample
}

func skipPath(p string) bool {
	for _, pre := range cfg.SkipPaths {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

// ---- redaction --------------------------------------------------------

func redactHeaders(h http.Header) map[string]string {
	if !cfg.Headers || len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, vs := range h {
		v := strings.Join(vs, ", ")
		if cfg.redactHeaders[strings.ToLower(k)] {
			v = mask(v)
		}
		out[k] = v
	}
	return out
}

func mask(s string) string {
	if s == "" {
		return ""
	}
	return "***"
}

// redactBody masks known-sensitive keys in a JSON object/array or a
// form-encoded body, caps the result to BodyMax, and reports truncation.
func redactBody(contentType string, raw []byte) (body string, truncated bool) {
	if len(raw) == 0 {
		return "", false
	}
	ct := strings.ToLower(contentType)

	switch {
	case strings.Contains(ct, "json"):
		var v interface{}
		if json.Unmarshal(raw, &v) == nil {
			redactJSON(v)
			if b, err := json.Marshal(v); err == nil {
				raw = b
			}
		}
	case strings.Contains(ct, "x-www-form-urlencoded"):
		raw = []byte(redactForm(string(raw)))
	}

	if cfg.BodyMax > 0 && len(raw) > cfg.BodyMax {
		return string(raw[:cfg.BodyMax]), true
	}
	return string(raw), false
}

func redactJSON(v interface{}) {
	switch t := v.(type) {
	case map[string]interface{}:
		for k, val := range t {
			if cfg.redactFields[strings.ToLower(k)] {
				t[k] = "***"
				continue
			}
			redactJSON(val)
		}
	case []interface{}:
		for _, val := range t {
			redactJSON(val)
		}
	}
}

func redactForm(s string) string {
	parts := strings.Split(s, "&")
	for i, p := range parts {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) == 2 && cfg.redactFields[strings.ToLower(kv[0])] {
			parts[i] = kv[0] + "=***"
		}
	}
	return strings.Join(parts, "&")
}
