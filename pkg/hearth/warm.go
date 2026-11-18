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
func (s *Site) Warm(ctx context.Context, paths []string, concurrency int) WarmReport {
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
// SitemapEntries from Options. Dynamic pages are only warmed if listed there.
func (s *Site) WarmPaths(ctx context.Context) ([]string, error) {
	var out []string
	for _, pat := range s.routes.Patterns() {
		pg, _ := s.routes.Lookup(pat)
		if !pg.IsDynamic() {
			out = append(out, pg.URL())
		}
	}
	if s.opt.SitemapEntries != nil {
		extra, err := s.opt.SitemapEntries(ctx)
		if err != nil {
			return nil, fmt.Errorf("hearth: warm: %w", err)
		}
		out = append(out, extra...)
	}
	sort.Strings(out)
	return out, nil
}
