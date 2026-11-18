package hearth

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func i18nBundle() fstest.MapFS {
	return fstest.MapFS{
		"manifest.json": {Data: []byte(`{"format":1,"version":"b1"}`)},
		"layouts/base.html": {Data: []byte(`<html lang="{{.Lang}}" dir="{{.Dir}}"><link rel="canonical" href="{{.Canonical}}">` +
			`{{range $l,$u := .Alternates}}<link rel="alternate" hreflang="{{$l}}" href="{{$u}}">{{end}}` +
			`{{block "body" .}}{{end}}</html>`)},
		"pages/index.html":     {Data: []byte(`{{define "body"}}{{t "hi"}} <a href="{{.Link "/about"}}">a</a>{{end}}`)},
		"pages/about.html":     {Data: []byte(`{{define "body"}}about {{t "hi"}}{{end}}`)},
		"pages/post/[id].html": {Data: []byte(`{{define "body"}}post {{index .Params "id"}}: {{.Data}}{{end}}`)},
		"locales/en.json":      {Data: []byte(`{"hi":"Hello"}`)},
		"locales/he.json":      {Data: []byte(`{"hi":"שלום"}`)},
	}
}

func newI18nSite(t *testing.T, mut func(*Options)) (*Site, string) {
	t.Helper()
	dir := t.TempDir()
	o := Options{Bundle: i18nBundle(), CacheDir: dir, BaseURL: "https://x.test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if mut != nil {
		mut(&o)
	}
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Handle("post/{id}", func(r *http.Request, p map[string]string) (any, Meta, error) {
		if p["id"] == "1" {
			return "First", Meta{}, nil
		}
		return nil, Meta{}, ErrNotFound
	}); err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func getHost(h http.Handler, host, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	req.Host = host
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestPrefixModeHasHreflangCanonicalAndPrefixedLinks(t *testing.T) {
	s, _ := newI18nSite(t, nil)
	en := get(s, "/about").Body.String()
	for _, want := range []string{`lang="en"`, `dir="ltr"`, `rel="canonical" href="https://x.test/about"`,
		`hreflang="en" href="https://x.test/about"`, `hreflang="he" href="https://x.test/he/about"`, "Hello"} {
		if !strings.Contains(en, want) {
			t.Errorf("en /about missing %q in %s", want, en)
		}
	}
	he := get(s, "/he/about").Body.String()
	for _, want := range []string{`lang="he"`, `dir="rtl"`, `rel="canonical" href="https://x.test/he/about"`, "שלום"} {
		if !strings.Contains(he, want) {
			t.Errorf("he /about missing %q in %s", want, he)
		}
	}
	if home := get(s, "/he").Body.String(); !strings.Contains(home, `href="/he/about"`) {
		t.Errorf("link not prefixed: %s", home)
	}
	// SSR pages get the same treatment as static ones (they used to lack hreflang)
	post := get(s, "/he/post/1").Body.String()
	if !strings.Contains(post, `hreflang="en" href="https://x.test/post/1"`) || !strings.Contains(post, `rel="canonical" href="https://x.test/he/post/1"`) {
		t.Errorf("SSR page: %s", post)
	}
}

func TestHostModeResolvesLanguageFromHost(t *testing.T) {
	s, dir := newI18nSite(t, func(o *Options) {
		o.I18n = I18n{Mode: "subdomain", Hosts: map[string]string{"en": "x.test", "he": "he.x.test"}}
	})
	he := getHost(s, "he.x.test", "/about")
	if he.Code != 200 || !strings.Contains(he.Body.String(), "שלום") || !strings.Contains(he.Body.String(), `rel="canonical" href="https://he.x.test/about"`) {
		t.Fatalf("he host: %d %s", he.Code, he.Body)
	}
	if !strings.Contains(he.Body.String(), `hreflang="en" href="https://x.test/about"`) {
		t.Errorf("alternates: %s", he.Body)
	}
	if en := getHost(s, "x.test:8443", "/about"); !strings.Contains(en.Body.String(), "Hello") {
		t.Errorf("en host with port: %s", en.Body)
	}
	if w := getHost(s, "evil.test", "/about"); w.Code != 404 {
		t.Errorf("unknown host = %d", w.Code)
	}
	// each language is cached under its own configured host directory, never the request's
	if _, err := os.Stat(filepath.Join(dir, "current", "he.x.test", "about", "index.html")); err != nil {
		t.Errorf("host dir missing: %v", err)
	}
	if again := getHost(s, "he.x.test", "/about"); again.Body.String() != he.Body.String() {
		t.Error("second request should be served from the host's file")
	}
}

func TestBadI18nOptionsAreRejected(t *testing.T) {
	mk := func(i I18n) error {
		_, err := New(Options{Bundle: i18nBundle(), CacheDir: t.TempDir(), I18n: i})
		return err
	}
	if mk(I18n{Mode: "weird"}) == nil {
		t.Error("unknown mode")
	}
	if mk(I18n{Mode: "domain"}) == nil {
		t.Error("host mode without hosts")
	}
	if mk(I18n{Mode: "domain", Hosts: map[string]string{"en": "a.test", "he": "../x"}}) == nil {
		t.Error("bad host")
	}
	if _, err := New(Options{Bundle: i18nBundle(), CacheDir: t.TempDir(), DefaultLang: "fr"}); err == nil {
		t.Error("default language without a catalog")
	}
}

func TestSitemapAndRobotsFollowLanguagesAndHosts(t *testing.T) {
	s, _ := newI18nSite(t, func(o *Options) {
		o.SitemapEntries = func(context.Context) ([]string, error) { return []string{"/post/1"}, nil }
	})
	sm := get(s, "/sitemap.xml").Body.String()
	for _, want := range []string{"<loc>https://x.test/</loc>", "<loc>https://x.test/he</loc>", "<loc>https://x.test/he/about</loc>", "<loc>https://x.test/he/post/1</loc>"} {
		if !strings.Contains(sm, want) {
			t.Errorf("sitemap missing %s:\n%s", want, sm)
		}
	}
	h, _ := newI18nSite(t, func(o *Options) {
		o.I18n = I18n{Mode: "subdomain", Hosts: map[string]string{"en": "x.test", "he": "he.x.test"}}
	})
	hesm := getHost(h, "he.x.test", "/sitemap.xml").Body.String()
	if !strings.Contains(hesm, "<loc>https://he.x.test/about</loc>") || strings.Contains(hesm, "https://x.test/") {
		t.Errorf("host sitemap must list only its own host:\n%s", hesm)
	}
	if rb := getHost(h, "he.x.test", "/robots.txt").Body.String(); !strings.Contains(rb, "Sitemap: https://he.x.test/sitemap.xml") {
		t.Errorf("robots: %s", rb)
	}
}

func TestPrerenderAndExport(t *testing.T) {
	s, _ := newI18nSite(t, nil)
	rep := s.Prerender(context.Background(), []string{"/", "/about", "/post/1", "/post/nope"}, 3)
	if rep.OK != 6 || len(rep.Pages) != 6 {
		t.Fatalf("ok = %d pages = %v", rep.OK, rep.Pages)
	}
	if len(rep.Failures) != 2 || rep.Failures[0].Path != "/post/nope" || rep.Failures[0].Lang != "en" || rep.Failures[0].Status != 404 {
		t.Fatalf("failures = %+v", rep.Failures)
	}
	out := t.TempDir()
	if err := s.Export(out); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"index.html", "about/index.html", "he/index.html", "he/post/1/index.html"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(f))); err != nil {
			t.Errorf("exported %s missing", f)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "post", "nope")); err == nil {
		t.Error("a failed page was exported")
	}
	// the exported file is byte-for-byte what the server answers
	want, _ := os.ReadFile(filepath.Join(out, "he", "about", "index.html"))
	if got := get(s, "/he/about").Body.Bytes(); string(got) != string(want) {
		t.Error("export differs from the server's response")
	}
}

func TestPrerenderReportsRenderErrorsAndBadPaths(t *testing.T) {
	b := i18nBundle()
	b["pages/broken.html"] = &fstest.MapFile{Data: []byte(`{{define "body"}}{{t "nope"}}{{end}}`)}
	s, err := New(Options{Bundle: b, CacheDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	rep := s.Prerender(context.Background(), []string{"/broken", "nope"}, 1)
	if len(rep.Failures) != 4 {
		t.Fatalf("failures = %+v", rep.Failures)
	}
	var sawMissing bool
	for _, f := range rep.Failures {
		if f.Path == "/broken" && strings.Contains(f.Err.Error(), `missing translation "nope"`) {
			sawMissing = true
		}
	}
	if !sawMissing {
		t.Errorf("the template error must reach the caller: %+v", rep.Failures)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := s.Prerender(ctx, []string{"/about"}, 1); r.OK != 0 || len(r.Failures) != 2 {
		t.Errorf("cancelled run: %+v", r)
	}
}

func TestFallbackGetsUnmatchedRequestsOnly(t *testing.T) {
	var got []string
	s, _ := newI18nSite(t, func(o *Options) {
		o.Fallback = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = append(got, r.URL.Path)
			w.Write([]byte("fallback"))
		})
	})
	if w := get(s, "/src/entry.ts"); w.Body.String() != "fallback" {
		t.Errorf("unmatched: %q", w.Body)
	}
	if w := get(s, "/post/nope"); w.Code != 404 || w.Body.String() == "fallback" {
		t.Errorf("a matched page that is not found must not reach the fallback: %d %q", w.Code, w.Body)
	}
	if w := get(s, "/about"); strings.Contains(w.Body.String(), "fallback") {
		t.Error("a rendered page reached the fallback")
	}
	if len(got) != 1 || got[0] != "/src/entry.ts" {
		t.Errorf("fallback saw %v", got)
	}
}

func TestDevModeNeedsNoManifestAndNeverCaches(t *testing.T) {
	b := i18nBundle()
	delete(b, "manifest.json")
	b["layouts/base.html"] = &fstest.MapFile{Data: []byte(`{{if .Dev}}DEV {{asset "src/entry.ts"}}{{end}}{{block "body" .}}{{end}}`)}
	dir := t.TempDir()
	s, err := New(Options{Bundle: b, CacheDir: dir, Dev: true, DevAssetURL: "http://localhost:5173/",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	w := get(s, "/about")
	if !strings.Contains(w.Body.String(), "DEV http://localhost:5173/src/entry.ts") {
		t.Errorf("asset not pointed at Vite: %s", w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
	if files := cachedFiles(t, dir); len(files) != 0 {
		t.Errorf("dev wrote cache files: %v", files)
	}
	// without Dev a missing manifest is an error
	if _, err := New(Options{Bundle: b, CacheDir: t.TempDir()}); err == nil {
		t.Error("missing manifest must fail outside Dev")
	}
}

func TestDevModeExplainsAMissingPage(t *testing.T) {
	s, _ := newI18nSite(t, func(o *Options) { o.Dev = true })
	if err := s.Handle("post/{id}", func(r *http.Request, p map[string]string) (any, Meta, error) {
		return nil, Meta{}, errorsJoin(ErrNotFound, "no mock data for "+r.URL.Path)
	}); err != nil {
		t.Fatal(err)
	}
	if w := get(s, "/post/9"); w.Code != 404 || !strings.Contains(w.Body.String(), "no mock data for /post/9") {
		t.Errorf("%d %q", w.Code, w.Body)
	}
}

// errorsJoin wraps sentinel with a message, the way a handler explains a 404 to a developer.
func errorsJoin(sentinel error, msg string) error { return fmt.Errorf("%w: %s", sentinel, msg) }
