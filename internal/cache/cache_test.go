package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCachePath(t *testing.T) {
	ok := map[string]string{
		"/":          "index.html",
		"/x":         "x/index.html",
		"/x/":        "x/index.html",
		"/a/b/c":     "a/b/c/index.html",
		"/post/42//": "post/42/index.html",
	}
	for in, want := range ok {
		got, err := RelPath("", in)
		if err != nil || got != filepath.FromSlash(want) {
			t.Errorf("CachePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{"", "x", "/../etc/passwd", "/a/../b", "/a/./b", "/a\\b", "/a\x00b", "/%2e%2e"}
	for _, in := range bad[:7] {
		if _, err := RelPath("", in); err == nil {
			t.Errorf("CachePath(%q) should fail", in)
		}
	}
	if p, err := RelPath("he.example.com", "/about"); err != nil || p != filepath.FromSlash("he.example.com/about/index.html") {
		t.Errorf("host path = %q, %v", p, err)
	}
	for _, h := range []string{"../x", "a/b", "A.com", ".x", "a..b"} {
		if _, err := RelPath(h, "/"); err == nil {
			t.Errorf("host %q should fail", h)
		}
	}
}

func TestCacheWriteIsFlat(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write("", "/about", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "about", "index.html")); err != nil || string(b) != "one" {
		t.Fatalf("page not at <dir>/about/index.html: %q, %v", b, err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*", ".tmp-*")); len(m) != 0 {
		t.Fatalf("leftover temp files: %v", m)
	}
	if _, err := os.Stat(filepath.Join(dir, "current")); !os.IsNotExist(err) {
		t.Fatal("a flat cache must not create a current symlink")
	}
	if _, err := os.Stat(filepath.Join(dir, "versions")); !os.IsNotExist(err) {
		t.Fatal("a flat cache must not create a versions directory")
	}
}

func TestTempDirKeepsTempFilesOutOfTheServedTree(t *testing.T) {
	root := t.TempDir()
	dir, tmp := filepath.Join(root, "public"), filepath.Join(root, "hearth-tmp")
	c, err := New(dir, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write("", "/post/1", []byte("body")); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "post", "1", "index.html")); err != nil || string(b) != "body" {
		t.Fatalf("page = %q, %v", b, err)
	}
	// nothing but the page tree is under dir: no dotfiles, no probe
	err = filepath.Walk(dir, func(p string, i os.FileInfo, err error) error {
		if err == nil && !i.IsDir() && i.Name() != "index.html" {
			t.Errorf("unexpected file in the served tree: %s", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("temp dir not empty after a write: %v", left)
	}
}

func TestNewSweepsOnlyStaleTempFiles(t *testing.T) {
	root := t.TempDir()
	tmp := filepath.Join(root, "hearth-tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	old, fresh := filepath.Join(tmp, "page-old"), filepath.Join(tmp, "page-fresh")
	for _, f := range []string{old, fresh} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if _, err := New(filepath.Join(root, "public"), tmp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("a stale temp file was not removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a recent temp file (maybe a live write) was removed")
	}
}

func TestNewFailsWhenTempDirIsNotUsable(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(filepath.Join(root, "public"), filepath.Join(file, "tmp")); err == nil {
		t.Fatal("a temp dir under a regular file must be an error")
	}
}

func TestInvalidateLeavesOtherFilesAlone(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, "")
	other := filepath.Join(dir, "assets", "app.js")
	_ = os.MkdirAll(filepath.Dir(other), 0o755)
	_ = os.WriteFile(other, []byte("js"), 0o644)
	_ = c.Write("", "/p", []byte("x"))
	if err := c.Invalidate("", "/p"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("Invalidate removed a file that is not a page")
	}
}

func TestInvalidateBlocksLateStaleWrite(t *testing.T) {
	c, _ := New(t.TempDir(), "")
	gen, _ := c.Gen("", "/p")
	// the render read old data; the data then changed and the page was invalidated (nothing cached yet)
	if err := c.Invalidate("", "/p"); err != nil {
		t.Fatal(err)
	}
	ok, err := c.WriteGen("", "/p", gen, []byte("stale"))
	if err != nil || ok {
		t.Fatalf("stale write: ok=%v err=%v", ok, err)
	}
	p, _ := c.Path("", "/p")
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("stale page was cached")
	}
	// a render that began after the invalidation is accepted
	gen, _ = c.Gen("", "/p")
	if ok, err := c.WriteGen("", "/p", gen, []byte("fresh")); err != nil || !ok {
		t.Fatalf("fresh write: ok=%v err=%v", ok, err)
	}
	// invalidating a cached page removes it; other pages stay
	_ = c.Write("", "/other", []byte("x"))
	if err := c.Invalidate("", "/p"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("page not removed")
	}
	op, _ := c.Path("", "/other")
	if _, err := os.Stat(op); err != nil {
		t.Fatal("unrelated page was removed")
	}
}
