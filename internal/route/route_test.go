package route

import (
	"testing"
	"testing/fstest"
)

func testFS() fstest.MapFS {
	f := func() *fstest.MapFile { return &fstest.MapFile{Data: []byte("x")} }
	return fstest.MapFS{
		"pages/index.html":          f(),
		"pages/about.html":          f(),
		"pages/about.he.html":       f(),
		"pages/post/[id].html":      f(),
		"pages/post/[id].mock.json": f(),
		"pages/post/new.html":       f(),
		"pages/blog/index.html":     f(),
	}
}

func TestDiscoverRoutes(t *testing.T) {
	r, err := Discover(testFS(), "pages")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"", "about", "blog", "post/new", "post/{id}"}
	got := r.Patterns()
	if len(got) != len(want) {
		t.Fatalf("patterns = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("patterns = %v, want %v", got, want)
		}
	}
	about, _ := r.Lookup("about")
	if about.Langs["he"] != "pages/about.he.html" {
		t.Errorf("langs = %v", about.Langs)
	}
	post, _ := r.Lookup("/post/{id}")
	if len(post.Params) != 1 || post.Params[0] != "id" || !post.IsDynamic() {
		t.Errorf("post = %+v", post)
	}
}

func TestMatchStaticBeatsParam(t *testing.T) {
	r, _ := Discover(testFS(), "pages")
	pg, params, ok := r.Match("/post/new")
	if !ok || pg.Pattern != "post/new" || len(params) != 0 {
		t.Fatalf("got %+v %v %v", pg, params, ok)
	}
	pg, params, ok = r.Match("/post/42")
	if !ok || pg.Pattern != "post/{id}" || params["id"] != "42" {
		t.Fatalf("got %+v %v %v", pg, params, ok)
	}
	if pg, _, ok := r.Match("/"); !ok || pg.Pattern != "" {
		t.Fatal("index should match /")
	}
	if _, _, ok := r.Match("/nope"); ok {
		t.Fatal("/nope should not match")
	}
}

func TestCheckHandlerTypo(t *testing.T) {
	r, _ := Discover(testFS(), "pages")
	if err := r.CheckHandler("post/{id}"); err != nil {
		t.Fatal(err)
	}
	if err := r.CheckHandler("post/{slug}"); err == nil {
		t.Fatal("typo should fail")
	}
}

func TestBadFiles(t *testing.T) {
	f := &fstest.MapFile{Data: []byte("x")}
	for _, name := range []string{"pages/[id.html", "pages/[a]/[a].html", "pages/he.html.html"} {
		_ = name
	}
	if _, err := Discover(fstest.MapFS{"pages/[id.html": f}, "pages"); err == nil {
		t.Error("malformed param should fail")
	}
	if _, err := Discover(fstest.MapFS{"pages/[a]/[a].html": f}, "pages"); err == nil {
		t.Error("repeated param should fail")
	}
	if _, err := Discover(fstest.MapFS{"pages/x.he.html": f}, "pages"); err == nil {
		t.Error("orphan override should fail")
	}
}

func TestErrorTemplatesAreNotRoutes(t *testing.T) {
	f := func() *fstest.MapFile { return &fstest.MapFile{Data: []byte("x")} }
	fsys := testFS()
	fsys["pages/404.html"] = f()
	fsys["pages/404.he.html"] = f()
	fsys["pages/410.html"] = f()
	fsys["pages/docs/404.html"] = f() // in a folder: an ordinary page
	r, err := Discover(fsys, "pages")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range r.Patterns() {
		if p == "404" || p == "410" {
			t.Errorf("%q is a route", p)
		}
	}
	if _, ok := r.Lookup("docs/404"); !ok {
		t.Error("docs/404 should be a page")
	}
	if _, _, ok := r.Match("/404"); ok {
		t.Error("/404 matches a page")
	}
	p, ok := r.ErrorPage(404)
	if !ok || p.File != "pages/404.html" || p.Langs["he"] != "pages/404.he.html" {
		t.Errorf("404 = %+v", p)
	}
	if p, ok := r.ErrorPage(410); !ok || p.File != "pages/410.html" {
		t.Errorf("410 = %+v", p)
	}
	delete(fsys, "pages/404.html")
	if _, err := Discover(fsys, "pages"); err == nil {
		t.Error("404.he.html without 404.html should fail like any override")
	}
}
