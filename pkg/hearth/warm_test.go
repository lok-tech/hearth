package hearth

import (
	"context"
	"testing"
)

func TestWarmFillsCacheAndReportsFailures(t *testing.T) {
	db := &fakeDB{}
	s, dir := newTestSite(t, db)
	rep := s.Warm(context.Background(), []string{"/", "/post/1", "/post/missing", "bad"}, 2)
	if rep.OK != 2 {
		t.Fatalf("ok = %d, report %+v", rep.OK, rep)
	}
	if rep.Failed["/post/missing"] != 404 || len(rep.Failed) != 2 {
		t.Fatalf("failed = %v", rep.Failed)
	}
	if n := len(cachedFiles(t, dir)); n != 2 {
		t.Fatalf("cached files = %d, want 2", n)
	}
	before := db.calls.Load()
	get(s, "/post/1")
	if db.calls.Load() != before {
		t.Fatal("warmed page should be served from file")
	}
}

func TestWarmCancelled(t *testing.T) {
	s, _ := newTestSite(t, &fakeDB{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep := s.Warm(ctx, []string{"/"}, 1)
	if rep.OK != 0 || len(rep.Failed) != 1 {
		t.Fatalf("%+v", rep)
	}
}

func TestWarmPaths(t *testing.T) {
	s, _ := newTestSite(t, &fakeDB{})
	p, err := s.WarmPaths(context.Background())
	if err != nil || len(p) != 1 || p[0] != "/" {
		t.Fatalf("%v %v", p, err)
	}
}
