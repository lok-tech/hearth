package build

import (
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
	f["layouts/base.html"] = `<link rel="stylesheet" href="{{asset "src/entry.ts.css"}}"><script type="module" src="{{asset "src/entry.ts"}}"></script>{{block "body" .}}{{end}}`
	site := testutil.WriteSite(t, f)
	cfg := &config.Config{DefaultLang: "en", AssetBase: "https://cdn.test", I18n: config.I18nConfig{Mode: "prefix"},
		Collections: map[string]config.Collection{"post/{id}": {Data: "posts", Param: "id"}}}
	// $1 = outDir; the args after it must include --base https://cdn.test/
	script := `case "$*" in *"--base https://cdn.test/"*) ;; *) echo "no --base in: $*" >&2; exit 3;; esac
mkdir -p "$1/.vite" && echo '{"src/entry.ts":{"file":"assets/entry-ab.js","css":["assets/entry-cd.css"]}}' > "$1/.vite/manifest.json"`
	cfg.Vite = &config.ViteConfig{Command: []string{"/bin/sh", "-c", script}}
	if _, err := Run(Options{SiteDir: site, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	html := testutil.Read(t, site, "dist", "public", "index.html")
	if !strings.Contains(html, `href="https://cdn.test/assets/entry-cd.css"`) || !strings.Contains(html, `src="https://cdn.test/assets/entry-ab.js"`) {
		t.Errorf("assets not on CDN: %s", html)
	}
	m, err := manifest.Parse([]byte(testutil.Read(t, site, "dist", "templates", "manifest.json")))
	if err != nil || m.Assets["src/entry.ts"] != "https://cdn.test/assets/entry-ab.js" {
		t.Errorf("bundle manifest: %v %v", m, err)
	}
}
