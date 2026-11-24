package cache

import (
	"errors"
	"testing"
)

// Both stores satisfy Store; a test below would not compile otherwise.
var (
	_ Store = (*Cache)(nil)
	_ Store = Nop{}
)

func TestCachePutGetAndStale(t *testing.T) {
	c, err := New(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("", "/a"); ok {
		t.Fatal("empty cache returned a page")
	}
	gen, _ := c.Gen("", "/a")
	if err := c.Put("", "/a", gen, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if b, ok := c.Get("", "/a"); !ok || string(b) != "one" {
		t.Fatalf("Get = %q, %v", b, ok)
	}
	// A render that read its generation before an Invalidate must not put its page back.
	gen, _ = c.Gen("", "/a")
	if err := c.Invalidate("", "/a"); err != nil {
		t.Fatal(err)
	}
	if err := c.Put("", "/a", gen, []byte("stale")); !errors.Is(err, ErrStale) {
		t.Fatalf("Put after Invalidate = %v, want ErrStale", err)
	}
	if _, ok := c.Get("", "/a"); ok {
		t.Fatal("stale page was kept")
	}
}

func TestNopKeepsNothing(t *testing.T) {
	n := Nop{}
	if err := n.Put("", "/a", 0, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, ok := n.Get("", "/a"); ok {
		t.Fatal("Nop returned a page")
	}
	if err := n.Invalidate("", "/a"); err != nil || n.Root() != "" {
		t.Fatalf("Invalidate = %v, Root = %q", err, n.Root())
	}
}
