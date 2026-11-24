package hearth

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

func siteBundle() fstest.MapFS {
	return fstest.MapFS{
		"manifest.json":        {Data: []byte(`{"format":3}`)},
		"layouts/base.html":    {Data: []byte(`<title>{{block "title" .}}x{{end}}</title><link rel="canonical" href="{{.Canonical}}">{{block "body" .}}{{end}}`)},
		"pages/index.html":     {Data: []byte(`{{define "body"}}home{{end}}`)},
		"pages/post/[id].html": {Data: []byte(`{{define "title"}}{{.Data}}{{end}}{{define "body"}}post {{index .Params "id"}}: {{.Data}}{{end}}`)},
	}
}

type fakeDB struct {
	calls atomic.Int64
	delay time.Duration
}

func (f *fakeDB) load(r *http.Request, p map[string]string) (any, Meta, error) {
	f.calls.Add(1)
	time.Sleep(f.delay)
	switch p["id"] {
	case "1":
		return "Hello", Meta{}, nil
	case "old":
		return nil, Meta{}, Redirect(301, "/post/1")
	case "gone":
		return nil, Meta{}, ErrGone
	case "boom":
		return nil, Meta{}, io.ErrUnexpectedEOF
	}
	return nil, Meta{}, ErrNotFound
}

func newTestSite(t *testing.T, db *fakeDB) (*Site, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := New(Options{Bundle: siteBundle(), CacheDir: dir, BaseURL: "https://x.test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Handle("post/{id}", db.load); err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func get(s http.Handler, path string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

func cachedFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(dir, func(p string, i os.FileInfo, err error) error {
		if err == nil && !i.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func TestRenderWriteThenServeFromFile(t *testing.T) {
	db := &fakeDB{}
	s, dir := newTestSite(t, db)
	w := get(s, "/post/1")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "post 1: Hello") {
		t.Fatalf("first: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `href="https://x.test/post/1"`) {
		t.Errorf("canonical missing: %s", w.Body)
	}
	if len(cachedFiles(t, dir)) != 1 {
		t.Fatalf("expected one cached file, got %v", cachedFiles(t, dir))
	}
	w2 := get(s, "/post/1")
	if w2.Body.String() != w.Body.String() || db.calls.Load() != 1 {
		t.Fatalf("second hit should come from file; calls=%d", db.calls.Load())
	}
	if w2.Header().Get("ETag") == "" || w2.Header().Get("Cache-Control") == "" {
		t.Error("missing ETag/cache.Cache-Control")
	}
	if w3 := get(s, "/post/1", "If-None-Match", w2.Header().Get("ETag")); w3.Code != 304 {
		t.Errorf("conditional = %d", w3.Code)
	}
}

func TestNon200sAreNeverCached(t *testing.T) {
	db := &fakeDB{}
	s, dir := newTestSite(t, db)
	if w := get(s, "/post/missing"); w.Code != 404 {
		t.Errorf("404 = %d", w.Code)
	}
	if w := get(s, "/nope"); w.Code != 404 {
		t.Errorf("unrouted = %d", w.Code)
	}
	if w := get(s, "/post/gone"); w.Code != 410 {
		t.Errorf("gone = %d", w.Code)
	}
	if w := get(s, "/post/boom"); w.Code != 500 {
		t.Errorf("boom = %d", w.Code)
	}
	w := get(s, "/post/old")
	if w.Code != 301 || w.Header().Get("Location") != "/post/1" {
		t.Errorf("redirect = %d %s", w.Code, w.Header().Get("Location"))
	}
	if f := cachedFiles(t, dir); len(f) != 0 {
		t.Fatalf("non-200 responses wrote files: %v", f)
	}
}

func TestSingleflightCollapsesConcurrentMisses(t *testing.T) {
	db := &fakeDB{delay: 100 * time.Millisecond}
	s, _ := newTestSite(t, db)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := get(s, "/post/1"); w.Code != 200 {
				t.Errorf("code %d", w.Code)
			}
		}()
	}
	wg.Wait()
	if n := db.calls.Load(); n != 1 {
		t.Fatalf("handler called %d times, want 1", n)
	}
}

func TestUnsafeAndMisc(t *testing.T) {
	s, _ := newTestSite(t, &fakeDB{})
	if w := get(s, "/post/1/"); w.Code != 301 {
		t.Errorf("trailing slash = %d", w.Code)
	}
	req := httptest.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Errorf("POST = %d", w.Code)
	}
	if w := get(s, "/healthz"); w.Code != 200 || !strings.HasPrefix(w.Body.String(), "ok") {
		t.Errorf("healthz = %d %s", w.Code, w.Body)
	}
	if w := get(s, "/robots.txt"); !strings.Contains(w.Body.String(), "Sitemap: https://x.test/sitemap.xml") {
		t.Errorf("robots = %s", w.Body)
	}
	if w := get(s, "/sitemap.xml"); !strings.Contains(w.Body.String(), "<loc>https://x.test/</loc>") {
		t.Errorf("sitemap = %s", w.Body)
	}
}

func TestHandlerTypoFailsAtStartup(t *testing.T) {
	s, _ := newTestSite(t, &fakeDB{})
	if err := s.Handle("post/{slug}", (&fakeDB{}).load); err == nil {
		t.Fatal("typo must fail")
	}
}

// The documented trailing-slash behaviour (docs/DEPLOY.md): a 301 to the same path without the
// slash, "/" excepted, the query string is not carried over, and several slashes collapse at once.
func TestTrailingSlashRedirectShape(t *testing.T) {
	s, _ := newTestSite(t, &fakeDB{})
	for _, c := range []struct{ in, to string }{
		{"/post/1/", "/post/1"},
		{"/post/1/?utm=a", "/post/1"},
		{"/post/1//", "/post/1"},
	} {
		w := get(s, c.in)
		if w.Code != 301 || w.Header().Get("Location") != c.to {
			t.Errorf("%s: %d Location %q, want 301 %q", c.in, w.Code, w.Header().Get("Location"), c.to)
		}
	}
	if w := get(s, "/"); w.Code == 301 {
		t.Errorf("/ must not redirect")
	}
}
