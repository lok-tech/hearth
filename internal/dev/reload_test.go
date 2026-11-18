package dev

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orelhadad/hearth/internal/testutil"
)

type syncWriter struct {
	mu  sync.Mutex
	hdr http.Header
	buf bytes.Buffer
}

func (w *syncWriter) Header() http.Header { return w.hdr }
func (w *syncWriter) WriteHeader(int)     {}
func (w *syncWriter) Flush()              {}
func (w *syncWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(b)
}
func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestInjectsScriptIntoHTMLOnly(t *testing.T) {
	rl := newReloader()
	h := rl.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".css") {
			w.Header().Set("Content-Type", "text/css")
			w.Write([]byte("a{}</body>"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", "999")
		w.WriteHeader(404)
		w.Write([]byte("<html><body>hi</body></html>"))
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code != 404 || !strings.Contains(w.Body.String(), "hi"+reloadScript+"</body>") {
		t.Errorf("html: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Content-Length") == "999" {
		t.Error("stale Content-Length")
	}
	if c := testutil.Get(h, "/a.css"); c.Body.String() != "a{}</body>" {
		t.Errorf("css modified: %s", c.Body)
	}
}

func TestReloadFiresOnSourceChangeAndManifestChange(t *testing.T) {
	reloadPoll, reloadSettle = 20*time.Millisecond, 40*time.Millisecond
	defer func() { reloadPoll, reloadSettle = 300*time.Millisecond, 150*time.Millisecond }()
	site := t.TempDir()
	out := filepath.Join(site, "out")
	os.MkdirAll(filepath.Join(site, "pages"), 0o755)
	os.MkdirAll(filepath.Join(out, ".vite"), 0o755)
	os.WriteFile(filepath.Join(site, "pages", "a.html"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(out, ".vite", "manifest.json"), []byte("{}"), 0o644)

	rl := newReloader()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rl.watch(ctx, site, out)

	rctx, rcancel := context.WithCancel(ctx)
	sw := &syncWriter{hdr: http.Header{}}
	go rl.middleware(http.NotFoundHandler()).ServeHTTP(sw, httptest.NewRequest("GET", reloadPath, nil).WithContext(rctx))
	defer rcancel()
	count := func() int { return strings.Count(sw.String(), "event: reload") }
	waitFor := func(n int, what string) {
		t.Helper()
		for i := 0; i < 150 && count() < n; i++ {
			time.Sleep(20 * time.Millisecond)
		}
		if count() < n {
			t.Fatalf("no reload after %s (stream: %q)", what, sw.String())
		}
	}
	time.Sleep(150 * time.Millisecond)
	os.WriteFile(filepath.Join(site, "pages", "a.html"), []byte("22"), 0o644)
	waitFor(1, "template edit")
	os.WriteFile(filepath.Join(out, ".vite", "manifest.json"), []byte(`{"x":1}`), 0o644)
	waitFor(2, "new Vite build")

	// the manifest disappearing mid-rebuild must not reload
	os.Remove(filepath.Join(out, ".vite", "manifest.json"))
	time.Sleep(300 * time.Millisecond)
	if count() != 2 {
		t.Errorf("reloaded during rebuild window: %d", count())
	}
}
