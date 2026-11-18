package main

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orelhadad/hearth/pkg/hearth"
)

// These are HTTP-level tests: real requests through the real Site and handler, a fake database
// (the Memory store), and the example's own templates. Nothing is mocked in between.

func newServer(t *testing.T) (*hearth.Site, *Memory, string) {
	t.Helper()
	store := NewMemory().Seed()
	dir := t.TempDir()
	site, err := hearth.New(hearth.Options{
		Bundle:        os.DirFS("site"), // the source tree is a valid bundle when assets are lenient
		LenientAssets: true,
		CacheDir:      dir,
		BaseURL:       "https://blog.test",
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	site.MustHandle("post/{slug}", postHandler(store))
	return site, store, dir
}

func get(h http.Handler, path string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// pagesOnDisk lists the files the cache holds for the active version.
func pagesOnDisk(t *testing.T, dir string) []string {
	t.Helper()
	root, err := filepath.EvalSymlinks(filepath.Join(dir, "current"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

func TestRendersFromTheDatabaseAndWritesAFile(t *testing.T) {
	site, store, dir := newServer(t)
	w := get(site, "/post/hello-world")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, want := range []string{"<h1>Hello, world</h1>", `rel="canonical" href="https://blog.test/post/hello-world"`,
		`hreflang="he" href="https://blog.test/he/post/hello-world"`, `lang="en"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if got := pagesOnDisk(t, dir); len(got) != 1 || got[0] != "post/hello-world/index.html" {
		t.Fatalf("files on disk = %v", got)
	}
	onDisk, _ := os.ReadFile(filepath.Join(dir, "current", "post", "hello-world", "index.html"))
	if string(onDisk) != body {
		t.Error("the file on disk is not what was served")
	}
	if store.Lookups != 1 {
		t.Errorf("database lookups = %d", store.Lookups)
	}
}

func TestSecondRequestIsServedFromTheFileNotTheDatabase(t *testing.T) {
	site, store, _ := newServer(t)
	first := get(site, "/post/hello-world")
	// even if the row changes underneath, the file wins until the version flips
	store.Put(Post{Slug: "hello-world", Title: "Changed behind the cache's back", Body: "x"})
	second := get(site, "/post/hello-world")
	if second.Body.String() != first.Body.String() {
		t.Error("second request did not come from the written file")
	}
	if store.Lookups != 1 {
		t.Errorf("database lookups = %d, want 1", store.Lookups)
	}
	if second.Header().Get("ETag") == "" || second.Header().Get("Cache-Control") == "" {
		t.Error("missing ETag or Cache-Control on a file hit")
	}
	if w := get(site, "/post/hello-world", "If-None-Match", second.Header().Get("ETag")); w.Code != 304 {
		t.Errorf("conditional request = %d", w.Code)
	}
}

func TestVersionBumpRefillsFromNewData(t *testing.T) {
	site, store, dir := newServer(t)
	get(site, "/post/hello-world")
	get(site, "/post/static-speed")
	before, _ := store.Version(context.Background())

	store.Put(Post{Slug: "hello-world", Title: "Hello again", Body: "edited"})
	after, _ := store.Version(context.Background())
	if before == after {
		t.Fatal("the version token must change when a post changes")
	}
	if err := site.SetVersion(after); err != nil {
		t.Fatal(err)
	}
	if got := pagesOnDisk(t, dir); len(got) != 0 {
		t.Fatalf("the flip should start an empty cache, found %v", got)
	}
	if w := get(site, "/post/hello-world"); !strings.Contains(w.Body.String(), "<h1>Hello again</h1>") {
		t.Errorf("stale page after the flip: %s", w.Body)
	}
	if got := pagesOnDisk(t, dir); len(got) != 1 {
		t.Errorf("pages refill lazily, one per visit; on disk: %v", got)
	}
}

type notifier func(ctx context.Context, notify func(string)) error

func (n notifier) Watch(ctx context.Context, notify func(string)) error { return n(ctx, notify) }

func TestSubscribeFlipsTheCacheWhenTheSourceReportsAChange(t *testing.T) {
	site, store, _ := newServer(t)
	get(site, "/post/hello-world")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changed := make(chan struct{})
	go site.Subscribe(ctx, notifier(func(ctx context.Context, notify func(string)) error {
		<-changed
		notify("v2")
		<-ctx.Done()
		return ctx.Err()
	}))
	store.Put(Post{Slug: "hello-world", Title: "Pushed", Body: "x"})
	close(changed)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(get(site, "/post/hello-world").Body.String(), "<h1>Pushed</h1>") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the page never refreshed after the source reported a change")
}

func TestNotFoundWritesNothing(t *testing.T) {
	site, store, dir := newServer(t)
	for _, p := range []string{"/post/nope", "/post/Bad_Slug", "/post/a%20b", "/post/UPPER", "/nothing-here"} {
		if w := get(site, p); w.Code != 404 {
			t.Errorf("%s = %d, want 404", p, w.Code)
		}
	}
	if got := pagesOnDisk(t, dir); len(got) != 0 {
		t.Fatalf("404s wrote files: %v", got)
	}
	// invalid slugs are rejected before the database is asked at all
	before := store.Lookups
	get(site, "/post/Bad_Slug")
	get(site, "/post/x'%3Bdrop%20table%20posts%3B--")
	if store.Lookups != before {
		t.Errorf("untrusted slugs reached the database (%d lookups)", store.Lookups-before)
	}
}

func TestRenamedPostRedirectsPermanentlyAndWritesNothing(t *testing.T) {
	site, _, dir := newServer(t)
	w := get(site, "/post/hello")
	if w.Code != 301 || w.Header().Get("Location") != "/post/hello-world" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Location"))
	}
	// the redirect keeps the visitor's language
	if w := get(site, "/he/post/hello"); w.Code != 301 || w.Header().Get("Location") != "/he/post/hello-world" {
		t.Errorf("he redirect = %d %q", w.Code, w.Header().Get("Location"))
	}
	if got := pagesOnDisk(t, dir); len(got) != 0 {
		t.Fatalf("redirects wrote files: %v", got)
	}
}

func TestDeletedPostIsGone(t *testing.T) {
	site, _, dir := newServer(t)
	if w := get(site, "/post/retired-post"); w.Code != 410 {
		t.Errorf("status %d, want 410", w.Code)
	}
	if got := pagesOnDisk(t, dir); len(got) != 0 {
		t.Errorf("410 wrote files: %v", got)
	}
}

func TestRedirectTargetsFromTheDatabaseAreNotTrusted(t *testing.T) {
	site, store, _ := newServer(t)
	for i, evil := range []string{"//evil.test/x", "https://evil.test", "/admin", "a/b", "A", ""} {
		old := "evil-" + string(rune('a'+i))
		store.Redirect(old, evil)
		w := get(site, "/post/"+old)
		if w.Code == 301 || w.Code == 302 || w.Header().Get("Location") != "" {
			t.Errorf("target %q produced a redirect to %q", evil, w.Header().Get("Location"))
		}
		if w.Code != 500 {
			t.Errorf("target %q answered %d, want 500", evil, w.Code)
		}
	}
}

func TestPostContentIsEscaped(t *testing.T) {
	site, store, _ := newServer(t)
	store.Put(Post{Slug: "xss", Title: `<img src=x onerror=alert(1)>`, Body: `<script>alert("hi")</script>`})
	body := get(site, "/post/xss").Body.String()
	if strings.Contains(body, "<script>alert") || strings.Contains(body, "<img src=x") {
		t.Fatalf("user content was not escaped:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected the escaped form of the script tag")
	}
}

func TestHebrewPagesAreRightToLeftAndLinkWithinTheLanguage(t *testing.T) {
	site, _, dir := newServer(t)
	w := get(site, "/he/post/hello-world")
	body := w.Body.String()
	for _, want := range []string{`lang="he"`, `dir="rtl"`, `rel="canonical" href="https://blog.test/he/post/hello-world"`, `href="/he/post/hello-world"`, "הפוסט הראשון"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if got := pagesOnDisk(t, dir); len(got) != 1 || got[0] != "he/post/hello-world/index.html" {
		t.Errorf("files = %v", got)
	}
}

func TestSitemapListsDatabasePostsInEveryLanguage(t *testing.T) {
	store := NewMemory().Seed()
	site, err := hearth.New(hearth.Options{Bundle: os.DirFS("site"), LenientAssets: true, CacheDir: t.TempDir(),
		BaseURL: "https://blog.test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		SitemapEntries: func(ctx context.Context) ([]string, error) {
			slugs, err := store.Slugs(ctx)
			paths := make([]string, len(slugs))
			for i, s := range slugs {
				paths[i] = "/post/" + s
			}
			return paths, err
		}})
	if err != nil {
		t.Fatal(err)
	}
	sm := get(site, "/sitemap.xml").Body.String()
	for _, want := range []string{"<loc>https://blog.test/post/hello-world</loc>", "<loc>https://blog.test/he/post/static-speed</loc>", "<loc>https://blog.test/</loc>"} {
		if !strings.Contains(sm, want) {
			t.Errorf("sitemap missing %s:\n%s", want, sm)
		}
	}
	if strings.Contains(sm, "retired-post") || strings.Contains(sm, "/post/hello</loc>") {
		t.Errorf("deleted or renamed slugs must not be listed:\n%s", sm)
	}
}

func TestStaticPagesSitemapAndHealth(t *testing.T) {
	site, _, _ := newServer(t)
	if w := get(site, "/"); w.Code != 200 || !strings.Contains(w.Body.String(), "Posts from the database") {
		t.Errorf("home: %d", w.Code)
	}
	if w := get(site, "/healthz"); w.Code != 200 || !strings.HasPrefix(w.Body.String(), "ok") {
		t.Errorf("healthz: %d", w.Code)
	}
	if w := get(site, "/robots.txt"); !strings.Contains(w.Body.String(), "Sitemap: https://blog.test/sitemap.xml") {
		t.Errorf("robots: %s", w.Body)
	}
	if w := get(site, "/post/hello-world/"); w.Code != 301 || w.Header().Get("Location") != "/post/hello-world" {
		t.Errorf("trailing slash: %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestAFailingDatabaseIsA500AndIsNeverCached(t *testing.T) {
	site, _, dir := newServer(t)
	site.MustHandle("post/{slug}", postHandler(failingStore{}))
	if w := get(site, "/post/hello-world"); w.Code != 500 {
		t.Errorf("status %d, want 500", w.Code)
	}
	if got := pagesOnDisk(t, dir); len(got) != 0 {
		t.Errorf("an error page was cached: %v", got)
	}
}

type failingStore struct{}

func (failingStore) Post(context.Context, string) (Post, error) { return Post{}, io.ErrUnexpectedEOF }
func (failingStore) Slugs(context.Context) ([]string, error)    { return nil, io.ErrUnexpectedEOF }
func (failingStore) Version(context.Context) (string, error)    { return "", io.ErrUnexpectedEOF }
