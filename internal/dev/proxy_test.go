package dev

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lok-tech/hearth/internal/config"
	"github.com/lok-tech/hearth/internal/testutil"
)

// These tests run in process: the backends are in-memory (fakeUpstream), no sockets are opened.

func proxyCfg(vite string, proxy map[string]string) *config.Config {
	c := &config.Config{DefaultLang: "en", I18n: config.I18nConfig{Mode: "prefix"}, Dev: config.DevConfig{Proxy: proxy}}
	if vite != "" {
		c.Vite = &config.ViteConfig{Command: []string{"x"}, DevURL: vite}
	}
	return c
}

func TestDevProxyReachesBackendBeforePagesAndVite(t *testing.T) {
	var got *http.Request
	var body string
	api := fakeUpstream(t, "api.test", func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			body = string(b)
		}
		w.Header().Set("X-From", "api")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("api:" + r.URL.Path))
	})
	other := fakeUpstream(t, "other.test", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("other:" + r.URL.Path)) })
	vite := fakeUpstream(t, "vite.test", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("vite:" + r.URL.Path)) })
	f := testutil.BaseSite()
	f["pages/api.html"] = `{{define "body"}}page api{{end}}` // a page under a proxied prefix: the proxy wins
	site := testutil.WriteSite(t, f)
	h, err := NewMock(site, proxyCfg(vite.URL, map[string]string{"/api": api.URL, "/api/other": other.URL, "/uploads": api.URL + "/files"}))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/api/items/7?x=1&y=%2F", strings.NewReader(`{"a":1}`))
	req.Host = "localhost:3000"
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "s=1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 201 || w.Body.String() != "api:/api/items/7" || w.Header().Get("X-From") != "api" {
		t.Fatalf("%d %q %v", w.Code, w.Body, w.Header())
	}
	if got.Method != "POST" || got.URL.RawQuery != "x=1&y=%2F" || body != `{"a":1}` ||
		got.Header.Get("Authorization") != "Bearer t" || got.Header.Get("Content-Type") != "application/json" || got.Header.Get("Cookie") != "s=1" {
		t.Errorf("request not preserved: %s %s %q %v", got.Method, got.URL, body, got.Header)
	}
	if got.URL.Host != "api.test" || (got.Host != "" && got.Host != "api.test") { // "" means: use the URL's host
		t.Errorf("outbound host = %q / %q, want the backend's, not localhost:3000", got.URL.Host, got.Host)
	}
	if got.Header.Get("X-Forwarded-Host") != "localhost:3000" || got.Header.Get("X-Forwarded-Proto") != "http" || got.Header.Get("X-Forwarded-For") == "" {
		t.Errorf("X-Forwarded-*: %v", got.Header)
	}
	// the exact prefix, longest prefix first, and a target with a base path
	if w := testutil.Get(h, "/api"); w.Body.String() != "api:/api" {
		t.Errorf("/api: %q", w.Body)
	}
	if w := testutil.Get(h, "/api/other/x"); w.Body.String() != "other:/api/other/x" {
		t.Errorf("longest prefix: %q", w.Body)
	}
	if w := testutil.Get(h, "/uploads/a.png"); w.Body.String() != "api:/files/uploads/a.png" {
		t.Errorf("target path: %q", w.Body)
	}
	// page routes and the Vite fallback are untouched
	if w := testutil.Get(h, "/about"); w.Code != 200 || strings.Contains(w.Body.String(), "api:") {
		t.Errorf("page shadowed: %d %s", w.Code, w.Body)
	}
	if w := testutil.Get(h, "/src/entry.ts"); w.Body.String() != "vite:/src/entry.ts" {
		t.Errorf("vite fallback: %q", w.Body)
	}
	// segment boundary: /apiary is not /api
	if w := testutil.Get(h, "/apiary"); strings.Contains(w.Body.String(), "api:") || w.Body.String() != "vite:/apiary" {
		t.Errorf("/apiary was proxied: %q", w.Body)
	}
	if w := testutil.Get(h, "/api2/x"); strings.Contains(w.Body.String(), "api:") {
		t.Errorf("/api2 was proxied: %q", w.Body)
	}
}

func TestDevProxyWorksWithoutViteAndReportsADownBackend(t *testing.T) {
	site := testutil.WriteSite(t, testutil.BaseSite())
	fakeUpstream(t, "down.test", nil) // registered as unreachable
	h, err := NewMock(site, proxyCfg("", map[string]string{"/api": "http://down.test"}))
	if err != nil {
		t.Fatal(err)
	}
	if w := testutil.Get(h, "/api/x"); w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "down.test") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
	if w := testutil.Get(h, "/about"); w.Code != 200 {
		t.Errorf("page: %d", w.Code)
	}
}

// Watch mode wraps the site in live reload; the proxy must sit outside it, so a backend's HTML
// is not rewritten and the reload stream path stays Hearth's.
func TestDevProxyOutsideLiveReload(t *testing.T) {
	fakeUpstream(t, "api.test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<body>fragment</body>"))
	})
	rl := newReloader()
	site := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<body>page</body>"))
	})
	h, err := withProxy(map[string]string{"/api": "http://api.test"}, rl.middleware(site))
	if err != nil {
		t.Fatal(err)
	}
	if w := testutil.Get(h, "/api/frag"); w.Body.String() != "<body>fragment</body>" {
		t.Errorf("backend HTML rewritten: %q", w.Body)
	}
	if w := testutil.Get(h, "/page"); !strings.Contains(w.Body.String(), "EventSource") {
		t.Errorf("page not injected: %q", w.Body)
	}
	if _, err := withProxy(map[string]string{"/__hearth": "http://api.test"}, site); err == nil {
		t.Error("a prefix covering the reload path should be refused")
	}
}

func TestDevProxyIgnoredByPreviewAndLive(t *testing.T) {
	// preview and live take only dev.server; the proxy map is never read there
	srv := fakeUpstream(t, "app.test", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("server:" + r.URL.Path)) })
	h, _ := NewPreview(t.TempDir(), srv.URL)
	if w := testutil.Get(h, "/api/x"); w.Body.String() != "server:/api/x" {
		t.Errorf("preview: %q", w.Body)
	}
}
