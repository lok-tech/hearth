package hearth

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
)

func ssrBundle(manifest string) fstest.MapFS {
	b := i18nBundle()
	b["manifest.json"] = &fstest.MapFile{Data: []byte(manifest)}
	b["pages/candidates.html"] = &fstest.MapFile{Data: []byte(`{{define "body"}}list {{.Data}}{{end}}`)}
	return b
}

const ssrManifest = `{"format":3,"version":"b1","ssr":["/candidates"]}`

func newSSRSite(t *testing.T, mut func(*Options)) (*Site, string) {
	t.Helper()
	dir := t.TempDir()
	o := Options{Bundle: ssrBundle(ssrManifest), CacheDir: dir, BaseURL: "https://x.test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if mut != nil {
		mut(&o)
	}
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestSSRHandlerRendersAndCaches(t *testing.T) {
	s, dir := newSSRSite(t, nil)
	var calls atomic.Int64
	s.MustHandle("/candidates", func(*http.Request, map[string]string) (any, Meta, error) {
		calls.Add(1)
		return "Ann", Meta{NoIndex: true}, nil
	})
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	w := get(s, "/candidates")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "list Ann") || w.Header().Get("X-Robots-Tag") != "noindex" {
		t.Fatalf("first: %d %v %s", w.Code, w.Header(), w.Body)
	}
	if n := len(cachedFiles(t, dir)); n != 1 {
		t.Fatalf("cached files = %d", n)
	}
	hits := s.Counters.Hits.Load()
	if w2 := get(s, "/candidates"); w2.Body.String() != w.Body.String() {
		t.Error("second answer differs")
	}
	if s.Counters.Hits.Load() != hits+1 || calls.Load() != 1 {
		t.Errorf("hits %d -> %d, handler calls %d", hits, s.Counters.Hits.Load(), calls.Load())
	}
}

func TestSSRHandlerOnStaticPageNotInSSRFails(t *testing.T) {
	s, _ := newSSRSite(t, nil)
	err := s.Handle("/about", func(*http.Request, map[string]string) (any, Meta, error) { return nil, Meta{}, nil })
	if err == nil || !strings.Contains(err.Error(), "shadowed") {
		t.Fatalf("Handle(/about) = %v", err)
	}
	if err := s.Handle("/", nil); err == nil {
		t.Error("index is a static page too")
	}
	if err := s.Handle("/nope", nil); err == nil {
		t.Error("unknown pattern accepted")
	}
	// the static page still renders without data
	if w := get(s, "/about"); w.Code != 200 || !strings.Contains(w.Body.String(), "about Hello") {
		t.Errorf("about: %d %s", w.Code, w.Body)
	}
}

func TestSSROutcomesFromAParameterlessHandler(t *testing.T) {
	s, dir := newSSRSite(t, nil)
	mode := "gone"
	s.MustHandle("candidates", func(*http.Request, map[string]string) (any, Meta, error) {
		switch mode {
		case "gone":
			return nil, Meta{}, ErrGone
		case "missing":
			return nil, Meta{}, ErrNotFound
		case "moved":
			return nil, Meta{}, Redirect(301, "/about")
		}
		return nil, Meta{}, io.ErrUnexpectedEOF
	})
	for _, c := range []struct {
		mode string
		code int
	}{{"gone", 410}, {"missing", 404}, {"moved", 301}, {"boom", 500}} {
		mode = c.mode
		w := get(s, "/candidates")
		if w.Code != c.code {
			t.Errorf("%s: %d, want %d", c.mode, w.Code, c.code)
		}
		if c.mode == "moved" && w.Header().Get("Location") != "/about" {
			t.Errorf("Location = %q", w.Header().Get("Location"))
		}
	}
	if n := len(cachedFiles(t, dir)); n != 0 {
		t.Errorf("non-200 cached: %d files", n)
	}
}

func TestSSRPageWithoutHandlerIs404AndValidateSaysSo(t *testing.T) {
	s, _ := newSSRSite(t, nil)
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "/candidates") {
		t.Errorf("Validate = %v", err)
	}
	if w := get(s, "/candidates"); w.Code != 404 {
		t.Errorf("no handler: %d", w.Code)
	}
	s.MustHandle("candidates", func(*http.Request, map[string]string) (any, Meta, error) { return "x", Meta{}, nil })
	if err := s.Validate(); err != nil {
		t.Errorf("Validate after Handle = %v", err)
	}
}

func TestSSRListIsValidatedAtNew(t *testing.T) {
	for name, man := range map[string]string{
		"unknown":  `{"format":3,"ssr":["/nope"]}`,
		"param":    `{"format":3,"ssr":["/post/{id}"]}`,
		"relative": `{"format":3,"ssr":["candidates"]}`,
	} {
		_, err := New(Options{Bundle: ssrBundle(man), CacheDir: t.TempDir()})
		if err == nil {
			t.Errorf("%s: New accepted it", name)
		}
	}
	if _, err := New(Options{Bundle: ssrBundle(`{"format":2}`), CacheDir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "format") {
		t.Errorf("old bundle format: %v", err)
	}
	// Options.SSR adds to the manifest (tools run a Site over a source tree with no manifest)
	s, err := New(Options{Bundle: ssrBundle(`{"format":3}`), CacheDir: t.TempDir(), SSR: []string{"/candidates"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Handle("candidates", func(*http.Request, map[string]string) (any, Meta, error) { return nil, Meta{}, nil }); err != nil {
		t.Error(err)
	}
}

func TestSitemapListsSSRPages(t *testing.T) {
	s, _ := newSSRSite(t, nil)
	w := get(s, "/sitemap.xml")
	for _, want := range []string{"https://x.test/candidates", "https://x.test/he/candidates"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("sitemap lacks %s: %s", want, w.Body)
		}
	}
}

func TestInvalidateParameterlessPageDropsEveryLanguage(t *testing.T) {
	s, _ := newSSRSite(t, nil)
	var n atomic.Int64
	s.MustHandle("/candidates", func(*http.Request, map[string]string) (any, Meta, error) {
		return fmt.Sprint("v", n.Add(1)), Meta{}, nil
	})
	get(s, "/candidates")
	get(s, "/he/candidates")
	get(s, "/about")
	if n.Load() != 2 {
		t.Fatalf("handler calls = %d", n.Load())
	}
	if err := s.Invalidate("/candidates"); err != nil {
		t.Fatal(err)
	}
	en, he := get(s, "/candidates"), get(s, "/he/candidates")
	if !strings.Contains(en.Body.String(), "list v3") && !strings.Contains(en.Body.String(), "list v4") || n.Load() != 4 {
		t.Errorf("not refreshed in both languages: calls=%d en=%s he=%s", n.Load(), en.Body, he.Body)
	}
	// other pages stay cached
	renders := s.Counters.Renders.Load()
	get(s, "/about")
	if s.Counters.Renders.Load() != renders {
		t.Error("Invalidate(/candidates) dropped /about")
	}
}

func TestFuncsAreCallableAndNotDoubleEscaped(t *testing.T) {
	b := ssrBundle(ssrManifest)
	b["pages/about.html"] = &fstest.MapFile{Data: []byte(`{{define "body"}}[{{num 1234567}}][{{trend 3 4}}][{{shout "a<b"}}]{{end}}`)}
	s, err := New(Options{Bundle: b, CacheDir: t.TempDir(), Funcs: template.FuncMap{
		"num": func(n int) string { return "1,234,567" },
		"trend": func(a, b float64) template.HTML {
			return template.HTML(fmt.Sprintf(`<svg data-a="%g" data-b="%g"></svg>`, a, b))
		},
		"shout": func(s string) (string, error) { return strings.ToUpper(s), nil },
	}})
	if err != nil {
		t.Fatal(err)
	}
	w := get(s, "/about")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `[1,234,567][<svg data-a="3" data-b="4"></svg>][A&lt;B]`) {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

func TestFuncsRulesAreEnforcedByNew(t *testing.T) {
	ok := func() string { return "" }
	for name, fm := range map[string]template.FuncMap{
		"shadows helper":  {"t": ok},
		"shadows asset":   {"asset": ok},
		"shadows builtin": {"len": ok},
		"shadows eq":      {"eq": ok},
		"not a func":      {"x": 42},
		"nil func":        {"x": (func() string)(nil)},
		"no result":       {"x": func() {}},
		"two results":     {"x": func() (string, string) { return "", "" }},
		"three results":   {"x": func() (string, int, error) { return "", 0, nil }},
		"bad name":        {"a-b": ok},
		"empty name":      {"": ok},
	} {
		if _, err := New(Options{Bundle: ssrBundle(ssrManifest), CacheDir: t.TempDir(), Funcs: fm}); err == nil || !strings.Contains(err.Error(), "Funcs") {
			t.Errorf("%s: New = %v", name, err)
		}
	}
	good := template.FuncMap{"a": ok, "b": func() (int, error) { return 1, nil }}
	if _, err := New(Options{Bundle: ssrBundle(ssrManifest), CacheDir: t.TempDir(), Funcs: good}); err != nil {
		t.Errorf("valid funcs rejected: %v", err)
	}
}

func TestFuncErrorFailsTheRenderAndCachesNothing(t *testing.T) {
	b := ssrBundle(ssrManifest)
	b["pages/about.html"] = &fstest.MapFile{Data: []byte(`{{define "body"}}before {{num 1}} after{{end}}`)}
	dir := t.TempDir()
	s, err := New(Options{Bundle: b, CacheDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Funcs: template.FuncMap{"num": func(int) (string, error) { return "", fmt.Errorf("boom") }}})
	if err != nil {
		t.Fatal(err)
	}
	w := get(s, "/about")
	if w.Code != 500 || strings.Contains(w.Body.String(), "before") {
		t.Errorf("%d %q", w.Code, w.Body)
	}
	if n := len(cachedFiles(t, dir)); n != 0 {
		t.Errorf("cached %d files", n)
	}
}

func TestBuildWritesStaticPagesNotSSRPages(t *testing.T) {
	s, _ := newSSRSite(t, func(o *Options) {
		o.Funcs = template.FuncMap{"num": func(n int) string { return fmt.Sprint(n) }}
		o.SitemapEntries = func(context.Context) ([]string, error) { return []string{"/post/1"}, nil }
	})
	s.MustHandle("/candidates", func(*http.Request, map[string]string) (any, Meta, error) { return "x", Meta{}, nil })
	s.MustHandle("post/{id}", func(r *http.Request, p map[string]string) (any, Meta, error) { return "First", Meta{}, nil })
	out := t.TempDir()
	rep, err := Build(context.Background(), s, out)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"index.html", "he/index.html", "about/index.html", "he/about/index.html", "post/1/index.html", "he/post/1/index.html", "sitemap.xml", "robots.txt"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "candidates", "index.html")); err == nil {
		t.Error("ssr page was exported")
	}
	if len(rep.Pages) != 6 {
		t.Errorf("pages = %v", rep.Pages)
	}
	sm, _ := os.ReadFile(filepath.Join(out, "sitemap.xml"))
	if !strings.Contains(string(sm), "https://x.test/candidates") {
		t.Errorf("sitemap: %s", sm)
	}
}

func TestBuildFailsWhenAPageDoesNotRender(t *testing.T) {
	s, _ := newSSRSite(t, func(o *Options) {
		o.SitemapEntries = func(context.Context) ([]string, error) { return []string{"/post/zzz", "/post/yyy"}, nil }
	})
	s.MustHandle("post/{id}", func(r *http.Request, p map[string]string) (any, Meta, error) { return nil, Meta{}, ErrNotFound })
	out := t.TempDir()
	if _, err := Build(context.Background(), s, out); err == nil || !strings.Contains(err.Error(), "more pages failed") {
		t.Errorf("Build = %v", err)
	}
	if ents, _ := os.ReadDir(out); len(ents) != 0 {
		t.Error("a failed build exported files")
	}
}

// Run with -race: parallel Prerender calls the same custom function from many goroutines.
func TestPrerenderCallsFuncsConcurrently(t *testing.T) {
	b := ssrBundle(ssrManifest)
	b["pages/post/[id].html"] = &fstest.MapFile{Data: []byte(`{{define "body"}}{{num .Data}}{{end}}`)}
	var calls atomic.Int64
	s, err := New(Options{Bundle: b, CacheDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Funcs: template.FuncMap{"num": func(n int) string { calls.Add(1); return fmt.Sprint(n * 2) }}})
	if err != nil {
		t.Fatal(err)
	}
	s.MustHandle("post/{id}", func(r *http.Request, p map[string]string) (any, Meta, error) {
		var n int
		fmt.Sscan(p["id"], &n)
		return n, Meta{}, nil
	})
	var paths []string
	for i := 0; i < 40; i++ {
		paths = append(paths, fmt.Sprint("/post/", i))
	}
	rep := s.Prerender(context.Background(), paths, 8)
	if len(rep.Failures) > 0 || rep.OK != 80 || calls.Load() != 80 {
		t.Errorf("ok=%d calls=%d failures=%v", rep.OK, calls.Load(), rep.Failures)
	}
}
