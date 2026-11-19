package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orelhadad/hearth/internal/config"
	"github.com/orelhadad/hearth/internal/testutil"
)

func ssrSite() map[string]string {
	f := testutil.BaseSite()
	f["hearth.config.json"] = `{"baseURL":"https://x.test","ssr":["/candidates"],"collections":{"post/{id}":{"data":"posts","param":"id"}}}`
	f["pages/candidates.html"] = `{{define "body"}}{{range .Data.names}}<b>{{.}}</b>{{end}}{{end}}`
	f["pages/candidates.mock.json"] = `{"names":["Ann","Bo"]}`
	return f
}

func TestBuildSkipsSSRPagesButKeepsThemInTheSitemap(t *testing.T) {
	f := ssrSite()
	site := testutil.WriteSite(t, f)
	cfg, err := config.Load(site)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(Options{SiteDir: site, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	pub := filepath.Join(site, "dist", "public")
	for _, p := range []string{"candidates/index.html", "he/candidates/index.html"} {
		if _, err := os.Stat(filepath.Join(pub, p)); err == nil {
			t.Errorf("%s was written; it would shadow the handler", p)
		}
	}
	for _, p := range rep.Pages {
		if strings.Contains(p, "candidates") {
			t.Errorf("report lists %s", p)
		}
	}
	sm := testutil.Read(t, pub, "sitemap.xml")
	for _, want := range []string{"<loc>https://x.test/candidates</loc>", "<loc>https://x.test/he/candidates</loc>"} {
		if !strings.Contains(sm, want) {
			t.Errorf("sitemap lacks %s:\n%s", want, sm)
		}
	}
	var m struct {
		Format int
		SSR    []string
	}
	if err := json.Unmarshal([]byte(testutil.Read(t, site, "dist", "manifest.json")), &m); err != nil {
		t.Fatal(err)
	}
	if m.Format != 3 || len(m.SSR) != 1 || m.SSR[0] != "/candidates" {
		t.Errorf("manifest = %+v", m)
	}
	// the bundle carries the template, so a server can render it
	if _, err := os.Stat(filepath.Join(site, "dist", "templates", "pages", "candidates.html")); err != nil {
		t.Error(err)
	}
}

func TestUnknownSSREntryFailsCheckAndBuild(t *testing.T) {
	for name, entry := range map[string]string{"unknown": "/nope", "param": "/post/{id}"} {
		f := testutil.BaseSite()
		f["hearth.config.json"] = `{"ssr":["` + entry + `"]}`
		site := testutil.WriteSite(t, f)
		cfg, err := config.Load(site)
		if name == "param" {
			if err == nil {
				t.Error("config should reject a parameter in ssr")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		errs := Check(site, cfg)
		if len(errs) == 0 || !strings.Contains(errs[0].Error(), `ssr entry "/nope"`) {
			t.Errorf("%s: check = %v", name, errs)
		}
		if _, err := Run(Options{SiteDir: site, Config: cfg}); err == nil || !strings.Contains(err.Error(), "ssr entry") {
			t.Errorf("%s: build = %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(site, "dist")); err == nil {
			t.Error("a failed ssr validation must not touch dist/")
		}
	}
}

func TestSSRPageWithRouteParamsInConfigIsRejectedByCheck(t *testing.T) {
	f := testutil.BaseSite()
	site := testutil.WriteSite(t, f)
	cfg := mustCfg(t, f)
	cfg.SSR = []string{"/post/{id}"} // bypasses config.Load, which already rejects it
	if errs := Check(site, cfg); len(errs) == 0 || !strings.Contains(errs[0].Error(), "route parameters") {
		t.Errorf("check = %v", errs)
	}
}

func TestCheckRendersSSRPagesFromMock(t *testing.T) {
	f := ssrSite()
	site := testutil.WriteSite(t, f)
	if errs := Check(site, mustCfg(t, f)); len(errs) != 0 {
		t.Fatalf("clean ssr site reported: %v", errs)
	}
	// a template that reads a key the mock lacks is caught
	f["pages/candidates.mock.json"] = `{"other":1}`
	site = testutil.WriteSite(t, f)
	if errs := Check(site, mustCfg(t, f)); len(errs) == 0 || !strings.Contains(errs[0].Error(), "/candidates") {
		t.Errorf("bad mock not caught: %v", errs)
	}
	// no mock at all is reported with the file to add
	delete(f, "pages/candidates.mock.json")
	site = testutil.WriteSite(t, f)
	errs := Check(site, mustCfg(t, f))
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "candidates.mock.json") {
		t.Errorf("missing mock not reported: %v", errs)
	}
	// invalid JSON in the mock
	f["pages/candidates.mock.json"] = `{`
	site = testutil.WriteSite(t, f)
	found := false
	for _, e := range Check(site, mustCfg(t, f)) {
		found = found || strings.Contains(e.Error(), "not valid JSON")
	}
	if !found {
		t.Error("invalid mock JSON not reported")
	}
}

func TestStubsLetCheckParseTemplatesThatCallGoFuncs(t *testing.T) {
	f := testutil.BaseSite()
	f["hearth.config.json"] = `{"funcs":["num"]}`
	f["pages/about.html"] = `{{define "body"}}{{num 1234}}{{end}}`
	delete(f, "pages/post/[id].html")
	delete(f, "pages/post/[id].mock.json")
	delete(f, "data/posts.json")
	site := testutil.WriteSite(t, f)
	errs := Check(site, mustCfg(t, f))
	if len(errs) == 0 {
		t.Fatal("a page that calls a stubbed func must be reported")
	}
	for _, e := range errs {
		if !NeedsProgram(e) || !strings.Contains(e.Error(), `"num"`) || !strings.Contains(e.Error(), "/about") {
			t.Errorf("unexpected problem: %v", e)
		}
	}
	// without the name in config, the template does not parse
	f["hearth.config.json"] = `{}`
	site = testutil.WriteSite(t, f)
	errs = Check(site, mustCfg(t, f))
	if len(errs) == 0 || NeedsProgram(errs[0]) || !strings.Contains(errs[0].Error(), "num") {
		t.Errorf("undeclared func: %v", errs)
	}
	// a page that declares the name but does not call it is fine
	f["hearth.config.json"] = `{"funcs":["num"]}`
	f["pages/about.html"] = `{{define "body"}}{{if false}}{{num 1}}{{end}}about{{end}}`
	site = testutil.WriteSite(t, f)
	if errs := Check(site, mustCfg(t, f)); len(errs) != 0 {
		t.Errorf("uncalled stub reported: %v", errs)
	}
}
