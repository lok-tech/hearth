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

func siteWithBundleVersion(t *testing.T, dir, bundleVer string, db *fakeDB, keep int) *Site {
	t.Helper()
	b := siteBundle()
	b["manifest.json"].Data = []byte(`{"format":1,"version":"` + bundleVer + `"}`)
	s, err := New(Options{Bundle: b, CacheDir: dir, BaseURL: "https://x.test", KeepVersions: keep,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Handle("post/{id}", db.load); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRestartKeepsCacheButNewBundleDropsIt(t *testing.T) {
	dir := t.TempDir()
	db := &fakeDB{}
	s1 := siteWithBundleVersion(t, dir, "aaa", db, 2)
	get(s1, "/post/1")
	if db.calls.Load() != 1 {
		t.Fatalf("calls = %d", db.calls.Load())
	}
	// data-driven invalidation survives a restart of the same bundle
	if err := s1.SetVersion("v7"); err != nil {
		t.Fatal(err)
	}
	get(s1, "/post/1")
	if db.calls.Load() != 2 {
		t.Fatalf("calls after flip = %d", db.calls.Load())
	}
	s2 := siteWithBundleVersion(t, dir, "aaa", db, 2)
	get(s2, "/post/1")
	if db.calls.Load() != 2 {
		t.Fatalf("same bundle restart must keep the warm cache; calls = %d", db.calls.Load())
	}
	// a redeploy with a new bundle must not serve pages rendered by the old one
	s3 := siteWithBundleVersion(t, dir, "bbb", db, 2)
	get(s3, "/post/1")
	if db.calls.Load() != 3 {
		t.Fatalf("new bundle must re-render; calls = %d", db.calls.Load())
	}
	if v, _ := s3.cache.Version(); !strings.HasPrefix(v, "bbb.") {
		t.Fatalf("version = %q", v)
	}
}

func TestOldVersionsArePruned(t *testing.T) {
	dir := t.TempDir()
	s := siteWithBundleVersion(t, dir, "aaa", &fakeDB{}, 1)
	for _, v := range []string{"v1", "v2", "v3", "v4"} {
		get(s, "/post/1")
		if err := s.SetVersion(v); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "versions"))
	if len(entries) != 2 { // current + 1 kept
		t.Fatalf("versions on disk = %d, want 2", len(entries))
	}
	if err := s.SetVersion("../x"); err == nil {
		t.Fatal("bad version must fail")
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
