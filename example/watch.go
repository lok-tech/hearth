package main

import (
	"context"
	"log/slog"
	"sort"
	"time"
)

// invalidator is what watchPosts needs from a *hearth.Site.
type invalidator interface{ Invalidate(paths ...string) error }

// watchPosts polls store.Version and, when it changes, invalidates the home page and every post
// page that may have changed: the slugs listed now and the ones listed before, so a deleted or
// renamed post's old page goes too. Pages refill lazily from the new data on the next visit.
func watchPosts(ctx context.Context, site invalidator, store Store, every time.Duration) {
	last, _ := store.Version(ctx)
	known, _ := store.Slugs(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		v, err := store.Version(ctx)
		if err != nil || v == last {
			continue
		}
		now, err := store.Slugs(ctx)
		if err != nil {
			continue
		}
		if err := site.Invalidate(postPaths(known, now)...); err != nil {
			slog.Error("invalidate", "err", err)
			continue // keep last, so the next tick tries again
		}
		last, known = v, now
	}
}

// postPaths is "/" plus "/post/<slug>" for every slug in either list, sorted.
func postPaths(lists ...[]string) []string {
	seen := map[string]bool{"/": true}
	out := []string{"/"}
	for _, l := range lists {
		for _, slug := range l {
			if p := "/post/" + slug; !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}
