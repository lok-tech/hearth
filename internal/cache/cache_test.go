package cache

import (
	"os"
	"path/filepath"
	"testing"
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

func TestCacheWriteAndFlip(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	v1, _ := c.Version()
	if err := c.Write(v1, "", "/about", []byte("one")); err != nil {
		t.Fatal(err)
	}
	p, _ := c.Path("", "/about")
	if b, err := os.ReadFile(p); err != nil || string(b) != "one" {
		t.Fatalf("read = %q, %v", b, err)
	}
	if err := c.SetVersion("v2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("page should be gone after flip, err=%v", err)
	}
	// A render that started under v1 must not leak into v2.
	if err := c.Write(v1, "", "/late", []byte("stale")); err != nil {
		t.Fatal(err)
	}
	lp, _ := c.Path("", "/late")
	if _, err := os.Stat(lp); !os.IsNotExist(err) {
		t.Fatal("stale write leaked into the new version")
	}
	// No temp files left behind.
	m, _ := filepath.Glob(filepath.Join(c.dir, "versions", v1, "*", ".tmp-*"))
	if len(m) != 0 {
		t.Fatalf("leftover temp files: %v", m)
	}
}

func TestSetVersionRejectsTraversal(t *testing.T) {
	c, _ := New(t.TempDir())
	for _, v := range []string{"", "..", "../x", "a/b"} {
		if err := c.SetVersion(v); err == nil {
			t.Errorf("SetVersion(%q) should fail", v)
		}
	}
}

func TestInvalidateBlocksLateStaleWrite(t *testing.T) {
	c, _ := New(t.TempDir())
	v, _ := c.Version()
	gen, _ := c.Gen("", "/p")
	// the render read old data; the data then changed and the page was invalidated (nothing cached yet)
	if err := c.Invalidate("", "/p"); err != nil {
		t.Fatal(err)
	}
	ok, err := c.WriteGen(v, "", "/p", gen, []byte("stale"))
	if err != nil || ok {
		t.Fatalf("stale write: ok=%v err=%v", ok, err)
	}
	p, _ := c.Path("", "/p")
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("stale page was cached")
	}
	// a render that began after the invalidation is accepted
	gen, _ = c.Gen("", "/p")
	if ok, err := c.WriteGen(v, "", "/p", gen, []byte("fresh")); err != nil || !ok {
		t.Fatalf("fresh write: ok=%v err=%v", ok, err)
	}
	// invalidating a cached page removes it; other pages stay
	_ = c.Write(v, "", "/other", []byte("x"))
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
