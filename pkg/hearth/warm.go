package hearth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// WarmReport summarises a warm run.
type WarmReport struct {
	OK     int
	Failed map[string]int // path -> status (0 = transport/panic)
}

type discardWriter struct {
	h    http.Header
	code int
}

func (d *discardWriter) Header() http.Header         { return d.h }
func (d *discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardWriter) WriteHeader(c int)           { d.code = c }

// Warm renders and caches the given URL paths in the active version by sending
// them through the site's own handler, so it uses exactly the production path
// (singleflight, concurrency limit, only 200s written). concurrency bounds
// parallel requests on top of the render limit.
//
// With the page cache off (HEARTH_CACHE=off) nothing would be kept, so Warm does nothing and reports no pages.
func (s *Site) Warm(ctx context.Context, paths []string, concurrency int) WarmReport {
	if !s.keepsPages() {
		s.log.Debug("hearth: warm skipped, the page cache is off")
		return WarmReport{Failed: map[string]int{}}
	}
	if concurrency <= 0 {
		concurrency = 4
	}
	rep := WarmReport{Failed: map[string]int{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
loop:
	for _, p := range paths {
		if ctx.Err() != nil {
			rep.Failed[p] = 0
			continue
		}
		select {
		case <-ctx.Done():
			mu.Lock()
			rep.Failed[p] = 0
			mu.Unlock()
			continue loop
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			code := 0
			func() {
				defer func() { _ = recover() }()
				u, err := url.Parse(p)
				if err != nil || !strings.HasPrefix(p, "/") {
					return
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, p, nil)
				if err != nil {
					return
				}
				w := &discardWriter{h: http.Header{}, code: 200}
				req.URL = u
				s.ServeHTTP(w, req)
				code = w.code
			}()
			mu.Lock()
			defer mu.Unlock()
			if code == http.StatusOK {
				rep.OK++
			} else {
				rep.Failed[p] = code
			}
		}(p)
	}
	wg.Wait()
	return rep
}

// WarmPaths lists what a warm run covers by default: static pages plus any
// SitemapURLs/SitemapEntries from Options. Dynamic pages are only warmed if listed there.
func (s *Site) WarmPaths(ctx context.Context) ([]string, error) {
	var out []string
	for _, pat := range s.routes.Patterns() {
		pg, _ := s.routes.Lookup(pat)
		if !pg.IsDynamic() {
			out = append(out, pg.URL())
		}
	}
	extra, err := s.sitemapEntries(ctx)
	if err != nil {
		return nil, fmt.Errorf("hearth: warm: %w", err)
	}
	for _, e := range extra {
		out = append(out, e.Path)
	}
	sort.Strings(out)
	return out, nil
}

// WarmSitemap renders every page the sitemap lists, in every language, through the same engine
// a visitor's request uses (it is Prerender over the sitemap's merged path list: static pages,
// `ssr` pages, SitemapURLs and SitemapEntries). Use it at startup after a deploy emptied the
// cache directory:
//
//	go site.WarmSitemap(ctx, 4)
//
// Static and `ssr` pages are rendered first, then the listed pages, so a partial warm still covers
// the pages that matter most. concurrency bounds parallel pages on top of Options.MaxConcurrent
// and each render keeps Options.RenderTimeout. Cancelling ctx stops the run: pages not yet started
// are reported as failures with the context's error. A page invalidated while its render is
// running is not cached, so an Invalidate during the warm cannot be undone by it. A large site costs real time and data source load;
// the cache is already useful while the warm runs. An error from SitemapURLs/SitemapEntries is returned
// with an empty report.
//
// With the page cache off (HEARTH_CACHE=off) it returns an empty report at once, without rendering or
// calling SitemapURLs: warming exists to fill the cache, and nothing would be kept. The same line of code
// can therefore run in production and in development.
func (s *Site) WarmSitemap(ctx context.Context, concurrency int) (PrerenderReport, error) {
	if !s.keepsPages() {
		s.log.Debug("hearth: warm sitemap skipped, the page cache is off")
		return PrerenderReport{}, nil
	}
	var first, rest []string
	seen := map[string]bool{}
	for _, pat := range s.routes.Patterns() {
		pg, _ := s.routes.Lookup(pat)
		if !pg.IsDynamic() && !seen[pg.URL()] {
			seen[pg.URL()] = true
			first = append(first, pg.URL())
		}
	}
	extra, err := s.sitemapEntries(ctx)
	if err != nil {
		return PrerenderReport{}, fmt.Errorf("hearth: warm sitemap: %w", err)
	}
	for _, e := range extra {
		if !seen[e.Path] {
			seen[e.Path] = true
			rest = append(rest, e.Path)
		}
	}
	sort.Strings(first)
	return s.Prerender(ctx, append(first, rest...), concurrency), nil
}
