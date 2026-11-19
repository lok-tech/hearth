package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orelhadad/hearth/internal/config"
	"github.com/orelhadad/hearth/internal/manifest"

	"github.com/orelhadad/hearth/internal/testutil"
)

func TestBuildPrerendersStaticAndCollectionPages(t *testing.T) {
	site := testutil.WriteSite(t, testutil.BaseSite())
	rep, err := Run(Options{SiteDir: site})
	if err != nil {
		t.Fatal(err)
	}
	pub := filepath.Join(site, "dist", "public")
	if s := testutil.Read(t, pub, "index.html"); !strings.Contains(s, "Hello") || !strings.Contains(s, `hreflang="he" href="https://x.test/he"`) {
		t.Errorf("index.html: %s", s)
	}
	if s := testutil.Read(t, pub, "he", "index.html"); !strings.Contains(s, "שלום") || !strings.Contains(s, `dir="rtl"`) || !strings.Contains(s, `href="https://x.test/he"`) {
		t.Errorf("he index: %s", s)
	}
	if s := testutil.Read(t, pub, "post", "1", "index.html"); !strings.Contains(s, "First #1") {
		t.Errorf("post 1: %s", s)
	}
	if s := testutil.Read(t, pub, "he", "post", "two", "index.html"); !strings.Contains(s, "Second #two") {
		t.Errorf("he post two: %s", s)
	}
	if testutil.Read(t, pub, "favicon.txt") != "fav" {
		t.Error("public/ not copied")
	}
	if s := testutil.Read(t, pub, "sitemap.xml"); !strings.Contains(s, "<loc>https://x.test/post/1</loc>") || !strings.Contains(s, "<loc>https://x.test/he/about</loc>") {
		t.Errorf("sitemap: %s", s)
	}
	if !strings.Contains(testutil.Read(t, pub, "robots.txt"), "Sitemap: https://x.test/sitemap.xml") {
		t.Error("robots")
	}
	if len(rep.Pages) != 8 {
		t.Errorf("pages = %v", rep.Pages)
	}
	// bundle for the Go server is self-contained and drops mock files
	tpl := filepath.Join(site, "dist", "templates")
	if _, err := os.Stat(filepath.Join(tpl, "pages", "post", "[id].mock.json")); err == nil {
		t.Error("mock json leaked into bundle")
	}
	if _, err := manifest.Parse([]byte(testutil.Read(t, tpl, "manifest.json"))); err != nil {
		t.Errorf("bundle manifest: %v", err)
	}
	if _, err := manifest.Parse([]byte(testutil.Read(t, site, "dist", "manifest.json"))); err != nil {
		t.Errorf("manifest: %v", err)
	}
}

func TestBuildFailsOnMissingTranslation(t *testing.T) {
	f := testutil.BaseSite()
	f["locales/he.json"] = `{}`
	if _, err := Run(Options{SiteDir: testutil.WriteSite(t, f)}); err == nil || !strings.Contains(err.Error(), "missing translation") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildRejectsBadCollectionData(t *testing.T) {
	for name, data := range map[string]string{
		"dup":       `[{"id":1,"title":"a"},{"id":1,"title":"b"}]`,
		"traversal": `[{"id":"../x","title":"a"}]`,
		"slash":     `[{"id":"a/b","title":"a"}]`,
		"missing":   `[{"title":"a"}]`,
		"notarray":  `{"id":1}`,
	} {
		f := testutil.BaseSite()
		f["data/posts.json"] = data
		if _, err := Run(Options{SiteDir: testutil.WriteSite(t, f)}); err == nil {
			t.Errorf("%s: should fail", name)
		}
	}
}

func TestBuildUnknownCollectionPattern(t *testing.T) {
	f := testutil.BaseSite()
	f["hearth.config.json"] = `{"collections":{"nope/{id}":{"data":"posts","param":"id"}}}`
	if _, err := Run(Options{SiteDir: testutil.WriteSite(t, f)}); err == nil {
		t.Fatal("unknown collection pattern must fail")
	}
}

func TestBuildRefusesDangerousOutDir(t *testing.T) {
	site := testutil.WriteSite(t, testutil.BaseSite())
	for _, out := range []string{site, filepath.Dir(site), "/"} {
		if _, err := Run(Options{SiteDir: site, OutDir: out}); err == nil {
			t.Errorf("out %q should be refused", out)
		}
	}
	if _, err := os.Stat(filepath.Join(site, "pages", "index.html")); err != nil {
		t.Fatal("source files were touched")
	}
}

func TestBuildSubdomainI18n(t *testing.T) {
	f := testutil.BaseSite()
	f["hearth.config.json"] = `{"baseURL":"https://x.test","i18n":{"mode":"subdomain","hosts":{"en":"x.test","he":"he.x.test"}},"collections":{"post/{id}":{"data":"posts","param":"id"}}}`
	site := testutil.WriteSite(t, f)
	if _, err := Run(Options{SiteDir: site}); err != nil {
		t.Fatal(err)
	}
	pub := filepath.Join(site, "dist", "public")
	if s := testutil.Read(t, pub, "he.x.test", "about", "index.html"); !strings.Contains(s, "about") {
		t.Error("host dir missing")
	}
	if s := testutil.Read(t, pub, "he.x.test", "index.html"); !strings.Contains(s, `href="https://he.x.test/"`) {
		t.Errorf("canonical: %s", s)
	}
}

func TestConvertViteManifest(t *testing.T) {
	m, err := ConvertViteManifest([]byte(`{"src/entry.ts":{"file":"assets/entry-ab12.js","src":"src/entry.ts","isEntry":true,"css":["assets/entry-cd34.css"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Assets["src/entry.ts"] != "/assets/entry-ab12.js" || m.Assets["src/entry.ts.css"] != "/assets/entry-cd34.css" {
		t.Fatalf("%v", m.Assets)
	}
}

// With assetBase set, Vite gets --base and every manifest URL points at the CDN.
func TestBuildWithAssetBase(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	f := testutil.BaseSite()
	f["layouts/base.html"] = `{{entry "src/entry.ts"}}<link rel="icon" href="{{asset "src/entry.ts.css"}}">{{block "body" .}}{{end}}`
	site := testutil.WriteSite(t, f)
	cfg := &config.Config{DefaultLang: "en", AssetBase: "https://cdn.test", I18n: config.I18nConfig{Mode: "prefix"},
		Collections: map[string]config.Collection{"post/{id}": {Data: "posts", Param: "id"}}}
	// $1 = outDir; the args after it must include --base https://cdn.test/
	script := `case "$*" in *"--base https://cdn.test/"*) ;; *) echo "no --base in: $*" >&2; exit 3;; esac
mkdir -p "$1/.vite" && echo '{"src/entry.ts":{"file":"assets/entry-ab.js","isEntry":true,"css":["assets/entry-cd.css"]}}' > "$1/.vite/manifest.json"`
	cfg.Vite = &config.ViteConfig{Command: []string{"/bin/sh", "-c", script}}
	if _, err := Run(Options{SiteDir: site, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	html := testutil.Read(t, site, "dist", "public", "index.html")
	if !strings.Contains(html, `<link rel="stylesheet" href="https://cdn.test/assets/entry-cd.css">`) || !strings.Contains(html, `src="https://cdn.test/assets/entry-ab.js"`) {
		t.Errorf("assets not on CDN: %s", html)
	}
	m, err := manifest.Parse([]byte(testutil.Read(t, site, "dist", "templates", "manifest.json")))
	if err != nil || m.Assets["src/entry.ts"] != "https://cdn.test/assets/entry-ab.js" || m.Format != 3 ||
		m.Entries["src/entry.ts"].JS != "https://cdn.test/assets/entry-ab.js" || m.Entries["src/entry.ts"].CSS[0] != "https://cdn.test/assets/entry-cd.css" {
		t.Errorf("bundle manifest: %v %v", m, err)
	}
}

func TestBuildWritesBrandedErrorPagesAndKeepsThemOutOfRoutes(t *testing.T) {
	f := testutil.BaseSite()
	f["pages/404.html"] = `{{define "body"}}missing {{.Status}}{{end}}`
	f["pages/404.he.html"] = `{{define "body"}}חסר {{.Status}}{{end}}`
	f["pages/410.html"] = `{{define "body"}}gone{{end}}`
	site := testutil.WriteSite(t, f)
	rep, err := Run(Options{SiteDir: site})
	if err != nil {
		t.Fatal(err)
	}
	pub := filepath.Join(site, "dist", "public")
	if s := testutil.Read(t, pub, "404.html"); !strings.Contains(s, "missing 404") || !strings.Contains(s, `lang="en"`) {
		t.Errorf("404.html: %s", s)
	}
	if s := testutil.Read(t, pub, "he", "404.html"); !strings.Contains(s, "חסר 404") || !strings.Contains(s, `dir="rtl"`) {
		t.Errorf("he/404.html: %s", s)
	}
	for _, p := range rep.Pages {
		if strings.Contains(p, "404") || strings.Contains(p, "410") {
			t.Errorf("error template prerendered: %s", p)
		}
	}
	if len(rep.Pages) != 8 {
		t.Errorf("pages = %v", rep.Pages)
	}
	if strings.Contains(testutil.Read(t, pub, "sitemap.xml"), "40") {
		t.Errorf("sitemap lists an error page: %s", testutil.Read(t, pub, "sitemap.xml"))
	}
	// the templates still ship in the bundle, for a server
	if _, err := os.Stat(filepath.Join(site, "dist", "templates", "pages", "404.html")); err != nil {
		t.Error(err)
	}
}

func TestBuildWithoutErrorTemplateWritesNo404File(t *testing.T) {
	site := testutil.WriteSite(t, testutil.BaseSite())
	if _, err := Run(Options{SiteDir: site}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(site, "dist", "public", "404.html")); err == nil {
		t.Error("404.html exists without a template")
	}
}

// A Vite manifest with a shared chunk that imports CSS, a chunk reached twice, an import cycle, a
// dynamic import, an asset and a CSS-only entry.
const viteFixture = `{
 "src/site.ts": {"file":"assets/site-aa.js","src":"src/site.ts","isEntry":true,"css":["assets/site-11.css"],
   "imports":["_shared-bb.js","_util-cc.js"],"dynamicImports":["src/lazy.ts"]},
 "src/game.ts": {"file":"assets/game-dd.js","src":"src/game.ts","isEntry":true,"css":["assets/game-22.css"],
   "imports":["_shared-bb.js"]},
 "_shared-bb.js": {"file":"assets/shared-bb.js","name":"shared","css":["assets/shared-33.css"],"imports":["_util-cc.js"]},
 "_util-cc.js": {"file":"assets/util-cc.js","name":"util","imports":["_shared-bb.js"],"css":["assets/util-44.css"]},
 "src/lazy.ts": {"file":"assets/lazy-ee.js","src":"src/lazy.ts","isDynamicEntry":true,"css":["assets/lazy-55.css"]},
 "src/theme.css": {"file":"assets/theme-ff.css","src":"src/theme.css","isEntry":true},
 "src/logo.svg": {"file":"assets/logo-gg.svg","src":"src/logo.svg"}
}`

func TestConvertViteManifestResolvesEntries(t *testing.T) {
	m, err := ConvertViteManifest([]byte(viteFixture))
	if err != nil {
		t.Fatal(err)
	}
	site := m.Entries["src/site.ts"]
	if site.JS != "/assets/site-aa.js" {
		t.Errorf("js = %q", site.JS)
	}
	// Imported chunks' CSS comes first (it runs first), the entry's own last; each file once.
	if got := strings.Join(site.CSS, " "); got != "/assets/util-44.css /assets/shared-33.css /assets/site-11.css" {
		t.Errorf("css = %s", got)
	}
	// Preload: static imports only, depth first, the cycle and the repeat listed once, no lazy chunk.
	if got := strings.Join(site.Preload, " "); got != "/assets/shared-bb.js /assets/util-cc.js" {
		t.Errorf("preload = %s", got)
	}
	game := m.Entries["src/game.ts"]
	if got := strings.Join(game.CSS, " "); got != "/assets/util-44.css /assets/shared-33.css /assets/game-22.css" {
		t.Errorf("game css = %s", got)
	}
	if got := strings.Join(game.Preload, " "); got != "/assets/shared-bb.js /assets/util-cc.js" {
		t.Errorf("game preload = %s", got)
	}
	if _, ok := m.Entries["src/lazy.ts"]; ok {
		t.Error("a dynamic entry is not a page entry")
	}
	if _, ok := m.Entries["_shared-bb.js"]; ok || m.Entries["src/logo.svg"].JS != "" {
		t.Error("chunks and assets are not entries")
	}
	theme := m.Entries["src/theme.css"]
	if theme.JS != "" || len(theme.CSS) != 1 || theme.CSS[0] != "/assets/theme-ff.css" {
		t.Errorf("css-only entry = %+v", theme)
	}
	// {{asset}} is unchanged.
	if m.Assets["src/site.ts"] != "/assets/site-aa.js" || m.Assets["src/site.ts.css"] != "/assets/site-11.css" || m.Assets["src/logo.svg"] != "/assets/logo-gg.svg" {
		t.Errorf("assets = %v", m.Assets)
	}
	// Stable: the converted manifest serialises identically every time.
	a, _ := json.Marshal(m)
	for i := 0; i < 20; i++ {
		m2, _ := ConvertViteManifest([]byte(viteFixture))
		if b, _ := json.Marshal(m2); string(a) != string(b) {
			t.Fatalf("unstable output:\n%s\n%s", a, b)
		}
	}
	out, err := m.Entry("src/site.ts")
	if err != nil || strings.Count(string(out), "<link rel=\"stylesheet\"") != 3 || strings.Count(string(out), "modulepreload") != 2 || strings.Count(string(out), "<script") != 1 {
		t.Errorf("helper output: %s %v", out, err)
	}
}

func TestConvertViteManifestMissingImport(t *testing.T) {
	_, err := ConvertViteManifest([]byte(`{"a.ts":{"file":"a.js","isEntry":true,"imports":["_gone.js"]}}`))
	if err == nil || !strings.Contains(err.Error(), "_gone.js") {
		t.Errorf("err = %v", err)
	}
}

// End to end: a layout with {{entry}} over a fake Vite run emits every CSS file and preload hint.
func TestBuildEmitsEntryTags(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	f := testutil.BaseSite()
	f["layouts/base.html"] = `<head>{{entry "src/site.ts"}}</head>{{block "body" .}}{{end}}`
	site := testutil.WriteSite(t, f)
	cfg := &config.Config{DefaultLang: "en", I18n: config.I18nConfig{Mode: "prefix"},
		Collections: map[string]config.Collection{"post/{id}": {Data: "posts", Param: "id"}}}
	fixture := filepath.Join(t.TempDir(), "vite-manifest.json")
	if err := os.WriteFile(fixture, []byte(viteFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Vite = &config.ViteConfig{Command: []string{"/bin/sh", "-c", `mkdir -p "$1/.vite" && cp '` + fixture + `' "$1/.vite/manifest.json"`}}
	if _, err := Run(Options{SiteDir: site, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	html := testutil.Read(t, site, "dist", "public", "index.html")
	for _, want := range []string{`href="/assets/util-44.css"`, `href="/assets/shared-33.css"`, `href="/assets/site-11.css"`,
		`<link rel="modulepreload" href="/assets/shared-bb.js">`, `<link rel="modulepreload" href="/assets/util-cc.js">`,
		`<script type="module" src="/assets/site-aa.js"></script>`} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %s:\n%s", want, html)
		}
	}
}
