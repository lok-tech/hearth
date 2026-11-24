package hearth

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

func warmSite(t *testing.T, calls *atomic.Int64, h Handler) (*Site, string) {
	t.Helper()
	if h == nil {
		h = func(*http.Request, map[string]string) (any, Meta, error) { calls.Add(1); return "Ann", Meta{}, nil }
	}
	post := func(r *http.Request, p map[string]string) (any, Meta, error) {
		calls.Add(1)
		if p["id"] == "boom" {
			return nil, Meta{}, io.ErrUnexpectedEOF
		}
		return "P" + p["id"], Meta{}, nil
	}
	s, dir := newSSRSite(t, func(o *Options) {
		o.SitemapURLs = func(context.Context) ([]SitemapEntry, error) {
			return []SitemapEntry{{Path: "/post/2"}, {Path: "/post/1"}, {Path: "/post/boom"}, {Path: "/about"}}, nil
		}
		o.SitemapEntries = func(context.Context) ([]string, error) { return []string{"/post/1"}, nil }
	})
	s.MustHandle("/candidates", h)
	s.MustHandle("post/{id}", post)
	return s, dir
}

func TestWarmSitemapMakesEverySitemapPathAHit(t *testing.T) {
	var calls atomic.Int64
	s, dir := warmSite(t, &calls, nil)
	rep, err := s.WarmSitemap(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	// 5 distinct paths (/, /about, /candidates, /post/1, /post/2) x 2 languages; /post/boom fails in both
	if rep.OK != 10 || len(rep.Failures) != 2 || rep.Failures[0].Path != "/post/boom" {
		t.Fatalf("report %+v", rep)
	}
	if n := len(cachedFiles(t, dir)); n != 10 {
		t.Fatalf("cached files = %d, want 10", n)
	}
	// every page from the sitemap is now a hit: Hits rises, Renders and handler calls do not
	renders, hits, before := s.Counters.Renders.Load(), s.Counters.Hits.Load(), calls.Load()
	var sm struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(get(s, "/sitemap.xml").Body.Bytes(), &sm); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, u := range sm.URLs {
		p := strings.TrimPrefix(u.Loc, "https://x.test")
		if strings.HasSuffix(p, "/post/boom") {
			continue
		}
		if w := get(s, p); w.Code != 200 {
			t.Fatalf("%s: %d", p, w.Code)
		}
		n++
	}
	if n != 10 || s.Counters.Hits.Load()-hits != int64(n) || s.Counters.Renders.Load() != renders || calls.Load() != before {
		t.Fatalf("n=%d hits +%d renders %d->%d calls %d->%d", n, s.Counters.Hits.Load()-hits, renders, s.Counters.Renders.Load(), before, calls.Load())
	}
}

func TestWarmSitemapWarmsSSRPageThroughItsHandler(t *testing.T) {
	var calls atomic.Int64
	s, _ := warmSite(t, &calls, func(*http.Request, map[string]string) (any, Meta, error) {
		calls.Add(100)
		return "FromHandler", Meta{}, nil
	})
	if _, err := s.WarmSitemap(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	if w := get(s, "/candidates"); !strings.Contains(w.Body.String(), "list FromHandler") || calls.Load() != before {
		t.Fatalf("%s (calls %d -> %d)", w.Body, before, calls.Load())
	}
	if before < 200 { // once per language
		t.Fatalf("handler ran %d times", before)
	}
}

func TestWarmSitemapOrdersSSRBeforeListedPages(t *testing.T) {
	var calls atomic.Int64
	var mu sync.Mutex
	var order []string
	note := func(n string) {
		mu.Lock()
		order = append(order, n)
		mu.Unlock()
	}
	s, _ := warmSite(t, &calls, nil)
	s.MustHandle("/candidates", func(*http.Request, map[string]string) (any, Meta, error) { note("ssr"); return "x", Meta{}, nil })
	s.MustHandle("post/{id}", func(*http.Request, map[string]string) (any, Meta, error) { note("post"); return "x", Meta{}, nil })
	if _, err := s.WarmSitemap(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	// concurrency 1 runs the jobs in list order: both languages of the ssr page, then the posts
	if got := strings.Join(order, ","); !strings.HasPrefix(got, "ssr,ssr,post,") {
		t.Fatalf("order = %s", got)
	}
}

func TestWarmSitemapCancelStopsEarly(t *testing.T) {
	var calls atomic.Int64
	s, _ := warmSite(t, &calls, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep, err := s.WarmSitemap(ctx, 2)
	if err != nil || rep.OK != 0 || len(rep.Failures) == 0 || calls.Load() != 0 {
		t.Fatalf("%+v %v calls=%d", rep, err, calls.Load())
	}
	if !errors.Is(rep.Failures[0], context.Canceled) {
		t.Fatalf("failure %v", rep.Failures[0])
	}
}

func TestWarmSitemapEntriesErrorIsReturned(t *testing.T) {
	s, _ := newSSRSite(t, func(o *Options) {
		o.SitemapURLs = func(context.Context) ([]SitemapEntry, error) { return nil, io.ErrClosedPipe }
	})
	if _, err := s.WarmSitemap(context.Background(), 1); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("err = %v", err)
	}
}

func TestWarmSitemapInvalidateMidWarmDoesNotCacheTheStalePage(t *testing.T) {
	var calls atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	s, dir := newSSRSite(t, func(o *Options) {
		o.SitemapURLs = func(context.Context) ([]SitemapEntry, error) { return []SitemapEntry{{Path: "/post/1"}}, nil }
		o.I18n = I18n{Mode: "prefix"}
	})
	var once sync.Once
	s.MustHandle("/candidates", func(*http.Request, map[string]string) (any, Meta, error) { return "x", Meta{}, nil })
	s.MustHandle("post/{id}", func(*http.Request, map[string]string) (any, Meta, error) {
		calls.Add(1)
		once.Do(func() { close(started); <-release })
		return "x", Meta{}, nil
	})
	done := make(chan PrerenderReport)
	go func() { rep, _ := s.WarmSitemap(context.Background(), 1); done <- rep }()
	<-started
	if err := s.Invalidate("/post/1"); err != nil {
		t.Fatal(err)
	}
	close(release)
	rep := <-done
	if rep.OK == 0 {
		t.Fatalf("%+v", rep)
	}
	// The first /post/1 (en) began rendering before the invalidation, so it read data that may
	// be stale and must not have been cached.
	if _, err := os.Stat(filepath.Join(dir, "post", "1", "index.html")); err == nil {
		t.Fatal("page rendered before the invalidation was cached")
	}
}
