package httplog

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// rotatingWriter is a minimal size + count + age log rotator (no external
// dependency - go.mod's local replace directives make adding one costly,
// see the Dockerfile's header comment). When the active file passes
// maxBytes it's renamed to "<path>.<timestamp>" (optionally gzipped) and a
// fresh file opened; backups beyond maxBackups, or older than maxAgeDays,
// are deleted. All writes are serialized by mu, so it's safe as the single
// sink behind the writer goroutine.
type rotatingWriter struct {
	mu         sync.Mutex
	path       string
	maxBytes   int64
	maxBackups int
	maxAgeDays int
	compress   bool
	f          *os.File
	size       int64
}

func newRotatingWriter(c Config) (*rotatingWriter, error) {
	r := &rotatingWriter{
		path:       c.File,
		maxBytes:   c.MaxSize,
		maxBackups: c.MaxBackups,
		maxAgeDays: c.MaxAgeDays,
		compress:   c.Compress,
	}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotatingWriter) open() error {
	if dir := filepath.Dir(r.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	sz := int64(0)
	if st, sErr := f.Stat(); sErr == nil {
		sz = st.Size()
	}
	r.f, r.size = f, sz
	return nil
}

func (r *rotatingWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	if r.maxBytes > 0 && r.size > 0 && r.size+int64(len(p)) > r.maxBytes {
		if err := r.rotate(); err != nil {
			fmt.Fprintf(os.Stderr, "httplog: rotate failed: %v\n", err)
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingWriter) rotate() error {
	_ = r.f.Close()
	rotated := fmt.Sprintf("%s.%s", r.path, time.Now().UTC().Format("20060102-150405.000"))
	if err := os.Rename(r.path, rotated); err != nil {
		// Reopen the original and give up on this rotation rather than
		// losing the sink entirely.
		return r.open()
	}
	if r.compress {
		go gzipInPlace(rotated)
	}
	go r.prune()
	return r.open()
}

// prune deletes rotated files (named "<base>.<...>") that are older than
// maxAgeDays or beyond the newest maxBackups.
func (r *rotatingWriter) prune() {
	dir := filepath.Dir(r.path)
	base := filepath.Base(r.path)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type bk struct {
		path string
		mod  time.Time
	}
	var backups []bk
	var cutoff time.Time
	if r.maxAgeDays > 0 {
		cutoff = time.Now().AddDate(0, 0, -r.maxAgeDays)
	}
	for _, e := range ents {
		if e.IsDir() || e.Name() == base || !strings.HasPrefix(e.Name(), base+".") {
			continue
		}
		info, iErr := e.Info()
		if iErr != nil {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if r.maxAgeDays > 0 && info.ModTime().Before(cutoff) {
			_ = os.Remove(full)
			continue
		}
		backups = append(backups, bk{full, info.ModTime()})
	}
	if r.maxBackups > 0 && len(backups) > r.maxBackups {
		sort.Slice(backups, func(i, j int) bool { return backups[i].mod.After(backups[j].mod) })
		for _, b := range backups[r.maxBackups:] {
			_ = os.Remove(b.path)
		}
	}
}

func (r *rotatingWriter) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

func gzipInPlace(path string) {
	in, err := os.Open(path)
	if err != nil {
		return
	}
	defer in.Close()
	out, err := os.OpenFile(path+".gz", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return
	}
	zw := gzip.NewWriter(out)
	if _, err := io.Copy(zw, in); err != nil {
		zw.Close()
		out.Close()
		_ = os.Remove(path + ".gz")
		return
	}
	zw.Close()
	out.Close()
	_ = os.Remove(path)
}
