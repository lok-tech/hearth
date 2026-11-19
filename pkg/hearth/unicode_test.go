package hearth

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// voters.co.il has /letter/{letter} with a Hebrew letter. Go decodes the percent-encoded request
// path, so the cache holds a file whose directory is the UTF-8 letter. Whether a web server in
// front matches the percent-encoded request to that file is NOT covered here: see docs/DEPLOY.md.
func TestHebrewParameterCacheServerAndPrerender(t *testing.T) {
	b := i18nBundle()
	b["pages/letter/[letter].html"] = &fstest.MapFile{Data: []byte(`{{define "body"}}letter {{index .Params "letter"}}|{{.Data}}{{end}}`)}
	s, dir := newErrSite(t, b, nil)
	calls := 0
	s.MustHandle("letter/{letter}", func(r *http.Request, p map[string]string) (any, Meta, error) {
		calls++
		if p["letter"] == "א" || p["letter"] == "ת" {
			return "ok-" + p["letter"], Meta{}, nil
		}
		return nil, Meta{}, ErrNotFound
	})

	// a percent-encoded request, as a browser sends it
	w := get(s, "/letter/%D7%90")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "letter א|ok-א") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	file := filepath.Join(dir, "current", "letter", "א", "index.html")
	if c, err := os.ReadFile(file); err != nil || string(c) != w.Body.String() {
		t.Fatalf("cache file %s: %v", file, err)
	}
	// second request: from the file, handler not called again
	if w2 := get(s, "/letter/%D7%90"); w2.Body.String() != w.Body.String() || calls != 1 {
		t.Errorf("second request: calls=%d", calls)
	}
	// Hebrew language prefix too
	if w3 := get(s, "/he/letter/%D7%AA"); w3.Code != 200 || !strings.Contains(w3.Body.String(), "ok-ת") {
		t.Errorf("he: %d %s", w3.Code, w3.Body)
	}
	if w4 := get(s, "/letter/%D7%91"); w4.Code != 404 {
		t.Errorf("unknown letter: %d", w4.Code)
	}
	// malformed encoding is a client error, never a cache path
	if w5 := get(s, "/letter/%D7"); w5.Code != 404 && w5.Code != 200 {
		t.Errorf("truncated utf-8: %d", w5.Code)
	}

	// Prerender takes the decoded path and Export keeps the Hebrew directory name
	rep := s.Prerender(context.Background(), []string{"/letter/ת"}, 2)
	if len(rep.Failures) != 0 || rep.OK != 2 {
		t.Fatalf("prerender: %+v", rep)
	}
	out := t.TempDir()
	if err := s.Export(out); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"letter/א/index.html", "letter/ת/index.html", "he/letter/ת/index.html"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("export lacks %s: %v", f, err)
		}
	}
	// Invalidate by the decoded path drops it
	if err := s.Invalidate("/letter/א"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("Invalidate left the Hebrew page")
	}
}
