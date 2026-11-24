package hearth

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func flatSite(t *testing.T, dir, tmp string, db *fakeDB) *Site {
	t.Helper()
	s, err := New(Options{Bundle: siteBundle(), CacheDir: dir, TmpDir: tmp, BaseURL: "https://x.test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Handle("post/{id}", db.load); err != nil {
		t.Fatal(err)
	}
	return s
}

// The cache is plain files under CacheDir: a restart keeps them, and there is no versions
// directory or current symlink for a web server to be pointed at.
func TestFlatCacheSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	db := &fakeDB{}
	get(flatSite(t, dir, "", db), "/post/1")
	if db.calls.Load() != 1 {
		t.Fatalf("calls = %d", db.calls.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, "post", "1", "index.html")); err != nil {
		t.Fatalf("page not at <dir>/post/1/index.html: %v", err)
	}
	for _, name := range []string{"current", "versions"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s must not exist in a flat cache", name)
		}
	}
	// a new Site over the same directory (a restart) answers from the file the first one wrote
	get(flatSite(t, dir, "", db), "/post/1")
	if db.calls.Load() != 1 {
		t.Fatalf("a restart must keep the warm cache; calls = %d", db.calls.Load())
	}
}

// CacheDir may be the directory a deploy fills with prebuilt files. Starting a Site and
// invalidating pages must never remove anything that is not a page's index.html.
func TestSharedDirectoryKeepsPrebuiltFiles(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"404.html": "nf", "sitemap.xml": "<s/>", "assets/app-abc.js": "js"} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db := &fakeDB{}
	s := flatSite(t, dir, "", db)
	get(s, "/post/1")
	if err := s.Invalidate("/post/1", "/", "/about"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"404.html", "sitemap.xml", "assets/app-abc.js"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
}

// Invalidate of a page that a deploy put there (prerendered) drops it, and the next request
// renders it again from current data.
func TestInvalidateDropsAPrebuiltPageAndItIsRenderedAgain(t *testing.T) {
	dir := t.TempDir()
	prebuilt := filepath.Join(dir, "post", "1", "index.html")
	_ = os.MkdirAll(filepath.Dir(prebuilt), 0o755)
	_ = os.WriteFile(prebuilt, []byte("prebuilt"), 0o644)
	db := &fakeDB{}
	s := flatSite(t, dir, "", db)
	if err := s.Invalidate("/post/1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(prebuilt); !os.IsNotExist(err) {
		t.Fatal("prebuilt page was not dropped")
	}
	if b := get(s, "/post/1").Body.String(); !strings.Contains(b, "post 1") {
		t.Fatalf("not re-rendered: %q", b)
	}
	if b, _ := os.ReadFile(prebuilt); !strings.Contains(string(b), "post 1") {
		t.Fatalf("fresh page not cached: %q", b)
	}
}

// With TmpDir set, no temp file ever appears under CacheDir.
func TestTmpDirKeepsTheServedTreeClean(t *testing.T) {
	root := t.TempDir()
	dir, tmp := filepath.Join(root, "public"), filepath.Join(root, "hearth-tmp")
	s := flatSite(t, dir, tmp, &fakeDB{})
	for _, p := range []string{"/post/1", "/", "/he/post/1"} {
		get(s, p)
	}
	_ = filepath.Walk(dir, func(p string, i os.FileInfo, err error) error {
		if err == nil && !i.IsDir() && i.Name() != "index.html" {
			t.Errorf("unexpected file in the served tree: %s", p)
		}
		return nil
	})
	if got := len(cachedFiles(t, dir)); got < 2 {
		t.Fatalf("cached files = %d, want at least 2", got)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("temp dir not empty: %v", left)
	}
}

func TestNewFailsOnAnUnusableTmpDir(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "file")
	_ = os.WriteFile(f, nil, 0o644)
	_, err := New(Options{Bundle: siteBundle(), CacheDir: filepath.Join(root, "public"), TmpDir: filepath.Join(f, "tmp"), BaseURL: "https://x.test"})
	if err == nil {
		t.Fatal("an unusable TmpDir must fail at startup")
	}
}

// A render that read the old data and is still running when Invalidate fires must not leave its
// output in the cache, and a request after the invalidation must not join it.
func TestInvalidateMidRender(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	val := atomic.Value{}
	val.Store("old")
	s, err := New(Options{Bundle: siteBundle(), CacheDir: t.TempDir(), BaseURL: "https://x.test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Handle("post/{id}", func(r *http.Request, p map[string]string) (any, Meta, error) {
		n := calls.Add(1)
		v := val.Load().(string)
		if n == 1 {
			close(started)
			<-release
		}
		return v, Meta{}, nil
	})
	done := make(chan string)
	go func() { done <- get(s, "/post/1").Body.String() }()
	<-started
	val.Store("new") // data first, then invalidate
	if err := s.Invalidate("/post/1"); err != nil {
		t.Fatal(err)
	}
	if b := get(s, "/post/1").Body.String(); !strings.Contains(b, "new") {
		t.Fatalf("request after Invalidate saw %q", b)
	}
	close(release)
	<-done
	if b := get(s, "/post/1").Body.String(); !strings.Contains(b, "new") {
		t.Fatalf("cache holds stale page: %q", b)
	}
	if s.Counters.StaleSkips.Load() != 1 {
		t.Fatalf("StaleSkips = %d", s.Counters.StaleSkips.Load())
	}
}
