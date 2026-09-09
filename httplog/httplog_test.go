package httplog

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuf) snapshot() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.b.Bytes()...)
}
func (s *syncBuf) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Len()
}

// enable forces the package on with a fresh in-memory sink for one test.
func enable(t *testing.T, overrides map[string]string) *syncBuf {
	t.Helper()
	for k, v := range map[string]string{
		"HTTP_LOG_ENABLED": "true", "HTTP_LOG_FILE": "stderr",
		"HTTP_LOG_BODIES": "true", "HTTP_LOG_BODY_MAX": "256",
		"HTTP_LOG_INBOUND": "true", "HTTP_LOG_OUTBOUND": "true",
	} {
		t.Setenv(k, v)
	}
	for k, v := range overrides {
		t.Setenv(k, v)
	}
	cfg = loadConfig()
	buf := &syncBuf{}
	sink = buf
	logCh = make(chan []byte, 256)
	done := make(chan struct{})
	writerWG.Add(1)
	go func() { defer close(done); writerLoop() }()
	t.Cleanup(func() { close(logCh); <-done })
	return buf
}

func drain(t *testing.T, buf *syncBuf) []map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for buf.len() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	var out []map[string]interface{}
	sc := bufio.NewScanner(bytes.NewReader(buf.snapshot()))
	for sc.Scan() {
		var m map[string]interface{}
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

func TestInboundRedactsAndCaptures(t *testing.T) {
	buf := enable(t, nil)

	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "hunter2") {
			t.Errorf("handler did not receive the real request body, got %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "kuetix_session=supersecret; HttpOnly")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"token":"abc123","ok":true}`))
	}))

	req := httptest.NewRequest("POST", "/login?x=1", strings.NewReader(`{"email":"a@b.co","password":"hunter2"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 201 {
		t.Fatalf("downstream status not passed through: %d", rec.Code)
	}
	entries := drain(t, buf)
	if len(entries) != 1 {
		t.Fatalf("want 1 log line, got %d: %s", len(entries), string(buf.snapshot()))
	}
	e := entries[0]
	if e["dir"] != "in" || e["method"] != "POST" || e["path"] != "/login" || e["query"] != "x=1" {
		t.Fatalf("bad entry: %+v", e)
	}
	if int(e["status"].(float64)) != 201 {
		t.Fatalf("status not logged: %+v", e["status"])
	}
	rh := e["reqHeaders"].(map[string]interface{})
	if rh["Authorization"] != "***" {
		t.Fatalf("Authorization not redacted: %v", rh["Authorization"])
	}
	sh := e["respHeaders"].(map[string]interface{})
	if sh["Set-Cookie"] != "***" {
		t.Fatalf("Set-Cookie not redacted: %v", sh["Set-Cookie"])
	}
	if !strings.Contains(e["reqBody"].(string), `"password":"***"`) || strings.Contains(e["reqBody"].(string), "hunter2") {
		t.Fatalf("request password not redacted: %v", e["reqBody"])
	}
	if !strings.Contains(e["respBody"].(string), `"token":"***"`) || strings.Contains(e["respBody"].(string), "abc123") {
		t.Fatalf("response token not redacted: %v", e["respBody"])
	}
}

func TestSkipPathsAndSample(t *testing.T) {
	buf := enable(t, map[string]string{"HTTP_LOG_SKIP_PATHS": "/health", "HTTP_LOG_SAMPLE": "0"})
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/health", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/anything", nil)) // sample=0
	if got := drain(t, buf); len(got) != 0 {
		t.Fatalf("expected nothing logged (skip + sample=0), got %d: %s", len(got), string(buf.snapshot()))
	}
}

func TestOutboundRoundTripper(t *testing.T) {
	buf := enable(t, nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"secret":"leak","status":"ok"}`))
	}))
	defer srv.Close()

	client := &http.Client{Transport: Transport(http.DefaultTransport)}
	req, _ := http.NewRequestWithContext(context.Background(), "POST", srv.URL+"/pay", strings.NewReader(`{"apiKey":"sk_live_x","amount":100}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "leak") {
		t.Fatalf("caller got a mangled body: %q", body)
	}

	entries := drain(t, buf)
	if len(entries) != 1 || entries[0]["dir"] != "out" {
		t.Fatalf("want 1 out entry, got %+v (%s)", entries, string(buf.snapshot()))
	}
	e := entries[0]
	if !strings.Contains(e["reqBody"].(string), `"apiKey":"***"`) {
		t.Fatalf("outbound apiKey not redacted: %v", e["reqBody"])
	}
	if !strings.Contains(e["respBody"].(string), `"secret":"***"`) || strings.Contains(e["respBody"].(string), "leak") {
		t.Fatalf("outbound response secret not redacted: %v", e["respBody"])
	}
}

func TestRotator(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "http.log")
	rw, err := newRotatingWriter(Config{File: path, MaxSize: 200, MaxBackups: 3, Compress: false})
	if err != nil {
		t.Fatal(err)
	}
	defer rw.Close()
	for i := 0; i < 20; i++ {
		if _, err := rw.Write([]byte(strings.Repeat("x", 50) + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(100 * time.Millisecond) // let async prune run
	ents, _ := os.ReadDir(dir)
	rotated := 0
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "http.log.") {
			rotated++
		}
	}
	if rotated == 0 {
		t.Fatal("nothing rotated")
	}
	if rotated > 3 {
		t.Fatalf("maxBackups=3 not enforced, %d rotated files", rotated)
	}
}
