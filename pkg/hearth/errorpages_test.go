package hearth

import (
	"context"
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

// errBundle is the i18n bundle plus branded error templates and a layout that emits the robots tag.
func errBundle() fstest.MapFS {
	b := i18nBundle()
	b["layouts/base.html"] = &fstest.MapFile{Data: []byte(`<html lang="{{.Lang}}" dir="{{.Dir}}">` +
		`{{if .NoIndex}}<meta name="robots" content="noindex">{{end}}` +
		`{{block "body" .}}{{end}}</html>`)}
	b["pages/404.html"] = &fstest.MapFile{Data: []byte(`{{define "body"}}NF-EN status={{.Status}} <a href="{{.Link "/"}}">home</a>{{end}}`)}
	b["pages/404.he.html"] = &fstest.MapFile{Data: []byte(`{{define "body"}}NF-HE status={{.Status}}{{end}}`)}
	b["pages/410.html"] = &fstest.MapFile{Data: []byte(`{{define "body"}}GONE status={{.Status}}{{end}}`)}
	return b
}

func newErrSite(t *testing.T, b fstest.MapFS, mut func(*Options)) (*Site, string) {
	t.Helper()
	dir := t.TempDir()
	o := Options{Bundle: b, CacheDir: dir, BaseURL: "https://x.test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if mut != nil {
		mut(&o)
	}
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	s.MustHandle("post/{id}", func(r *http.Request, p map[string]string) (any, Meta, error) {
		switch p["id"] {
		case "1":
			return "First", Meta{}, nil
		case "gone":
			return nil, Meta{}, ErrGone
		}
		return nil, Meta{}, ErrNotFound
	})
	return s, dir
}

func TestErrorTemplatesForHandlerOutcomesAndUnmatchedPaths(t *testing.T) {
	s, dir := newErrSite(t, errBundle(), nil)
	cases := []struct {
		path   string
		status int
		want   string
	}{
		{"/post/nope", 404, "NF-EN status=404"},
		{"/post/gone", 410, "GONE status=410"},
		{"/no/such/page", 404, "NF-EN status=404"},
		{"/he/post/nope", 404, "NF-HE status=404"}, // language override
		{"/he/zzz", 404, "NF-HE status=404"},
		{"/he/post/gone", 410, "GONE status=410"}, // no 410.he.html: the base template, still in Hebrew layout
	}
	for _, c := range cases {
		w := get(s, c.path)
		if w.Code != c.status || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%s: %d %q, want %d %q", c.path, w.Code, w.Body, c.status, c.want)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s content type %q", c.path, ct)
		}
		if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s Cache-Control %q", c.path, cc)
		}
		if !strings.Contains(w.Body.String(), `<meta name="robots" content="noindex">`) || w.Header().Get("X-Robots-Tag") != "noindex" {
			t.Errorf("%s is not noindex: %v %s", c.path, w.Header(), w.Body)
		}
	}
	if !strings.Contains(get(s, "/he/zzz").Body.String(), `dir="rtl"`) {
		t.Error("he error page is not rtl")
	}
	if !strings.Contains(get(s, "/he/zzz2").Body.String(), "NF-HE") || strings.Contains(get(s, "/zzz").Body.String(), `href="/he`) {
		t.Error("language links leak")
	}
	if !strings.Contains(get(s, "/he/post/gone").Body.String(), `lang="he"`) {
		t.Error("410 not rendered in the request language")
	}
	if n := len(cachedFiles(t, dir)); n != 0 {
		t.Errorf("error bodies were cached: %v", cachedFiles(t, dir))
	}
	// HEAD has no body but the same status.
	req := httptest.NewRequest(http.MethodHead, "/nope", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 404 || w.Body.Len() != 0 {
		t.Errorf("HEAD: %d %q", w.Code, w.Body)
	}
}

func TestErrorTemplatesAreNotRoutes(t *testing.T) {
	b := errBundle()
	b["pages/docs/404.html"] = &fstest.MapFile{Data: []byte(`{{define "body"}}DOCS404{{end}}`)} // a normal page in a folder
	s, _ := newErrSite(t, b, nil)
	if w := get(s, "/docs/404"); w.Code != 200 || !strings.Contains(w.Body.String(), "DOCS404") {
		t.Errorf("/docs/404: %d %s", w.Code, w.Body)
	}
	// the top-level ones are neither pages nor sitemap entries nor prerender targets
	if w := get(s, "/404"); w.Code != 404 || !strings.Contains(w.Body.String(), "NF-EN status=404") {
		t.Errorf("/404: %d %s", w.Code, w.Body)
	}
	if w := get(s, "/410"); w.Code != 404 {
		t.Errorf("/410: %d", w.Code)
	}
	sm := get(s, "/sitemap.xml").Body.String()
	if strings.Contains(sm, "x.test/404") || strings.Contains(sm, "x.test/410") || strings.Contains(sm, "he/404") || !strings.Contains(sm, "/docs/404") {
		t.Errorf("sitemap: %s", sm)
	}
	out := t.TempDir()
	rep, err := Build(context.Background(), s, out)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range rep.Pages {
		if p == "/404" || p == "/410" || p == "/he/404" || p == "/he/410" {
			t.Errorf("error template prerendered as a page: %s", p)
		}
	}
	for _, f := range []string{"404/index.html", "410/index.html", "he/404/index.html"} {
		if _, err := os.Stat(filepath.Join(out, f)); err == nil {
			t.Errorf("%s was written", f)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "docs/404/index.html")); err != nil {
		t.Errorf("docs/404 page missing: %v", err)
	}
}

func TestPlainTextFallbackWithoutTemplates(t *testing.T) {
	s, _ := newSSRSite(t, nil) // no 404.html in this bundle
	s.MustHandle("/candidates", func(*http.Request, map[string]string) (any, Meta, error) { return nil, Meta{}, ErrGone })
	for path, code := range map[string]int{"/zzz": 404, "/candidates": 410} {
		w := get(s, path)
		if w.Code != code || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") || strings.Contains(w.Body.String(), "<html") {
			t.Errorf("%s: %d %v %q", path, w.Code, w.Header(), w.Body)
		}
	}
	out := t.TempDir()
	if _, err := Build(context.Background(), s, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "404.html")); err == nil {
		t.Error("404.html written without a template")
	}
}

func TestOnlyA410TemplateStillFallsBackFor404(t *testing.T) {
	b := i18nBundle()
	b["pages/410.html"] = &fstest.MapFile{Data: []byte(`{{define "body"}}GONE{{end}}`)}
	s, _ := newErrSite(t, b, nil)
	if w := get(s, "/nope"); w.Code != 404 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("404: %d %v", w.Code, w.Header())
	}
	if w := get(s, "/post/gone"); w.Code != 410 || !strings.Contains(w.Body.String(), "GONE") {
		t.Errorf("410: %d %s", w.Code, w.Body)
	}
}

func TestFallbackWinsForUnmatchedPaths(t *testing.T) {
	s, _ := newErrSite(t, errBundle(), func(o *Options) {
		o.Fallback = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("vite")) })
	})
	if w := get(s, "/nope.js"); w.Body.String() != "vite" {
		t.Errorf("unmatched path: %d %q", w.Code, w.Body)
	}
	// a matched page whose handler says not found is still the branded 404
	if w := get(s, "/post/nope"); w.Code != 404 || !strings.Contains(w.Body.String(), "NF-EN") {
		t.Errorf("handler 404: %d %q", w.Code, w.Body)
	}
}

func TestUnknownHostIsPlainText(t *testing.T) {
	s, _ := newErrSite(t, errBundle(), func(o *Options) {
		o.I18n = I18n{Mode: "subdomain", Hosts: map[string]string{"en": "x.test", "he": "he.x.test"}}
	})
	if w := getHost(s, "other.test", "/a"); w.Code != 404 || strings.Contains(w.Body.String(), "<html") {
		t.Errorf("%d %q", w.Code, w.Body)
	}
	if w := getHost(s, "he.x.test", "/a"); w.Code != 404 || !strings.Contains(w.Body.String(), "NF-HE") {
		t.Errorf("%d %q", w.Code, w.Body)
	}
}

func TestBuildWrites404FilePerLanguageLocation(t *testing.T) {
	s, _ := newErrSite(t, errBundle(), nil)
	out := t.TempDir()
	if _, err := Build(context.Background(), s, out); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{"404.html": "NF-EN", "he/404.html": "NF-HE"} {
		b, err := os.ReadFile(filepath.Join(out, file))
		if err != nil || !strings.Contains(string(b), want) || !strings.Contains(string(b), `content="noindex"`) {
			t.Errorf("%s: %v %s", file, err, b)
		}
	}
	// it is what the server answers
	b, _ := os.ReadFile(filepath.Join(out, "404.html"))
	if get(s, "/missing").Body.String() != string(b) {
		t.Error("404.html differs from the served 404 body")
	}

	h, _ := newErrSite(t, errBundle(), func(o *Options) {
		o.I18n = I18n{Mode: "subdomain", Hosts: map[string]string{"en": "x.test", "he": "he.x.test"}}
	})
	out = t.TempDir()
	if _, err := Build(context.Background(), h, out); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{"x.test/404.html": "NF-EN", "he.x.test/404.html": "NF-HE"} {
		if b, err := os.ReadFile(filepath.Join(out, file)); err != nil || !strings.Contains(string(b), want) {
			t.Errorf("%s: %v %s", file, err, b)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "404.html")); err == nil {
		t.Error("host mode wrote a root 404.html")
	}
}

func TestBrokenErrorTemplateFailsBuildAndFallsBackAtRuntime(t *testing.T) {
	b := errBundle()
	b["pages/404.html"] = &fstest.MapFile{Data: []byte(`{{define "body"}}{{.Nope.x}}{{end}}`)}
	s, _ := newErrSite(t, b, nil)
	if w := get(s, "/zzz"); w.Code != 404 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("runtime: %d %v", w.Code, w.Header())
	}
	if _, err := Build(context.Background(), s, t.TempDir()); err == nil {
		t.Error("build accepted a broken 404 template")
	}
}

func TestNoIndexFromHandlerMetaAndStaticDefine(t *testing.T) {
	b := errBundle()
	b["pages/private.html"] = &fstest.MapFile{Data: []byte(`{{define "robots"}}noindex{{end}}{{define "body"}}private{{end}}`)}
	b["pages/open.html"] = &fstest.MapFile{Data: []byte(`{{define "body"}}open{{end}}`)}
	b["pages/bad.html"] = &fstest.MapFile{Data: []byte(`{{define "robots"}}nofollow{{end}}{{define "body"}}bad{{end}}`)}
	s, dir := newErrSite(t, b, nil)
	const tag = `<meta name="robots" content="noindex">`

	w := get(s, "/private")
	if w.Code != 200 || strings.Count(w.Body.String(), tag) != 1 {
		t.Fatalf("static noindex: %d %s", w.Code, w.Body)
	}
	if strings.Contains(get(s, "/open").Body.String(), "robots") {
		t.Error("an open page has a robots tag")
	}
	if w := get(s, "/bad"); w.Code != 500 {
		t.Errorf("bad robots value: %d", w.Code)
	}
	// the cached file carries the tag, so a web server answering without Hearth still says noindex
	if rep := s.Prerender(context.Background(), []string{"/private"}, 1); len(rep.Failures) != 0 {
		t.Fatal(rep.Failures)
	}
	found := false
	for _, f := range cachedFiles(t, dir) {
		if strings.HasSuffix(f, filepath.Join("private", "index.html")) {
			found = true
			if c, _ := os.ReadFile(f); strings.Count(string(c), tag) != 1 {
				t.Errorf("cached file: %s", c)
			}
		}
	}
	if !found {
		t.Error("private page not cached")
	}
}

func TestNoIndexHandlerAndTemplateEmitOneTag(t *testing.T) {
	b := errBundle()
	b["pages/private.html"] = &fstest.MapFile{Data: []byte(`{{define "robots"}}noindex{{end}}{{define "body"}}private{{end}}`)}
	b["pages/list.html"] = &fstest.MapFile{Data: []byte(`{{define "body"}}list{{end}}`)}
	s, dir := newErrSite(t, b, func(o *Options) { o.SSR = []string{"/private", "/list"} })
	h := func(*http.Request, map[string]string) (any, Meta, error) { return nil, Meta{NoIndex: true}, nil }
	s.MustHandle("/private", h)
	s.MustHandle("/list", h)
	const tag = `<meta name="robots" content="noindex">`
	for _, p := range []string{"/private", "/list"} {
		w := get(s, p)
		if w.Code != 200 || w.Header().Get("X-Robots-Tag") != "noindex" || strings.Count(w.Body.String(), tag) != 1 {
			t.Errorf("%s: %d %v %s", p, w.Code, w.Header(), w.Body)
		}
	}
	for _, f := range cachedFiles(t, dir) {
		if c, _ := os.ReadFile(f); strings.Count(string(c), tag) != 1 {
			t.Errorf("cached %s lacks the single tag: %s", f, c)
		}
	}
	if len(cachedFiles(t, dir)) != 2 {
		t.Errorf("cached: %v", cachedFiles(t, dir))
	}
}
