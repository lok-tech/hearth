package main

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Memory is a Store in memory: the no-database demo (STORE=memory) and the tests' fake database.
type Memory struct {
	mu        sync.RWMutex
	posts     map[string]Post
	gone      map[string]bool
	redirects map[string]string
	Lookups   int // how many times Post was called (tests use it to prove a page came from a file)
}

func NewMemory() *Memory {
	return &Memory{posts: map[string]Post{}, gone: map[string]bool{}, redirects: map[string]string{}}
}

// Seed fills the demo content.
func (m *Memory) Seed() *Memory {
	now := time.Now().UTC().Truncate(time.Microsecond)
	m.Put(Post{Slug: "hello-world", Title: "Hello, world", UpdatedAt: now,
		Body: "This page was rendered from the database once, written to a file, and every later request is served from that file."})
	m.Put(Post{Slug: "static-speed", Title: "Static speed, dynamic content", UpdatedAt: now,
		Body: "Change a row, flip the version, and the next visit renders it again."})
	m.Redirect("hello", "hello-world")
	m.Delete("retired-post")
	return m
}

func (m *Memory) Put(p Post) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
	}
	delete(m.gone, p.Slug)
	m.posts[p.Slug] = p
}

func (m *Memory) Delete(slug string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.posts, slug)
	m.gone[slug] = true
}

func (m *Memory) Redirect(old, to string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.redirects[old] = to
}

func (m *Memory) Post(_ context.Context, slug string) (Post, error) {
	m.mu.Lock()
	m.Lookups++
	defer m.mu.Unlock()
	if p, ok := m.posts[slug]; ok {
		return p, nil
	}
	if to, ok := m.redirects[slug]; ok {
		return Post{}, &MovedError{To: to}
	}
	if m.gone[slug] {
		return Post{}, ErrGone
	}
	return Post{}, ErrNotFound
}

func (m *Memory) Slugs(context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.posts))
	for s := range m.posts {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

func (m *Memory) Version(context.Context) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var latest time.Time
	for _, p := range m.posts {
		if p.UpdatedAt.After(latest) {
			latest = p.UpdatedAt
		}
	}
	return versionOf(latest, len(m.posts)+len(m.gone)+len(m.redirects)), nil
}
