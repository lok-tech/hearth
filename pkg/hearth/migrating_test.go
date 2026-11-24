package hearth_test

// The Go snippets of docs/MIGRATING-FROM-ASTRO.md live here, so they compile and run against the
// real API. If you change one, change the guide.

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lok-tech/hearth/pkg/hearth"
)

// --- guide: "Number formatting" and "trend" ------------------------------------------------

// group inserts a comma between thousands: 1234567 -> "1,234,567". he-IL uses the same grouping
// as en-US; for another locale change the separator.
func group(digits string) string {
	for i := len(digits) - 3; i > 0; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	return digits
}

func num(n int) string {
	if n < 0 {
		return "-" + group(strconv.Itoa(-n))
	}
	return group(strconv.Itoa(n))
}

// dec formats f with the given number of decimals and grouped thousands: dec 1234.5 1 -> "1,234.5".
func dec(f float64, places int) string {
	s := strconv.FormatFloat(f, 'f', places, 64)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, hasFrac := strings.Cut(s, ".")
	out := group(whole)
	if hasFrac {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// trend draws a sparkline. It takes numbers only and writes fixed attributes with strconv, so no
// string a user wrote ever reaches the markup. That is what makes template.HTML safe here.
func trend(points []float64) template.HTML {
	if len(points) < 2 {
		return ""
	}
	lo, hi := points[0], points[0]
	for _, p := range points {
		lo, hi = min(lo, p), max(hi, p)
	}
	span := hi - lo
	if span == 0 {
		span = 1
	}
	const w, h = 120.0, 32.0
	var b strings.Builder
	b.WriteString(`<svg class="trend" viewBox="0 0 120 32" width="120" height="32" role="img" aria-hidden="true"><polyline fill="none" stroke="currentColor" stroke-width="2" points="`)
	for i, p := range points {
		x := w * float64(i) / float64(len(points)-1)
		y := h - (p-lo)/span*h
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strconv.FormatFloat(x, 'f', 1, 64))
		b.WriteByte(',')
		b.WriteString(strconv.FormatFloat(y, 'f', 1, 64))
	}
	b.WriteString(`"/></svg>`)
	return template.HTML(b.String())
}

// --- guide: a pilot site --------------------------------------------------------------------

type candidate struct {
	ID      string
	Name    string
	Votes   int
	Score   float64
	History []float64
	Updated time.Time
}

var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// store stands in for the database.
type store struct {
	byID    map[string]candidate
	renamed map[string]string // old id -> new id (url_redirects)
	gone    map[string]bool
}

func (s store) loadCandidate(r *http.Request, p map[string]string) (any, hearth.Meta, error) {
	id := p["id"]
	if !slugRe.MatchString(id) || len(id) > 80 { // validate before anything else
		return nil, hearth.Meta{}, hearth.ErrNotFound
	}
	if to, ok := s.renamed[id]; ok && slugRe.MatchString(to) {
		return nil, hearth.Meta{}, hearth.Redirect(301, "/candidates/"+to) // a path we built, never a stored URL
	}
	if s.gone[id] {
		return nil, hearth.Meta{}, hearth.ErrGone
	}
	c, ok := s.byID[id]
	if !ok {
		return nil, hearth.Meta{}, hearth.ErrNotFound
	}
	// A page with no votes yet should not be indexed: Meta.NoIndex sets the header and the layout's <meta>.
	return c, hearth.Meta{NoIndex: c.Votes == 0}, nil
}

func (s store) loadList(*http.Request, map[string]string) (any, hearth.Meta, error) {
	list := make([]candidate, 0, len(s.byID))
	for _, c := range s.byID {
		list = append(list, c)
	}
	// Sort here, not in the template. This is plain byte order; for Hebrew collation see the guide.
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return map[string]any{"Candidates": list}, hearth.Meta{}, nil
}

func pilotBundle() fstest.MapFS {
	f := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	return fstest.MapFS{
		"manifest.json": f(`{"format":3,"version":"b1","ssr":["/candidates"],"entries":{"src/site.ts":{"js":"/assets/site-1.js","css":["/assets/site-2.css"],"preload":[]}}}`),
		"layouts/base.html": f(`<!doctype html><html lang="{{.Lang}}" dir="{{.Dir}}"><head>` +
			`<title>{{block "title" .}}Voters{{end}}</title>` +
			`{{if .NoIndex}}<meta name="robots" content="noindex">{{end}}` +
			`<link rel="canonical" href="{{.Canonical}}">{{entry "src/site.ts"}}</head>` +
			`<body>{{block "body" .}}{{end}}</body></html>`),
		"pages/candidates.html": f(`{{define "title"}}Candidates{{end}}{{define "body"}}<ul>` +
			`{{range .Data.Candidates}}<li><a href="{{$.Link "/candidates/"}}{{.ID}}">{{.Name}}</a> {{num .Votes}}</li>{{end}}</ul>{{end}}`),
		"pages/candidates/[id].html": f(`{{define "title"}}{{.Data.Name}}{{end}}{{define "body"}}` +
			`<main class="candidate" data-redirect-to="{{json .Data.ID}}" data-history="{{json .Data.History}}">` +
			`<h1>{{.Data.Name}}</h1><p>{{dec .Data.Score 1}} / {{dec .Data.Score 2}} / {{num .Data.Votes}}</p>{{trend .Data.History}}` +
			`<script type="application/json" id="candidate-data">{{json .Data}}</script></main>{{end}}`),
		"pages/404.html":  f(`{{define "title"}}Not found{{end}}{{define "body"}}<h1>{{.Status}}</h1>{{end}}`),
		"pages/410.html":  f(`{{define "title"}}Gone{{end}}{{define "body"}}<h1>{{.Status}}</h1>{{end}}`),
		"locales/he.json": f(`{}`),
	}
}

func newPilot(t *testing.T, forBuild bool) (*hearth.Site, store) {
	t.Helper()
	db := store{
		byID: map[string]candidate{
			"dana-levi": {ID: "dana-levi", Name: `דנה "לוי" </script>`, Votes: 1234567, Score: 4.567, History: []float64{3, 5, 4, 8}, Updated: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)},
			"new-one":   {ID: "new-one", Name: "חדש", Votes: 0, Score: 0, History: []float64{1, 1}},
		},
		renamed: map[string]string{"old-dana": "dana-levi", "evil": "//attacker.example"},
		gone:    map[string]bool{"removed": true},
	}
	opts := hearth.Options{
		Bundle:      pilotBundle(),
		CacheDir:    t.TempDir(),
		BaseURL:     "https://voters.example",
		DefaultLang: "he",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Funcs: template.FuncMap{
			"num":   num,
			"dec":   dec,
			"trend": trend,
		},
		SitemapURLs: func(ctx context.Context) ([]hearth.SitemapEntry, error) {
			var out []hearth.SitemapEntry
			for _, c := range db.byID {
				out = append(out, hearth.SitemapEntry{Path: "/candidates/" + c.ID, LastMod: c.Updated})
			}
			return out, nil
		},
	}
	if forBuild {
		// hearth.Build prerenders every SitemapURLs path into dist/public, and a file there would
		// shadow the handler. The build's Site leaves them out; the server's /sitemap.xml has them.
		opts.SitemapURLs = nil
	}
	site, err := hearth.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	site.MustHandle("/candidates", db.loadList)
	site.MustHandle("/candidates/{id}", db.loadCandidate)
	if err := site.Validate(); err != nil {
		t.Fatal(err)
	}
	return site, db
}

func serve(h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

func TestGuideFormatters(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{num(0), "0"}, {num(999), "999"}, {num(1000), "1,000"}, {num(1234567), "1,234,567"}, {num(-4321), "-4,321"},
		{dec(1234.5, 1), "1,234.5"}, {dec(4.567, 2), "4.57"}, {dec(-1234567.891, 2), "-1,234,567.89"}, {dec(7, 0), "7"},
	} {
		if c.got != c.want {
			t.Errorf("got %q want %q", c.got, c.want)
		}
	}
	got := string(trend([]float64{3, 5, 4, 8}))
	if !strings.Contains(got, `points="0.0,32.0 40.0,19.2 80.0,25.6 120.0,0.0"`) {
		t.Errorf("trend: %s", got)
	}
	if trend(nil) != "" || trend([]float64{1}) != "" {
		t.Error("trend of fewer than two points must be empty")
	}
}

func TestGuidePilotPages(t *testing.T) {
	site, _ := newPilot(t, false)

	w := serve(site, "/candidates/dana-levi")
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, body)
	}
	for _, want := range []string{
		`<html lang="he" dir="rtl">`, `<link rel="canonical" href="https://voters.example/candidates/dana-levi">`,
		`<link rel="stylesheet" href="/assets/site-2.css">`, `<script type="module" src="/assets/site-1.js">`,
		`1,234,567`, `4.6 / 4.57`, `data-history="[3,5,4,8]"`, `data-redirect-to="&#34;dana-levi&#34;"`, `<svg class="trend"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %s", want, body)
		}
	}
	if strings.Contains(body, `name="robots"`) || strings.Contains(w.Header().Get("X-Robots-Tag"), "noindex") {
		t.Error("an indexed page must not carry noindex")
	}
	// the JSON script block parses back to the handler's data, even with </script> in a name
	m := regexp.MustCompile(`(?s)<script type="application/json" id="candidate-data">(.*?)</script>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no data block in %s", body)
	}
	var got struct{ Name string }
	if err := json.Unmarshal([]byte(m[1]), &got); err != nil || got.Name != `דנה "לוי" </script>` {
		t.Errorf("data block: %v %q", err, m[1])
	}

	if w := serve(site, "/candidates/new-one"); w.Code != 200 || !strings.Contains(w.Body.String(), `<meta name="robots" content="noindex">`) ||
		w.Header().Get("X-Robots-Tag") != "noindex" {
		t.Errorf("noindex page: %d %v", w.Code, w.Header())
	}
	if w := serve(site, "/candidates/old-dana"); w.Code != 301 || w.Header().Get("Location") != "/candidates/dana-levi" {
		t.Errorf("redirect: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := serve(site, "/candidates/evil"); w.Code != 404 {
		t.Errorf("a stored target that is not a valid slug must not redirect: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := serve(site, "/candidates/removed"); w.Code != 410 || !strings.Contains(w.Body.String(), "<h1>410</h1>") {
		t.Errorf("gone: %d %s", w.Code, w.Body)
	}
	if w := serve(site, "/candidates/nobody"); w.Code != 404 || !strings.Contains(w.Body.String(), "<h1>404</h1>") {
		t.Errorf("missing: %d %s", w.Code, w.Body)
	}
	if w := serve(site, "/candidates"); w.Code != 200 || !strings.Contains(w.Body.String(), "1,234,567") {
		t.Errorf("list: %d %s", w.Code, w.Body)
	}
}

// search.json from your own mux in front of the Site.
// The server's /sitemap.xml lists database rows with lastmod from SitemapURLs.
func TestGuideSitemapLastMod(t *testing.T) {
	site, _ := newPilot(t, false)
	w := serve(site, "/sitemap.xml")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<lastmod>2026-11-01T10:00:00Z</lastmod>") ||
		strings.Count(w.Body.String(), "<lastmod>") != 1 {
		t.Errorf("sitemap: %d %s", w.Code, w.Body)
	}
}

func TestGuideSearchMux(t *testing.T) {
	site, db := newPilot(t, false)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /search.json", func(w http.ResponseWriter, r *http.Request) {
		type item struct {
			ID, Name string
		}
		var items []item
		for _, c := range db.byID {
			items = append(items, item{c.ID, c.Name})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_ = json.NewEncoder(w).Encode(items)
	})
	mux.Handle("/", site) // everything else is Hearth's
	if w := serve(mux, "/search.json"); w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "json") {
		t.Errorf("search.json: %d %v", w.Code, w.Header())
	}
	if w := serve(mux, "/candidates/dana-levi"); w.Code != 200 {
		t.Errorf("site behind the mux: %d", w.Code)
	}

	// the same file behind Site.Matches: it only sees paths that match no page
	inner, err := hearth.New(hearth.Options{
		Bundle: pilotBundle(), CacheDir: t.TempDir(), DefaultLang: "he", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Funcs: template.FuncMap{"num": num, "dec": dec, "trend": trend},
	})
	if err != nil {
		t.Fatal(err)
	}
	fb := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !inner.Matches(r) {
			fmt.Fprint(w, `[]`)
			return
		}
		inner.ServeHTTP(w, r)
	})
	if w := serve(fb, "/search.json"); w.Code != 200 || w.Body.String() != "[]" {
		t.Errorf("fallback: %d %q", w.Code, w.Body)
	}
}

// The Go program's `build` subcommand: hearth.Build writes static pages, the sitemap with lastmod
// and 404.html, and never a file for an ssr page or a database-backed route.
func TestGuideBuildSubcommand(t *testing.T) {
	site, _ := newPilot(t, true)
	out := filepath.Join(t.TempDir(), "public")
	if _, err := hearth.Build(context.Background(), site, out); err != nil {
		t.Fatal(err)
	}
	sm, err := os.ReadFile(filepath.Join(out, "sitemap.xml"))
	if err != nil || !strings.Contains(string(sm), "/candidates</loc>") || strings.Contains(string(sm), "dana-levi") {
		t.Errorf("sitemap: %v %s", err, sm)
	}
	if _, err := os.Stat(filepath.Join(out, "candidates", "dana-levi", "index.html")); err == nil {
		t.Error("a database-backed route must not be prerendered")
	}
	if _, err := os.Stat(filepath.Join(out, "404.html")); err != nil {
		t.Errorf("404.html: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "candidates", "index.html")); err == nil {
		t.Error("an ssr page must not be written by the build")
	}
}
