package dev

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orelhadad/hearth/internal/config"

	"github.com/orelhadad/hearth/internal/testutil"
)

func TestMockHandler(t *testing.T) {
	f := testutil.BaseSite()
	f["pages/post/[id].mock.json"] = `{"1":{"title":"One"},"*":{"title":"Fallback"}}`
	f["pages/entry.html"] = `{{define "body"}}<script src="{{asset "src/entry.ts"}}"></script>{{end}}`
	site := testutil.WriteSite(t, f)
	vite := fakeUpstream(t, "vite.test", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("vite:" + r.URL.Path)) })
	cfg := &config.Config{DefaultLang: "en", I18n: config.I18nConfig{Mode: "prefix"}, Vite: &config.ViteConfig{Command: []string{"x"}, DevURL: vite.URL}}
	h, err := NewMock(site, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if w := testutil.Get(h, "/post/1"); w.Code != 200 || !strings.Contains(w.Body.String(), "One #1") {
		t.Errorf("post 1: %d %s", w.Code, w.Body)
	}
	if w := testutil.Get(h, "/post/zzz"); !strings.Contains(w.Body.String(), "Fallback #zzz") {
		t.Errorf("fallback: %s", w.Body)
	}
	if w := testutil.Get(h, "/he"); !strings.Contains(w.Body.String(), "שלום") {
		t.Errorf("he: %s", w.Body)
	}
	if w := testutil.Get(h, "/entry"); !strings.Contains(w.Body.String(), `src="`+vite.URL+`/src/entry.ts"`) {
		t.Errorf("asset not pointed at vite: %s", w.Body)
	}
	if w := testutil.Get(h, "/src/entry.ts"); w.Body.String() != "vite:/src/entry.ts" {
		t.Errorf("unmatched path should proxy to vite: %s", w.Body)
	}
	// edits show up without restart
	os.WriteFile(filepath.Join(site, "pages", "about.html"), []byte(`{{define "body"}}changed{{end}}`), 0o644)
	if w := testutil.Get(h, "/about"); !strings.Contains(w.Body.String(), "changed") {
		t.Errorf("no live reload of templates: %s", w.Body)
	}
}

func TestMockHandlerNoMockIs404(t *testing.T) {
	site := testutil.WriteSite(t, testutil.BaseSite())
	h, _ := NewMock(site, &config.Config{DefaultLang: "en", I18n: config.I18nConfig{Mode: "prefix"}})
	if w := testutil.Get(h, "/post/1"); w.Code != 404 || !strings.Contains(w.Body.String(), "mock.json") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
	if w := testutil.Get(h, "/nothing"); w.Code != 404 {
		t.Errorf("%d", w.Code)
	}
}

func TestPreviewFileThenServer(t *testing.T) {
	pub := t.TempDir()
	os.MkdirAll(filepath.Join(pub, "about"), 0o755)
	os.WriteFile(filepath.Join(pub, "about", "index.html"), []byte("from file"), 0o644)
	os.MkdirAll(filepath.Join(pub, "assets"), 0o755)
	os.WriteFile(filepath.Join(pub, "assets", "a.js"), []byte("js"), 0o644)
	var hits int
	srv := fakeUpstream(t, "app.test", func(w http.ResponseWriter, r *http.Request) { hits++; w.Write([]byte("from server " + r.URL.Path)) })
	h, err := NewPreview(pub, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if w := testutil.Get(h, "/about"); w.Body.String() != "from file" {
		t.Errorf("about: %q", w.Body)
	}
	if w := testutil.Get(h, "/assets/a.js"); w.Body.String() != "js" {
		t.Errorf("asset: %q", w.Body)
	}
	if w := testutil.Get(h, "/post/9"); w.Body.String() != "from server /post/9" {
		t.Errorf("post: %q", w.Body)
	}
	if w := testutil.Get(h, "/../../etc/passwd"); strings.Contains(w.Body.String(), "root:") {
		t.Error("traversal served a file")
	}
	if hits != 2 && hits != 1 {
		t.Errorf("server hits = %d", hits)
	}
}

func TestProxyTargetValidation(t *testing.T) {
	for _, bad := range []string{"", "ftp://x", "localhost:3000", "http://"} {
		if _, err := NewLive(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
	fakeUpstream(t, "down.test", nil)
	h, _ := NewLive("http://down.test")
	if w := testutil.Get(h, "/"); w.Code != http.StatusBadGateway {
		t.Errorf("unreachable upstream = %d", w.Code)
	}
}

type upstream struct {
	URL string
}

type memTransport map[string]http.HandlerFunc

func (m memTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	h, ok := m[r.URL.Host]
	if !ok || h == nil {
		return nil, fmt.Errorf("dial %s: connection refused", r.URL.Host)
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec.Result(), nil
}

// fakeUpstream registers an in-memory server (no sockets) reachable as http://host.
func fakeUpstream(t *testing.T, host string, h http.HandlerFunc) upstream {
	t.Helper()
	mt, _ := proxyTransport.(memTransport)
	if mt == nil {
		mt = memTransport{}
		proxyTransport = mt
		t.Cleanup(func() { proxyTransport = nil })
	}
	mt[host] = h
	return upstream{URL: "http://" + host}
}

func TestMockServesSSRPagesFromTheirMockFile(t *testing.T) {
	f := testutil.BaseSite()
	f["pages/candidates.html"] = `{{define "body"}}{{range .Data.names}}<b>{{.}}</b>{{end}}{{end}}`
	f["pages/candidates.mock.json"] = `{"names":["Ann","Bo"]}`
	f["pages/stats.html"] = `{{define "body"}}{{num 5}}{{end}}`
	site := testutil.WriteSite(t, f)
	cfg := &config.Config{DefaultLang: "en", I18n: config.I18nConfig{Mode: "prefix"}, SSR: []string{"/candidates"}, Funcs: []string{"num"}}
	h, err := NewMock(site, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/candidates", "/he/candidates"} {
		if w := testutil.Get(h, p); w.Code != 200 || !strings.Contains(w.Body.String(), "<b>Ann</b><b>Bo</b>") {
			t.Errorf("%s: %d %s", p, w.Code, w.Body)
		}
	}
	// a stubbed function fails loudly instead of rendering a partial page
	if w := testutil.Get(h, "/stats"); w.Code != 500 {
		t.Errorf("stub call: %d %s", w.Code, w.Body)
	}
	// an ssr entry that matches no page is reported, not ignored
	cfg.SSR = []string{"/missing"}
	h, _ = NewMock(site, cfg)
	if w := testutil.Get(h, "/"); w.Code != 500 || !strings.Contains(w.Body.String(), "/missing") {
		t.Errorf("unknown ssr entry: %d %s", w.Code, w.Body)
	}
}

func TestMockServesTheErrorTemplateWithStatus(t *testing.T) {
	f := testutil.BaseSite()
	f["pages/404.html"] = `{{define "body"}}branded {{.Status}}{{end}}`
	h, err := NewMock(testutil.WriteSite(t, f), &config.Config{DefaultLang: "en", I18n: config.I18nConfig{Mode: "prefix"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/nothing", "/he/nothing"} {
		if w := testutil.Get(h, p); w.Code != 404 || !strings.Contains(w.Body.String(), "branded 404") {
			t.Errorf("%s: %d %s", p, w.Code, w.Body)
		}
	}
}
