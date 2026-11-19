package manifest

import (
	"fmt"
	"html/template"
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
	pic, err := m.Picture("hero", `a "quote"`, "", true)
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

// legacyPicture is the Picture implementation from before responsive widths, kept as a reference so a
// single-size bundle is proven byte-identical.
func legacyPicture(files []ImageFile, alt string, lazy bool) string {
	esc := template.HTMLEscapeString
	var b strings.Builder
	b.WriteString("<picture>")
	last := files[len(files)-1]
	for _, f := range files[:len(files)-1] {
		fmt.Fprintf(&b, `<source type="%s" srcset="%s">`, esc(f.Type), esc(f.URL))
	}
	fmt.Fprintf(&b, `<img src="%s" alt="%s"`, esc(last.URL), esc(alt))
	if last.Width > 0 && last.Height > 0 {
		fmt.Fprintf(&b, ` width="%d" height="%d"`, last.Width, last.Height)
	}
	if lazy {
		b.WriteString(` loading="lazy" decoding="async"`)
	}
	b.WriteString("></picture>")
	return b.String()
}

func imgs() *Manifest {
	return &Manifest{Format: BundleFormat, Images: map[string][]ImageFile{
		"hero": {
			{URL: "/i/h-480.avif", Type: "image/avif", Width: 480, Height: 270},
			{URL: "/i/h-1600.avif", Type: "image/avif", Width: 1600, Height: 900},
			{URL: "/i/h-480.webp", Type: "image/webp", Width: 480, Height: 270},
			{URL: "/i/h-1600.webp", Type: "image/webp", Width: 1600, Height: 900},
			{URL: "/i/h-480.jpg", Type: "image/jpeg", Width: 480, Height: 270},
			{URL: "/i/h-1600.jpg", Type: "image/jpeg", Width: 1600, Height: 900},
		},
		"small": { // narrower than the smallest width: one size only
			{URL: "/i/s.avif", Type: "image/avif", Width: 300, Height: 100},
			{URL: "/i/s.jpg", Type: "image/jpeg", Width: 300, Height: 100},
		},
		"dark":  {{URL: "/i/d.webp", Type: "image/webp", Width: 40, Height: 40}, {URL: "/i/d.png", Type: "image/png", Width: 40, Height: 40}},
		"light": {{URL: "/i/l.webp", Type: "image/webp", Width: 40, Height: 40}, {URL: "/i/l.png", Type: "image/png", Width: 40, Height: 40}},
	}}
}

func TestPictureSingleWidthIsByteIdentical(t *testing.T) {
	fixtures := [][]ImageFile{
		{{URL: "/i/h.avif", Type: "image/avif"}, {URL: "/i/h.jpg", Type: "image/jpeg", Width: 800, Height: 600}},
		{{URL: "/i/h.avif", Type: "image/avif", Width: 800, Height: 600}, {URL: "/i/h.webp", Type: "image/webp", Width: 800, Height: 600}, {URL: "/i/h.png", Type: "image/png", Width: 800, Height: 600}},
		{{URL: `/i/a"b&c.jpg`, Type: "image/jpeg", Width: 5, Height: 5}},
	}
	for i, files := range fixtures {
		m := &Manifest{Images: map[string][]ImageFile{"x": files}}
		for _, lazy := range []bool{true, false} {
			got, err := m.Picture("x", `a <"b">`, "", lazy)
			if err != nil || string(got) != legacyPicture(files, `a <"b">`, lazy) {
				t.Errorf("fixture %d lazy=%v:\n%s\nwant\n%s\n%v", i, lazy, got, legacyPicture(files, `a <"b">`, lazy), err)
			}
		}
		// sizes is ignored when no srcset carries widths
		got, _ := m.PictureArgs("x", "A", "50vw")
		if want, _ := m.Picture("x", "A", "", true); got != want {
			t.Errorf("fixture %d: sizes changed single-width output", i)
		}
	}
}

func TestPictureSeveralWidths(t *testing.T) {
	got, err := imgs().PictureArgs("hero", "Alt")
	want := `<picture><source type="image/avif" srcset="/i/h-480.avif 480w, /i/h-1600.avif 1600w" sizes="100vw">` +
		`<source type="image/webp" srcset="/i/h-480.webp 480w, /i/h-1600.webp 1600w" sizes="100vw">` +
		`<img src="/i/h-1600.jpg" srcset="/i/h-480.jpg 480w, /i/h-1600.jpg 1600w" sizes="100vw" alt="Alt" width="1600" height="900" loading="lazy" decoding="async"></picture>`
	if err != nil || string(got) != want {
		t.Fatalf("%s\n%v", got, err)
	}
	got, _ = imgs().PictureArgs("hero", "Alt", "(min-width: 60rem) 50vw, 100vw")
	if strings.Count(string(got), `sizes="(min-width: 60rem) 50vw, 100vw"`) != 3 || strings.Contains(string(got), "100vw\" ") && strings.Contains(string(got), `sizes="100vw"`) {
		t.Errorf("sizes: %s", got)
	}
	// widths given out of order are sorted
	m := imgs()
	h := m.Images["hero"]
	h[0], h[1] = h[1], h[0]
	got2, _ := m.PictureArgs("hero", "Alt")
	if string(got2) != want {
		t.Errorf("unsorted input changed output:\n%s", got2)
	}
}

func TestPictureSmallSourceHasASingleSize(t *testing.T) {
	got, err := imgs().PictureArgs("small", "s", "50vw")
	want := `<picture><source type="image/avif" srcset="/i/s.avif"><img src="/i/s.jpg" alt="s" width="300" height="100" loading="lazy" decoding="async"></picture>`
	if err != nil || string(got) != want {
		t.Fatalf("%s %v", got, err)
	}
}

func TestPictureEscapesSizesAndAlt(t *testing.T) {
	got, _ := imgs().PictureArgs("hero", `x"y`, `a"><script>`)
	if strings.Contains(string(got), `"><script>`) || strings.Contains(string(got), `x"y`) {
		t.Errorf("unescaped: %s", got)
	}
}

func TestPictureEagerAndArgCounts(t *testing.T) {
	got, err := imgs().PictureArgs("small", "s", "eager")
	if err != nil || strings.Contains(string(got), "loading=") || strings.Contains(string(got), "decoding=") {
		t.Errorf("eager: %s %v", got, err)
	}
	got, err = imgs().PictureArgs("hero", "s", "50vw", "eager")
	if err != nil || strings.Contains(string(got), "loading=") || !strings.Contains(string(got), `sizes="50vw"`) {
		t.Errorf("sizes+eager: %s %v", got, err)
	}
	for _, args := range [][]string{{}, {"hero"}, {"hero", "a", "50vw", "x"}, {"hero", "a", "50vw", "eager", "eager"}, {"hero", "a", ""}, {"hero", "a", "", "eager"}} {
		if _, err := imgs().PictureArgs(args...); err == nil {
			t.Errorf("%q must fail", args)
		}
	}
	if _, err := imgs().PictureArgs("nope", "a"); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("missing image: %v", err)
	}
	l := imgs()
	l.Lenient = true
	if got, err := l.PictureArgs("nope", "a"); err != nil || !strings.Contains(string(got), `src="/nope"`) || strings.Contains(string(got), "<source") {
		t.Errorf("lenient placeholder: %s %v", got, err)
	}
}

func TestPictureSet(t *testing.T) {
	got, err := imgs().PictureSetArgs("Logo", "", "(prefers-color-scheme: dark)", "dark", "", "light")
	want := `<picture>` +
		`<source media="(prefers-color-scheme: dark)" type="image/webp" srcset="/i/d.webp">` +
		`<source media="(prefers-color-scheme: dark)" type="image/png" srcset="/i/d.png">` +
		`<source type="image/webp" srcset="/i/l.webp">` +
		`<img src="/i/l.png" alt="Logo" width="40" height="40" loading="lazy" decoding="async"></picture>`
	if err != nil || string(got) != want {
		t.Fatalf("%s\n%v", got, err)
	}
	got, _ = imgs().PictureSetArgs("L", "", "(a)", "dark", "", "light", "eager")
	if strings.Contains(string(got), "loading=") || !strings.Contains(string(got), `media="(a)"`) {
		t.Errorf("eager: %s", got)
	}
}

func TestPictureSetWidthsAndSizes(t *testing.T) {
	got, err := imgs().PictureSetArgs("Hero", "(min-width: 60rem) 50vw, 100vw", "(max-width: 40rem)", "hero", "", "hero")
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `<source media="(max-width: 40rem)" type="image/jpeg" srcset="/i/h-480.jpg 480w, /i/h-1600.jpg 1600w" sizes="(min-width: 60rem) 50vw, 100vw">`) ||
		strings.Count(s, "<source") != 5 || !strings.Contains(s, `<img src="/i/h-1600.jpg" srcset=`) {
		t.Errorf("%s", s)
	}
	got, _ = imgs().PictureSetArgs("Hero", "", "(m)", "hero", "", "hero")
	if strings.Count(string(got), `sizes="100vw"`) != 6 {
		t.Errorf("default sizes: %s", got)
	}
	// an image named "eager" as the fallback is a name, not the option
	m := imgs()
	m.Images["eager"] = m.Images["small"]
	got, err = m.PictureSetArgs("A", "", "(m)", "small", "", "eager")
	if err != nil || !strings.Contains(string(got), `loading="lazy"`) || !strings.Contains(string(got), "/i/s.jpg") {
		t.Errorf("image named eager: %s %v", got, err)
	}
}

func TestPictureSetErrorsAndMediaEscaping(t *testing.T) {
	bad := map[string][]string{
		"no args":         {},
		"alt only":        {"a"},
		"no pairs":        {"a", ""},
		"no fallback":     {"a", "", "(m)", "dark"},
		"odd":             {"a", "", "(m)", "dark", "", "light", "x"},
		"missing name":    {"a", "", "(m)", "nope", "", "light"},
		"missing fb":      {"a", "", "(m)", "dark", "", "nope"},
		"quote in media":  {"a", "", `(m)" onload="x`, "dark", "", "light"},
		"newline":         {"a", "", "(m)\n", "dark", "", "light"},
		"tab":             {"a", "", "(m)\t", "dark", "", "light"},
		"empty mid media": {"a", "", "", "dark", "", "light"},
		"media on last":   {"a", "", "(m)", "dark", "(n)", "light"},
	}
	for n, args := range bad {
		if _, err := imgs().PictureSetArgs(args...); err == nil {
			t.Errorf("%s must fail", n)
		}
	}
	got, err := imgs().PictureSetArgs("a", "", "(min-width: 1px) and (a<b) & x", "dark", "", "light")
	if err != nil || !strings.Contains(string(got), `media="(min-width: 1px) and (a&lt;b) &amp; x"`) {
		t.Errorf("media not escaped: %s %v", got, err)
	}
	// lenient: missing names become placeholders
	l := imgs()
	l.Lenient = true
	if got, err := l.PictureSetArgs("a", "", "(m)", "nope", "", "nope2"); err != nil || !strings.Contains(string(got), `<source media="(m)" srcset="/nope">`) || !strings.Contains(string(got), `src="/nope2"`) {
		t.Errorf("lenient: %s %v", got, err)
	}
	// still an error for bad media in lenient mode
	if _, err := l.PictureSetArgs("a", "", "(m)\x00", "nope", "", "nope2"); err == nil {
		t.Error("lenient must still validate media")
	}
}

func TestSeveralRenditionsNeedWidths(t *testing.T) {
	m := &Manifest{Images: map[string][]ImageFile{"x": {{URL: "/a.jpg", Type: "image/jpeg"}, {URL: "/b.jpg", Type: "image/jpeg"}}}}
	if _, err := m.Picture("x", "a", "", true); err == nil {
		t.Error("renditions without widths must fail")
	}
}
