package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Post is what the post template receives as .Data.
type Post struct {
	Slug      string
	Title     string
	Body      string // plain text; the template escapes it (never mark user content as HTML)
	UpdatedAt time.Time
}

var (
	// ErrNotFound: no such post, never was.
	ErrNotFound = errors.New("post not found")
	// ErrGone: the post existed and was deleted on purpose (answers 410).
	ErrGone = errors.New("post gone")
)

// MovedError: the post was renamed (answers 301 to the new slug).
type MovedError struct{ To string }

func (e *MovedError) Error() string { return "post moved to " + e.To }

// Store is everything the server needs from a database. Postgres implements it for real
// (postgres.go); memory.go implements it for the demo and the tests.
type Store interface {
	// Post returns the post, ErrNotFound, ErrGone or a *MovedError.
	Post(ctx context.Context, slug string) (Post, error)
	// Slugs lists every published post, for the sitemap (deleted posts must not be in it).
	Slugs(ctx context.Context) ([]string, error)
	// Version is a short token that changes whenever any post changes. The server flips its
	// page cache when the token changes, so pages refill from the new data.
	Version(ctx context.Context) (string, error)
}

func versionOf(maxUpdated time.Time, count int) string {
	return fmt.Sprintf("%x-%d", maxUpdated.UnixMicro(), count) // letters, digits, "-": a valid cache version
}
