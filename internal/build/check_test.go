package build

import (
	"strings"
	"testing"

	"github.com/orelhadad/hearth/internal/config"
	"github.com/orelhadad/hearth/internal/testutil"
)

func TestCheckReportsAllProblems(t *testing.T) {
	if errs := Check(testutil.WriteSite(t, testutil.BaseSite()), mustCfg(t, testutil.BaseSite())); len(errs) != 0 {
		t.Fatalf("clean site reported: %v", errs)
	}
	f := testutil.BaseSite()
	f["locales/he.json"] = `{}`
	f["pages/about.html"] = `{{define "body"}}{{t "nope"}}{{end}}`
	f["pages/post/[id].mock.json"] = `[1]`
	errs := Check(testutil.WriteSite(t, f), mustCfg(t, f))
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	for _, want := range []string{"locale key missing: he:hi", "/about [en]", "must be a JSON object"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
}

func mustCfg(t *testing.T, files map[string]string) *config.Config {
	t.Helper()
	c, err := config.Load(testutil.WriteSite(t, map[string]string{"hearth.config.json": files["hearth.config.json"]}))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCheckTrialRendersErrorTemplates(t *testing.T) {
	f := testutil.BaseSite()
	f["pages/404.html"] = `{{define "body"}}{{t "nope"}}{{end}}`
	f["pages/410.html"] = `{{define "body"}}fine {{.Status}}{{end}}`
	errs := Check(testutil.WriteSite(t, f), mustCfg(t, f))
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	if !strings.Contains(joined, "pages/404.html [en]") || strings.Contains(joined, "410") {
		t.Errorf("errors:\n%s", joined)
	}
}

func TestWarningsForEnvVarsWithoutPrefix(t *testing.T) {
	site := func(viteCfg string, src string) string {
		f := testutil.BaseSite()
		f["src/entry.ts"] = src
		if viteCfg != "" {
			f["vite.config.ts"] = viteCfg
		}
		return testutil.WriteSite(t, f)
	}
	use := `const u = import.meta.env.PUBLIC_API_URL; const m = import.meta.env.MODE; const v = import.meta.env.VITE_X; if (import.meta.env.DEV && import.meta.env.PROD || import.meta.env.SSR) {} const b = import.meta.env.BASE_URL;`
	// no vite config at all: only PUBLIC_ warns; built-ins and VITE_ are silent
	w := Warnings(site("", use))
	if len(w) != 1 || !strings.Contains(w[0], "PUBLIC_API_URL") || !strings.Contains(w[0], "src/entry.ts") {
		t.Fatalf("%v", w)
	}
	// a config without envPrefix warns too, and names the file
	if w := Warnings(site(`export default {}`, use)); len(w) != 1 || !strings.Contains(w[0], "vite.config.ts has no envPrefix") {
		t.Fatalf("%v", w)
	}
	// a commented-out envPrefix does not count
	if w := Warnings(site("export default {\n // envPrefix: ['PUBLIC_'],\n /* envPrefix: 'PUBLIC_' */ }", use)); len(w) != 1 {
		t.Fatalf("%v", w)
	}
	for name, cfg := range map[string]string{
		"array":    `export default { envPrefix: ['VITE_', "PUBLIC_"] }`,
		"string":   `export default defineConfig({ envPrefix: 'PUBLIC_', build: {} })`,
		"specific": "export default { envPrefix: [`PUBLIC_API`] }",
		"dynamic":  `const p = ["X"]; export default { envPrefix: p }`,
	} {
		if w := Warnings(site(cfg, use)); len(w) != 0 {
			t.Errorf("%s: unexpected %v", name, w)
		}
	}
	// a prefix that does not cover the name still warns
	if w := Warnings(site(`export default { envPrefix: "APP_" }`, use)); len(w) != 1 {
		t.Errorf("%v", w)
	}
	// VITE_ alone is silent; bracket access is read
	if w := Warnings(site("", `import.meta.env.VITE_A; import.meta.env["VITE_B"]`)); len(w) != 0 {
		t.Errorf("%v", w)
	}
	if w := Warnings(site("", `import.meta.env["PUBLIC_B"]`)); len(w) != 1 {
		t.Errorf("%v", w)
	}
	// a site with no src/ has nothing to say
	if w := Warnings(testutil.WriteSite(t, testutil.BaseSite())); len(w) != 0 {
		t.Errorf("%v", w)
	}
}
