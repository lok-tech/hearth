package hearth

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOverridesFromEnv(t *testing.T) {
	ok := []struct {
		env  []string
		want Overrides
	}{
		{nil, Overrides{}},
		{[]string{"HEARTH_TEMPLATES=request"}, Overrides{Templates: "request"}},
		{[]string{"HEARTH_CACHE=off", "HEARTH_DIAGNOSTICS=1"}, Overrides{Cache: "off", Diagnostics: "1"}},
		{[]string{"HEARTH_ASSETS=vite:http://localhost:5173"}, Overrides{Assets: "vite:http://localhost:5173"}},
		{[]string{"HEARTH_ASSETS=placeholder"}, Overrides{Assets: "placeholder"}},
		{[]string{"HEARTH_ENV=dev"}, Overrides{Templates: "request", Cache: "off", Diagnostics: "1"}},
		// An individual variable wins over the preset.
		{[]string{"HEARTH_ENV=dev", "HEARTH_CACHE=disk"}, Overrides{Templates: "request", Cache: "disk", Diagnostics: "1"}},
		// Other HEARTH_* names belong to the deployment, not to the layers.
		{[]string{"HEARTH_CACHE_DIR=/x", "HEARTH_TMP_DIR=/y", "PATH=/bin"}, Overrides{}},
		{[]string{"HEARTH_CACHE="}, Overrides{}},
	}
	for _, c := range ok {
		got, err := overridesFrom(c.env)
		if err != nil || got != c.want {
			t.Errorf("%v = %+v, %v; want %+v", c.env, got, err, c.want)
		}
	}
	bad := []string{"HEARTH_TEMPLATES=always", "HEARTH_CACHE=memory", "HEARTH_ASSETS=cdn", "HEARTH_ASSETS=vite:",
		"HEARTH_ASSETS=vite:localhost:5173", "HEARTH_DIAGNOSTICS=yes", "HEARTH_ENV=prod"}
	for _, kv := range bad {
		_, err := overridesFrom([]string{kv})
		name, _, _ := strings.Cut(kv, "=")
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v, want one naming %s", kv, err, name)
		}
	}
}

func TestNewFailsOnABadEnvValue(t *testing.T) {
	t.Setenv("HEARTH_CACHE", "memory")
	if _, err := New(Options{Bundle: siteBundle(), CacheDir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "HEARTH_CACHE") {
		t.Fatalf("New = %v, want an error naming HEARTH_CACHE", err)
	}
	t.Setenv("HEARTH_CACHE", "")
	if _, err := New(Options{Bundle: siteBundle(), CacheDir: t.TempDir(), Overrides: Overrides{Templates: "x"}}); err == nil {
		t.Fatal("a bad Options.Overrides value must fail New too")
	}
}

func TestCacheOffKeepsNothingAndSendsNoStore(t *testing.T) {
	t.Setenv("HEARTH_CACHE", "off")
	s, dir := newTestSite(t, &fakeDB{})
	for i := 0; i < 2; i++ {
		w := get(s, "/")
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("GET / = %d %q", w.Code, w.Header().Get("Cache-Control"))
		}
	}
	if files := cachedFiles(t, dir); len(files) != 0 {
		t.Fatalf("cache off wrote %v", files)
	}
	if got := s.Counters.Renders.Load(); got != 2 {
		t.Fatalf("renders = %d, want 2 (nothing is reused)", got)
	}
}

func TestEnvWinsOverCode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HEARTH_CACHE", "off")
	s, err := New(Options{Bundle: siteBundle(), CacheDir: dir, Overrides: Overrides{Cache: "disk"},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	get(s, "/")
	if files := cachedFiles(t, dir); len(files) != 0 {
		t.Fatalf("the environment (off) lost to code (disk): %v", files)
	}
	// Without the variable, code applies.
	t.Setenv("HEARTH_CACHE", "")
	s, _ = New(Options{Bundle: siteBundle(), CacheDir: dir, Overrides: Overrides{Cache: "off"},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	get(s, "/")
	if files := cachedFiles(t, dir); len(files) != 0 {
		t.Fatalf("Options.Overrides cache=off was ignored: %v", files)
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTemplatesRequestShowsEditsOnTheNextRequest(t *testing.T) {
	for _, mode := range []string{"once", "request"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HEARTH_TEMPLATES", mode)
			t.Setenv("HEARTH_CACHE", "off") // isolate the template layer from the store
			src := t.TempDir()
			writeTree(t, src, map[string]string{
				"manifest.json":     `{"format":3}`,
				"layouts/base.html": `{{block "body" .}}{{end}}`,
				"pages/index.html":  `{{define "body"}}v1{{end}}`,
			})
			s, err := New(Options{Bundle: os.DirFS(src), CacheDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if err != nil {
				t.Fatal(err)
			}
			if body := get(s, "/").Body.String(); body != "v1" {
				t.Fatalf("first = %q", body)
			}
			writeTree(t, src, map[string]string{"pages/index.html": `{{define "body"}}v2{{end}}`})
			want := map[string]string{"once": "v1", "request": "v2"}[mode]
			if body := get(s, "/").Body.String(); body != want {
				t.Fatalf("after the edit = %q, want %q", body, want)
			}
		})
	}
}

func TestTemplatesRequestRecoversFromAParseError(t *testing.T) {
	t.Setenv("HEARTH_TEMPLATES", "request")
	t.Setenv("HEARTH_CACHE", "off")
	t.Setenv("HEARTH_DIAGNOSTICS", "1")
	src := t.TempDir()
	writeTree(t, src, map[string]string{
		"manifest.json":     `{"format":3}`,
		"layouts/base.html": `{{block "body" .}}{{end}}`,
		"pages/index.html":  `{{define "body"}}ok{{end}}`,
	})
	s, err := New(Options{Bundle: os.DirFS(src), CacheDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	writeTree(t, src, map[string]string{"pages/index.html": `{{define "body"}}{{oops{{end}}`})
	w := get(s, "/")
	if w.Code != 500 || !strings.Contains(w.Body.String(), "pages/index.html") {
		t.Fatalf("broken template = %d %q, want 500 naming the file (diagnostics on)", w.Code, w.Body.String())
	}
	writeTree(t, src, map[string]string{"pages/index.html": `{{define "body"}}fixed{{end}}`})
	if w := get(s, "/"); w.Code != 200 || w.Body.String() != "fixed" {
		t.Fatalf("after the fix = %d %q", w.Code, w.Body.String())
	}
}

func TestParseErrorIsOpaqueWithoutDiagnostics(t *testing.T) {
	t.Setenv("HEARTH_TEMPLATES", "request")
	t.Setenv("HEARTH_CACHE", "off")
	src := t.TempDir()
	writeTree(t, src, map[string]string{
		"manifest.json":     `{"format":3}`,
		"layouts/base.html": `{{block "body" .}}{{end}}`,
		"pages/index.html":  `{{define "body"}}{{oops{{end}}`,
	})
	s, err := New(Options{Bundle: os.DirFS(src), CacheDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if w := get(s, "/"); w.Code != 500 || strings.Contains(w.Body.String(), "index.html") {
		t.Fatalf("= %d %q, want a bare 500", w.Code, w.Body.String())
	}
}

func TestViteAssetsOverride(t *testing.T) {
	t.Setenv("HEARTH_ASSETS", "vite:http://localhost:5173/")
	t.Setenv("HEARTH_CACHE", "off")
	src := t.TempDir() // no manifest.json: the Vite server serves the assets
	writeTree(t, src, map[string]string{
		"layouts/base.html": `{{block "body" .}}{{end}}`,
		"pages/index.html":  `{{define "body"}}{{entry "src/site.ts"}}|{{asset "a.css"}}|{{if .HMR}}hmr{{end}}{{end}}`,
	})
	s, err := New(Options{Bundle: os.DirFS(src), CacheDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	body := get(s, "/").Body.String()
	for _, want := range []string{"http://localhost:5173/@vite/client", "http://localhost:5173/src/site.ts", "|http://localhost:5173/a.css|", "hmr"} {
		if !strings.Contains(body, want) {
			t.Errorf("body %q lacks %q", body, want)
		}
	}
}

func TestAssetsManifestIsStrictEvenWhenCodeAsksForPlaceholders(t *testing.T) {
	t.Setenv("HEARTH_ASSETS", "manifest")
	src := t.TempDir()
	writeTree(t, src, map[string]string{"layouts/base.html": `x`, "pages/index.html": `x`})
	_, err := New(Options{Bundle: os.DirFS(src), CacheDir: t.TempDir(), Overrides: Overrides{Assets: "placeholder"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err == nil {
		t.Fatal("HEARTH_ASSETS=manifest must require manifest.json")
	}
}

func TestDiagnosticsShowTheReasonFor404(t *testing.T) {
	for _, on := range []string{"", "1"} {
		t.Setenv("HEARTH_DIAGNOSTICS", on)
		s, _ := newTestSite(t, &fakeDB{})
		w := get(s, "/post/nope")
		reason := strings.Contains(w.Body.String(), ErrNotFound.Error()) && w.Body.String() != "Not Found\n"
		if w.Code != 404 || reason != (on == "1") {
			t.Errorf("HEARTH_DIAGNOSTICS=%q: %d %q", on, w.Code, w.Body.String())
		}
	}
}

func TestOverridesWarnOnceAndOnlyWhenActive(t *testing.T) {
	var buf bytes.Buffer
	lg := slog.New(slog.NewTextHandler(&buf, nil))
	if _, err := New(Options{Bundle: siteBundle(), CacheDir: t.TempDir(), Logger: lg}); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("production defaults logged %q", buf.String())
	}
	t.Setenv("HEARTH_CACHE", "off")
	t.Setenv("HEARTH_TEMPLATES", "request")
	if _, err := New(Options{Bundle: siteBundle(), CacheDir: t.TempDir(), Logger: lg}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Count(out, "overrides active") != 1 || !strings.Contains(out, "cache=off") || !strings.Contains(out, "templates=request") {
		t.Fatalf("warning = %q", out)
	}
}

func TestIgnoreEnvAndCodeOverridesStaySilent(t *testing.T) {
	t.Setenv("HEARTH_CACHE", "off")
	var buf bytes.Buffer
	dir := t.TempDir()
	s, err := New(Options{Bundle: siteBundle(), CacheDir: dir, IgnoreEnv: true, Overrides: Overrides{Diagnostics: "1"}, Logger: slog.New(slog.NewTextHandler(&buf, nil))})
	if err != nil {
		t.Fatal(err)
	}
	get(s, "/")
	if len(cachedFiles(t, dir)) == 0 {
		t.Fatal("IgnoreEnv still applied HEARTH_CACHE=off")
	}
	if buf.Len() != 0 {
		t.Fatalf("a tool's own overrides logged %q", buf.String())
	}
	t.Setenv("HEARTH_CACHE", "bogus")
	if _, err := New(Options{Bundle: siteBundle(), CacheDir: dir, IgnoreEnv: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}); err != nil {
		t.Fatalf("IgnoreEnv must not even parse the environment: %v", err)
	}
}

func TestExportRefusesWhenTheCacheIsOff(t *testing.T) {
	t.Setenv("HEARTH_CACHE", "off")
	s, _ := newTestSite(t, &fakeDB{})
	if err := s.Export(t.TempDir()); err == nil || !strings.Contains(err.Error(), "HEARTH_CACHE=off") {
		t.Fatalf("Export = %v, want an error naming HEARTH_CACHE=off", err)
	}
}

func TestMatches(t *testing.T) {
	s, _ := newTestSite(t, &fakeDB{})
	for _, c := range []struct {
		method, path string
		want         bool
	}{
		{"GET", "/", true}, {"GET", "/post/1", true}, {"GET", "/post/anything", true}, // a handler decides the 404
		{"GET", "/healthz", true}, {"GET", "/robots.txt", true}, {"GET", "/sitemap.xml", true}, {"GET", "/sitemap-2.xml", true},
		{"GET", "/post/1/", true},   // trailing slash: Site redirects
		{"POST", "/anything", true}, // Site answers 405
		{"GET", "/src/entry.ts", false}, {"GET", "/search.json", false}, {"GET", "/nope", false}, {"HEAD", "/nope", false},
		{"GET", "/%00", false},
	} {
		r := httptest.NewRequest(c.method, "http://x.test"+c.path, nil)
		if got := s.Matches(r); got != c.want {
			t.Errorf("%s %s: Matches = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestBundleOverrideReplacesTheCodesBundle(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{"layouts/base.html": `{{block "body" .}}{{end}}`, "pages/index.html": `{{define "body"}}from the directory{{end}}`})
	t.Setenv("HEARTH_BUNDLE", src)
	t.Setenv("HEARTH_ASSETS", "placeholder") // a source tree has no manifest
	s, err := New(Options{Bundle: siteBundle(), CacheDir: t.TempDir(), Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	if body := get(s, "/").Body.String(); body != "from the directory" {
		t.Fatalf("body = %q, want the directory's page, not the code's bundle", body)
	}
	// Code that sets it loses to the environment.
	other := t.TempDir()
	writeTree(t, other, map[string]string{"layouts/base.html": `{{block "body" .}}{{end}}`, "pages/index.html": `{{define "body"}}other{{end}}`})
	s, err = New(Options{Bundle: siteBundle(), CacheDir: t.TempDir(), Logger: quiet(), Overrides: Overrides{Bundle: other}})
	if err != nil {
		t.Fatal(err)
	}
	if body := get(s, "/").Body.String(); body != "from the directory" {
		t.Fatalf("env must win over Options.Overrides.Bundle, got %q", body)
	}
	// With no variable, the code's Overrides.Bundle applies, and Options.Bundle may be left nil.
	t.Setenv("HEARTH_BUNDLE", "")
	s, err = New(Options{CacheDir: t.TempDir(), Logger: quiet(), Overrides: Overrides{Bundle: other}})
	if err != nil {
		t.Fatal(err)
	}
	if body := get(s, "/").Body.String(); body != "other" {
		t.Fatalf("Overrides.Bundle = %q", body)
	}
	if _, err := New(Options{CacheDir: t.TempDir(), Logger: quiet()}); err == nil {
		t.Fatal("no bundle at all must still fail")
	}
}

func TestBundleOverrideMustBeAnExistingDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	writeTree(t, filepath.Dir(file), map[string]string{"f": "x"})
	for _, bad := range []string{filepath.Join(t.TempDir(), "missing"), file} {
		t.Setenv("HEARTH_BUNDLE", bad)
		_, err := New(Options{Bundle: siteBundle(), CacheDir: t.TempDir(), Logger: quiet()})
		if err == nil || !strings.Contains(err.Error(), "HEARTH_BUNDLE") {
			t.Errorf("HEARTH_BUNDLE=%s: err = %v, want one naming HEARTH_BUNDLE", bad, err)
		}
	}
}

func TestSSRListComesFromTheConfigWhenThereIsNoManifest(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{
		"hearth.config.json": `{"baseURL":"https://x.test","ssr":["/"],"dev":{"proxy":{"/api":"http://localhost:1"}}}`,
		"layouts/base.html":  `{{block "body" .}}{{end}}`,
		"pages/index.html":   `{{define "body"}}{{.Data}}{{end}}`,
	})
	t.Setenv("HEARTH_BUNDLE", src)
	t.Setenv("HEARTH_ASSETS", "placeholder")
	t.Setenv("HEARTH_CACHE", "off")
	s, err := New(Options{Bundle: siteBundle(), CacheDir: t.TempDir(), Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Handle("/", func(*http.Request, map[string]string) (any, Meta, error) { return "live data", Meta{}, nil }); err != nil {
		t.Fatalf("an ssr page of the config must take a handler: %v", err)
	}
	if body := get(s, "/").Body.String(); body != "live data" {
		t.Fatalf("body = %q", body)
	}

	// Without the config's ssr list the same handler would be shadowed by the prebuilt page, as before.
	writeTree(t, src, map[string]string{"hearth.config.json": `{}`})
	s, _ = New(Options{Bundle: siteBundle(), CacheDir: t.TempDir(), Logger: quiet()})
	if err := s.Handle("/", func(*http.Request, map[string]string) (any, Meta, error) { return nil, Meta{}, nil }); err == nil {
		t.Fatal("a page that is not in ssr must refuse a handler")
	}
	// A broken config is an error that names the file.
	writeTree(t, src, map[string]string{"hearth.config.json": `{oops`})
	if _, err := New(Options{Bundle: siteBundle(), CacheDir: t.TempDir(), Logger: quiet()}); err == nil || !strings.Contains(err.Error(), "hearth.config.json") {
		t.Fatalf("broken config: %v", err)
	}
}

func TestAManifestIsAuthoritativeOverTheConfigsSSR(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{
		"manifest.json":      `{"format":3}`,
		"hearth.config.json": `{"ssr":["/"]}`,
		"layouts/base.html":  `x`,
		"pages/index.html":   `x`,
	})
	s, err := New(Options{Bundle: os.DirFS(src), CacheDir: t.TempDir(), Logger: quiet(), IgnoreEnv: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Handle("/", func(*http.Request, map[string]string) (any, Meta, error) { return nil, Meta{}, nil }); err == nil {
		t.Fatal("the build's manifest lists no ssr page; the config next to it must not add one")
	}
}

// One server function, two environments: what the website's main.go and dev.go became.
func TestOneServerRunsInProductionAndDevelopment(t *testing.T) {
	const layout = `{{block "body" .}}{{end}}|{{entry "src/entry.ts"}}`
	prod, dev := t.TempDir(), t.TempDir()
	writeTree(t, prod, map[string]string{ // what `hearth build` writes into dist/templates
		"manifest.json":     `{"format":3,"ssr":["/"],"entries":{"src/entry.ts":{"js":"/assets/e-1.js","css":["/assets/e-1.css"]}}}`,
		"layouts/base.html": layout,
		"pages/index.html":  `{{define "body"}}home {{.Data}}{{end}}`,
	})
	writeTree(t, dev, map[string]string{ // the source tree
		"hearth.config.json": `{"ssr":["/"]}`,
		"layouts/base.html":  layout,
		"pages/index.html":   `{{define "body"}}home {{.Data}}{{end}}`,
	})
	newServer := func(t *testing.T) (*Site, string) { // the only server code; it never mentions development
		dir := t.TempDir()
		s, err := New(Options{Bundle: os.DirFS(prod), CacheDir: dir, Logger: quiet()})
		if err != nil {
			t.Fatal(err)
		}
		s.MustHandle("/", func(*http.Request, map[string]string) (any, Meta, error) { return "(db)", Meta{}, nil })
		return s, dir
	}

	t.Run("production", func(t *testing.T) {
		s, dir := newServer(t)
		w := get(s, "/")
		if w.Body.String() != `home (db)|<link rel="stylesheet" href="/assets/e-1.css">`+"\n"+`<script type="module" src="/assets/e-1.js"></script>` {
			t.Fatalf("body = %q", w.Body)
		}
		if len(cachedFiles(t, dir)) == 0 {
			t.Fatal("production caches the page")
		}
	})
	t.Run("development", func(t *testing.T) {
		t.Setenv("HEARTH_ENV", "dev")
		t.Setenv("HEARTH_BUNDLE", dev)
		t.Setenv("HEARTH_ASSETS", "vite:http://localhost:5173")
		s, dir := newServer(t)
		body := get(s, "/").Body.String()
		if !strings.Contains(body, "home (db)|") || !strings.Contains(body, "http://localhost:5173/@vite/client") {
			t.Fatalf("body = %q", body)
		}
		writeTree(t, dev, map[string]string{"pages/index.html": `{{define "body"}}EDITED {{.Data}}{{end}}`})
		if body := get(s, "/").Body.String(); !strings.HasPrefix(body, "EDITED (db)|") {
			t.Fatalf("an edit to the source tree must show on the next request, got %q", body)
		}
		if len(cachedFiles(t, dir)) != 0 {
			t.Fatal("development keeps no cache")
		}
	})
}
