package manifest

import (
	"strings"
	"testing"
)

func TestParseManifest(t *testing.T) {
	m, err := Parse([]byte(`{"format":3,"assets":{"entry.js":"/assets/entry-abc.js"},"images":{"hero":[{"url":"/i/h.avif","type":"image/avif"},{"url":"/i/h.jpg","type":"image/jpeg","width":800,"height":600}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if u, err := m.Asset("entry.js"); err != nil || u != "/assets/entry-abc.js" {
		t.Fatalf("asset = %q, %v", u, err)
	}
	if _, err := m.Asset("missing.js"); err == nil {
		t.Fatal("unknown asset must fail")
	}
	pic, err := m.Picture("hero", `a "quote"`, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`<source type="image/avif" srcset="/i/h.avif">`, `width="800" height="600"`, `alt="a &#34;quote&#34;"`, `loading="lazy"`} {
		if !strings.Contains(string(pic), s) {
			t.Errorf("picture missing %s: %s", s, pic)
		}
	}
}

func TestManifestFormatMismatch(t *testing.T) {
	if _, err := Parse([]byte(`{"format":99}`)); err == nil || !strings.Contains(err.Error(), "format") {
		t.Fatalf("err = %v", err)
	}
	if _, err := Parse([]byte(`{"format":3,"bogus":1}`)); err == nil {
		t.Fatal("unknown fields must fail")
	}
	if _, err := Parse([]byte(`nope`)); err == nil {
		t.Fatal("bad json must fail")
	}
}

func TestPrefixURLs(t *testing.T) {
	m := &Manifest{Assets: map[string]string{"a": "/assets/a.js"}, Images: map[string][]ImageFile{"h": {{URL: "/assets/img/h.jpg"}}}}
	m.PrefixURLs("https://cdn.test")
	if m.Assets["a"] != "https://cdn.test/assets/a.js" || m.Images["h"][0].URL != "https://cdn.test/assets/img/h.jpg" {
		t.Fatalf("%+v", m)
	}
}

func TestManifestCarriesTheSSRList(t *testing.T) {
	m, err := Parse([]byte(`{"format":3,"ssr":["/","/candidates"]}`))
	if err != nil || len(m.SSR) != 2 || m.SSR[1] != "/candidates" {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := Parse([]byte(`{"format":2,"ssr":["/"]}`)); err == nil {
		t.Error("format 1 bundles must fail loudly")
	}
}

func entryManifest() *Manifest {
	return &Manifest{Format: BundleFormat, Assets: map[string]string{}, Entries: map[string]Entry{
		"src/site.ts": {
			JS:      "/assets/site-1.js",
			CSS:     []string{"/assets/shared-2.css", "/assets/site-3.css"},
			Preload: []string{"/assets/shared-4.js"},
		},
		"src/theme.css": {CSS: []string{"/assets/theme-5.css"}, Preload: []string{}},
	}}
}

func TestEntryHelper(t *testing.T) {
	got, err := entryManifest().Entry("src/site.ts")
	if err != nil {
		t.Fatal(err)
	}
	want := `<link rel="stylesheet" href="/assets/shared-2.css">
<link rel="stylesheet" href="/assets/site-3.css">
<link rel="modulepreload" href="/assets/shared-4.js">
<script type="module" src="/assets/site-1.js"></script>`
	if string(got) != want {
		t.Errorf("entry:\n%s\nwant:\n%s", got, want)
	}
	css, err := entryManifest().Entry("src/theme.css")
	if err != nil || strings.Contains(string(css), "<script") || !strings.Contains(string(css), "theme-5.css") {
		t.Errorf("css-only entry: %q %v", css, err)
	}
	if _, err := entryManifest().Entry("src/nope.ts"); err == nil || !strings.Contains(err.Error(), "src/nope.ts") {
		t.Errorf("unknown entry must fail: %v", err)
	}
}

func TestEntryEscapesURLs(t *testing.T) {
	m := &Manifest{Entries: map[string]Entry{"e": {JS: `/a"><script>x</script>.js`, CSS: []string{`/b"c.css`}, Preload: []string{`/d&e.js`}}}}
	got, _ := m.Entry("e")
	for _, bad := range []string{`"><script>`, `b"c`} {
		if strings.Contains(string(got), bad) {
			t.Errorf("unescaped %q in %s", bad, got)
		}
	}
	if !strings.Contains(string(got), "d&amp;e.js") {
		t.Errorf("preload not escaped: %s", got)
	}
}

func TestEntryAssetBase(t *testing.T) {
	m := entryManifest()
	m.PrefixURLs("https://cdn.test")
	got, _ := m.Entry("src/site.ts")
	for _, want := range []string{`href="https://cdn.test/assets/shared-2.css"`, `href="https://cdn.test/assets/shared-4.js"`, `src="https://cdn.test/assets/site-1.js"`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if css, _ := m.Entry("src/theme.css"); strings.Contains(string(css), "script") || !strings.Contains(string(css), "https://cdn.test/assets/theme-5.css") {
		t.Errorf("css-only entry with base: %s", css)
	}
}

func TestEntryDevAndLenient(t *testing.T) {
	m := entryManifest()
	m.DevBase = "http://localhost:5173"
	got, err := m.Entry("src/site.ts")
	want := `<script type="module" src="http://localhost:5173/@vite/client"></script>
<script type="module" src="http://localhost:5173/src/site.ts"></script>`
	if err != nil || string(got) != want {
		t.Errorf("dev: %q %v", got, err)
	}
	if got, err := m.Entry("src/unknown.ts"); err != nil || !strings.Contains(string(got), "5173/src/unknown.ts") {
		t.Errorf("dev serves any entry: %q %v", got, err)
	}
	l := &Manifest{Lenient: true}
	if got, err := l.Entry("src/site.ts"); err != nil || string(got) != `<script type="module" src="/src/site.ts"></script>` {
		t.Errorf("lenient: %q %v", got, err)
	}
	// a known entry still renders fully in lenient mode
	l.Entries = entryManifest().Entries
	if got, _ := l.Entry("src/site.ts"); !strings.Contains(string(got), "modulepreload") {
		t.Errorf("lenient known entry: %s", got)
	}
}

func TestParseEntries(t *testing.T) {
	m, err := Parse([]byte(`{"format":3,"entries":{"a.ts":{"js":"/a.js","css":["/a.css"],"preload":[]}}}`))
	if err != nil || m.Entries["a.ts"].JS != "/a.js" {
		t.Fatalf("%v %v", m, err)
	}
}
