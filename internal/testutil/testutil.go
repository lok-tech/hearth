// Package testutil holds helpers shared by the tests of the internal packages.
package testutil

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// WriteSite writes files (path -> content) into a fresh temp dir and returns it.
func WriteSite(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, c := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// BaseSite is a small two-language site with a bound collection, used by build and dev tests.
func BaseSite() map[string]string {
	return map[string]string{
		"hearth.config.json":        `{"baseURL":"https://x.test","collections":{"post/{id}":{"data":"posts","param":"id"}}}`,
		"layouts/base.html":         `<html lang="{{.Lang}}" dir="{{.Dir}}"><title>{{block "title" .}}x{{end}}</title><link rel="canonical" href="{{.Canonical}}">{{range $l,$u := .Alternates}}<link rel="alternate" hreflang="{{$l}}" href="{{$u}}">{{end}}{{block "body" .}}{{end}}`,
		"pages/index.html":          `{{define "body"}}{{t "hi"}}{{end}}`,
		"pages/about.html":          `{{define "body"}}about{{end}}`,
		"pages/post/[id].html":      `{{define "title"}}{{.Data.title}}{{end}}{{define "body"}}{{.Data.title}} #{{index .Params "id"}}{{end}}`,
		"pages/post/[id].mock.json": `{}`,
		"data/posts.json":           `[{"id":1,"title":"First"},{"id":"two","title":"Second"}]`,
		"locales/en.json":           `{"hi":"Hello"}`,
		"locales/he.json":           `{"hi":"שלום"}`,
		"public/favicon.txt":        `fav`,
	}
}

// Read returns the content of the file at filepath.Join(parts...).
func Read(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Get serves one GET through h; hdr is alternating header names and values.
func Get(h http.Handler, path string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}
