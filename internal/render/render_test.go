package render

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/orelhadad/hearth/internal/i18n"
	"github.com/orelhadad/hearth/internal/route"
)

func renderFS() fstest.MapFS {
	return fstest.MapFS{
		"layouts/base.html": {Data: []byte(`<!doctype html><html lang="{{.Lang}}" dir="{{.Dir}}"><head><title>{{block "title" .}}Default{{end}}</title>` +
			`<meta name="description" content="{{block "description" .}}{{end}}"><link rel="canonical" href="{{.Canonical}}">{{block "head" .}}{{end}}</head>` +
			`<body>{{block "body" .}}{{end}}</body></html>`)},
		"partials/foot.html":  {Data: []byte(`{{define "foot"}}<footer>{{t "hi"}}</footer>{{end}}`)},
		"pages/about.html":    {Data: []byte(`{{define "title"}}About{{end}}{{define "body"}}<h1>{{t "hi"}}</h1>{{template "foot" .}}{{end}}`)},
		"pages/about.he.html": {Data: []byte(`{{define "title"}}עלינו{{end}}{{define "body"}}<h1>HE override</h1>{{end}}`)},
		"pages/xss.html":      {Data: []byte(`{{define "body"}}<p>{{.Data}}</p>{{end}}`)},
		"pages/bad.html":      {Data: []byte(`{{define "body"}}{{t "nope"}}{{end}}`)},
		"locales/en.json":     {Data: []byte(`{"hi":"Hello"}`)},
		"locales/he.json":     {Data: []byte(`{"hi":"שלום"}`)},
	}
}

func newTestRenderer(t *testing.T) (*Renderer, *route.Table) {
	t.Helper()
	fsys := renderFS()
	routes, err := route.Discover(fsys, "pages")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := i18n.Load(fsys, "locales")
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(Options{FS: fsys, Routes: routes, Catalogs: cat})
	if err != nil {
		t.Fatal(err)
	}
	return r, routes
}

func TestRenderLayoutAndPartials(t *testing.T) {
	r, routes := newTestRenderer(t)
	pg, _ := routes.Lookup("about")
	out, err := r.Render(pg, PageData{Lang: "en", Canonical: "https://x.test/about"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"<title>About</title>", "<h1>Hello</h1>", "<footer>Hello</footer>", `href="https://x.test/about"`, `dir="ltr"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
}

func TestRenderLanguageOverride(t *testing.T) {
	r, routes := newTestRenderer(t)
	pg, _ := routes.Lookup("about")
	out, err := r.Render(pg, PageData{Lang: "he", Dir: "rtl"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "HE override") || !strings.Contains(string(out), `dir="rtl"`) {
		t.Fatalf("override not used: %s", out)
	}
}

func TestRenderEscapesAndFailsOnMissingKey(t *testing.T) {
	r, routes := newTestRenderer(t)
	pg, _ := routes.Lookup("xss")
	out, err := r.Render(pg, PageData{Lang: "en", Data: `<script>alert(1)</script>`})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "<script>") {
		t.Fatalf("data not escaped: %s", out)
	}
	bad, _ := routes.Lookup("bad")
	if _, err := r.Render(bad, PageData{Lang: "en"}); err == nil || !strings.Contains(err.Error(), "missing translation") {
		t.Fatalf("err = %v", err)
	}
}

func TestPageDataLink(t *testing.T) {
	d := PageData{Prefix: "/he"}
	if d.Link("/") != "/he" || d.Link("/about") != "/he/about" {
		t.Error("prefixed links wrong")
	}
	if (PageData{}).Link("/") != "/" || (PageData{}).Link("/a") != "/a" {
		t.Error("default links wrong")
	}
}

func TestRenderRobotsBlockAndStatus(t *testing.T) {
	fsys := fstest.MapFS{
		"layouts/base.html": {Data: []byte(`{{if .NoIndex}}<meta name="robots" content="noindex">{{end}}[{{.Status}}]{{block "body" .}}{{end}}`)},
		"pages/a.html":      {Data: []byte(`{{define "robots"}} noindex {{end}}{{define "body"}}a{{end}}`)},
		"pages/b.html":      {Data: []byte(`{{define "body"}}b{{end}}`)},
		"pages/c.html":      {Data: []byte(`{{define "robots"}}{{if .NoIndex}}noindex{{end}}{{end}}{{define "body"}}c{{end}}`)},
		"pages/d.html":      {Data: []byte(`{{define "robots"}}follow{{end}}{{define "body"}}d{{end}}`)},
	}
	routes, err := route.Discover(fsys, "pages")
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(Options{FS: fsys, Routes: routes})
	if err != nil {
		t.Fatal(err)
	}
	render := func(name string, d PageData) (string, error) {
		pg, _ := routes.Lookup(name)
		b, err := r.Render(pg, d)
		return string(b), err
	}
	const tag = `<meta name="robots" content="noindex">`
	if out, err := render("a", PageData{Lang: "en"}); err != nil || out != tag+"[200]a" {
		t.Errorf("a = %q, %v", out, err)
	}
	if out, _ := render("a", PageData{Lang: "en", NoIndex: true}); strings.Count(out, tag) != 1 {
		t.Errorf("handler and template both set noindex: %q", out)
	}
	if out, _ := render("b", PageData{Lang: "en"}); out != "[200]b" {
		t.Errorf("b = %q", out)
	}
	if out, _ := render("b", PageData{Lang: "en", NoIndex: true, Status: 404}); out != tag+"[404]b" {
		t.Errorf("b noindex = %q", out)
	}
	if out, _ := render("c", PageData{Lang: "en"}); out != "[200]c" {
		t.Errorf("c = %q", out)
	}
	if _, err := render("d", PageData{Lang: "en"}); err == nil || !strings.Contains(err.Error(), "must produce") {
		t.Errorf("d: %v", err)
	}
}
