package hearth

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
