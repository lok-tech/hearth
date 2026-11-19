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
