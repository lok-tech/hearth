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
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func sitemapSite(t *testing.T, mod func(*Options)) *Site {
	t.Helper()
	o := Options{Bundle: siteBundle(), CacheDir: t.TempDir(), BaseURL: "https://x.test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if mod != nil {
		mod(&o)
	}
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	s.MustHandle("post/{id}", func(r *http.Request, p map[string]string) (any, Meta, error) {
		return "post " + p["id"], Meta{}, nil
	})
	return s
}

func TestSitemapLastMod(t *testing.T) {
	zone := time.FixedZone("x", 3*3600)
	older := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	newer := time.Date(2026, 5, 6, 12, 0, 0, 0, zone)
	s := sitemapSite(t, func(o *Options) {
		o.SitemapEntries = func(context.Context) ([]string, error) { return []string{"/post/1", "/post/2"}, nil } // deprecated: no date
		o.SitemapURLs = func(context.Context) ([]SitemapEntry, error) {
			return []SitemapEntry{{"/post/2", older}, {"/post/2", newer}, {"/post/3", newer}, {"/post/2", time.Time{}}}, nil
		}
	})
	sm := get(s, "/sitemap.xml").Body.String()
	for _, want := range []string{
		"<url><loc>https://x.test/post/1</loc></url>",                                        // legacy entry: no lastmod
		"<url><loc>https://x.test/post/2</loc><lastmod>2026-05-06T09:00:00Z</lastmod></url>", // latest wins, UTC
		"<url><loc>https://x.test/post/3</loc><lastmod>2026-05-06T09:00:00Z</lastmod></url>",
		"<url><loc>https://x.test/</loc></url>", // static page: nothing invented
	} {
		if !strings.Contains(sm, want) {
			t.Errorf("sitemap missing %s:\n%s", want, sm)
		}
	}
	if n := strings.Count(sm, "<loc>https://x.test/post/2</loc>"); n != 1 {
		t.Errorf("post/2 listed %d times", n)
	}
	// Deterministic: sorted, identical on every request.
	if again := get(s, "/sitemap.xml").Body.String(); again != sm {
		t.Error("sitemap output is not stable")
	}
	if strings.Index(sm, "/post/1<") > strings.Index(sm, "/post/2<") {
		t.Error("not sorted")
	}
}

func TestSitemapLastModAppliesToEveryLanguage(t *testing.T) {
	d := time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC)
	s, _ := newI18nSite(t, func(o *Options) {
		o.SitemapURLs = func(context.Context) ([]SitemapEntry, error) { return []SitemapEntry{{"/post/1", d}}, nil }
	})
	sm := get(s, "/sitemap.xml").Body.String()
	for _, want := range []string{"<loc>https://x.test/post/1</loc><lastmod>2026-02-03T00:00:00Z", "<loc>https://x.test/he/post/1</loc><lastmod>2026-02-03T00:00:00Z"} {
		if !strings.Contains(sm, want) {
			t.Errorf("missing %s:\n%s", want, sm)
		}
	}
}

func TestSitemapURLsErrorIs500(t *testing.T) {
	s := sitemapSite(t, func(o *Options) {
		o.SitemapURLs = func(context.Context) ([]SitemapEntry, error) { return nil, io.ErrUnexpectedEOF }
	})
	if w := get(s, "/sitemap.xml"); w.Code != 500 {
		t.Errorf("code = %d", w.Code)
	}
}

func manyPosts(n int) func(context.Context) ([]SitemapEntry, error) {
	return func(context.Context) ([]SitemapEntry, error) {
		var out []SitemapEntry
		for i := n; i >= 1; i-- { // unsorted on purpose
			out = append(out, SitemapEntry{Path: fmt.Sprintf("/post/%02d", i), LastMod: time.Date(2026, 1, i, 0, 0, 0, 0, time.UTC)})
		}
		return out, nil
	}
}

var locRE = regexp.MustCompile(`<loc>([^<]*)</loc>`)

func locs(xml string) []string {
	var out []string
	for _, m := range locRE.FindAllStringSubmatch(xml, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestSitemapSplitsAboveTheLimit(t *testing.T) {
	whole := sitemapSite(t, func(o *Options) { o.SitemapURLs = manyPosts(7) })
	want := locs(get(whole, "/sitemap.xml").Body.String())
	if len(want) != 8 {
		t.Fatalf("whole sitemap has %d urls", len(want))
	}
	if w := get(whole, "/sitemap-1.xml"); w.Code != 404 {
		t.Errorf("a sitemap that fits one file has no parts: %d", w.Code)
	}

	s := sitemapSite(t, func(o *Options) { o.SitemapURLs = manyPosts(7) })
	s.sitemapMax.urls = 3
	w := get(s, "/sitemap.xml")
	idx := w.Body.String()
	if w.Code != 200 || !strings.Contains(idx, "<sitemapindex") || strings.Contains(idx, "<urlset") {
		t.Fatalf("expected an index: %d\n%s", w.Code, idx)
	}
	if got := locs(idx); len(got) != 3 || got[0] != "https://x.test/sitemap-1.xml" || got[2] != "https://x.test/sitemap-3.xml" {
		t.Fatalf("index entries = %v", got)
	}
	var joined []string
	for n := 1; n <= 3; n++ {
		part := get(s, fmt.Sprintf("/sitemap-%d.xml", n))
		if part.Code != 200 || !strings.Contains(part.Body.String(), "<urlset") {
			t.Fatalf("part %d: %d %s", n, part.Code, part.Body)
		}
		l := locs(part.Body.String())
		if len(l) > 3 {
			t.Errorf("part %d has %d urls", n, len(l))
		}
		joined = append(joined, l...)
	}
	if strings.Join(joined, "\n") != strings.Join(want, "\n") {
		t.Errorf("parts joined differ from the single sitemap:\n%v\n%v", joined, want)
	}
	if p := get(s, "/sitemap-2.xml").Body.String(); !strings.Contains(p, "<lastmod>") {
		t.Errorf("parts keep lastmod: %s", p)
	}
	for _, bad := range []string{"/sitemap-4.xml", "/sitemap-0.xml", "/sitemap-01.xml", "/sitemap-x.xml", "/sitemap-.xml"} {
		if c := get(s, bad).Code; c != 404 {
			t.Errorf("%s = %d", bad, c)
		}
	}
	// the byte limit splits too
	s2 := sitemapSite(t, func(o *Options) { o.SitemapURLs = manyPosts(7) })
	s2.sitemapMax.bytes = 700
	if !strings.Contains(get(s2, "/sitemap.xml").Body.String(), "<sitemapindex") {
		t.Error("byte limit did not split")
	}
}

// What `hearth build` writes is what the server answers, split or not.
func TestBuildWritesSitemapPartsLikeTheServer(t *testing.T) {
	for _, split := range []bool{false, true} {
		t.Run(fmt.Sprint("split=", split), func(t *testing.T) {
			s := sitemapSite(t, func(o *Options) { o.SitemapURLs = manyPosts(7) })
			if split {
				s.sitemapMax.urls = 3
			}
			out := t.TempDir()
			if _, err := Build(context.Background(), s, out); err != nil {
				t.Fatal(err)
			}
			names := []string{"sitemap.xml", "robots.txt"}
			if split {
				names = append(names, "sitemap-1.xml", "sitemap-2.xml", "sitemap-3.xml")
			}
			for _, n := range names {
				built, err := os.ReadFile(filepath.Join(out, n))
				if err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				s.ServeHTTP(w, httptest.NewRequest("GET", "/"+n, nil))
				if w.Body.String() != string(built) {
					t.Errorf("%s differs:\n--- built\n%s\n--- server\n%s", n, built, w.Body)
				}
			}
			if _, err := os.Stat(filepath.Join(out, "sitemap-4.xml")); err == nil {
				t.Error("too many parts written")
			}
			if split {
				if _, err := os.Stat(filepath.Join(out, "sitemap-1.xml")); err != nil {
					t.Error(err)
				}
			} else if _, err := os.Stat(filepath.Join(out, "sitemap-1.xml")); err == nil {
				t.Error("part written without a split")
			}
			if robots, _ := os.ReadFile(filepath.Join(out, "robots.txt")); !strings.Contains(string(robots), "Sitemap: https://x.test/sitemap.xml\n") {
				t.Errorf("robots.txt: %s", robots)
			}
			// pages from SitemapURLs were prerendered
			if _, err := os.Stat(filepath.Join(out, "post", "07", "index.html")); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestWarmPathsIncludeSitemapURLs(t *testing.T) {
	s := sitemapSite(t, func(o *Options) { o.SitemapURLs = manyPosts(2) })
	paths, err := s.WarmPaths(context.Background())
	if err != nil || strings.Join(paths, " ") != "/ /post/01 /post/02" {
		t.Errorf("%v %v", paths, err)
	}
}

// {{entry}} in a layout, against a format 3 manifest, a Vite dev server, and a source tree.
func entryBundle(manifestJSON string) fstest.MapFS {
	b := siteBundle()
	b["layouts/base.html"] = &fstest.MapFile{Data: []byte(`<head>{{entry "src/site.ts"}}</head>{{block "body" .}}{{end}}`)}
	if manifestJSON != "" {
		b["manifest.json"] = &fstest.MapFile{Data: []byte(manifestJSON)}
	}
	return b
}

const entryManifestJSON = `{"format":3,"entries":{"src/site.ts":{"js":"/assets/site-1.js","css":["/assets/shared-2.css","/assets/site-3.css"],"preload":["/assets/shared-4.js"]}}}`

func TestEntryHelperInASite(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := New(Options{Bundle: entryBundle(entryManifestJSON), CacheDir: t.TempDir(), Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	body := get(s, "/").Body.String()
	for _, want := range []string{`<link rel="stylesheet" href="/assets/shared-2.css">`, `<link rel="stylesheet" href="/assets/site-3.css">`,
		`<link rel="modulepreload" href="/assets/shared-4.js">`, `<script type="module" src="/assets/site-1.js"></script>`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in %s", want, body)
		}
	}

	// unknown entry outside lenient/dev: the render fails, nothing is cached
	s, err = New(Options{Bundle: entryBundle(`{"format":3}`), CacheDir: t.TempDir(), Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	if w := get(s, "/"); w.Code != 500 {
		t.Errorf("unknown entry = %d, want 500", w.Code)
	}
	if _, err := Build(context.Background(), s, t.TempDir()); err == nil || !strings.Contains(err.Error(), "entry") {
		t.Errorf("build with an unknown entry: %v", err)
	}

	// lenient (hearth check): placeholder
	s, err = New(Options{Bundle: entryBundle(""), CacheDir: t.TempDir(), LenientAssets: true, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	if body := get(s, "/").Body.String(); !strings.Contains(body, `<script type="module" src="/src/site.ts"></script>`) || strings.Contains(body, "stylesheet") {
		t.Errorf("lenient: %s", body)
	}

	// dev server: the Vite client and the module script
	s, err = New(Options{Bundle: entryBundle(""), CacheDir: t.TempDir(), Dev: true, DevAssetURL: "http://localhost:5173/", Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	if body := get(s, "/").Body.String(); !strings.Contains(body, `<script type="module" src="http://localhost:5173/@vite/client"></script>`) ||
		!strings.Contains(body, `<script type="module" src="http://localhost:5173/src/site.ts"></script>`) || strings.Contains(body, "stylesheet") {
		t.Errorf("dev: %s", body)
	}
}
