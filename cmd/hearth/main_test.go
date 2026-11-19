package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSite(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"hearth.config.json": `{"baseURL":"https://x.test"}`,
		"layouts/base.html":  `<title>{{block "title" .}}x{{end}}</title>{{block "body" .}}{{end}}`,
		"pages/index.html":   `{{define "body"}}home{{end}}`,
	}
	for p, c := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte(c), 0o644)
	}
	return dir
}

func do(args ...string) (int, string, string) {
	var o, e bytes.Buffer
	c := run(args, &o, &e)
	return c, o.String(), e.String()
}

func TestBuildAndCheck(t *testing.T) {
	site := writeSite(t)
	if c, o, e := do("check", "-site", site); c != 0 || !strings.Contains(o, "ok") {
		t.Fatalf("check: %d %q %q", c, o, e)
	}
	if c, o, e := do("build", "-site", site); c != 0 || !strings.Contains(o, "built 1 pages") {
		t.Fatalf("build: %d %q %q", c, o, e)
	}
	if b, err := os.ReadFile(filepath.Join(site, "dist", "public", "index.html")); err != nil || !strings.Contains(string(b), "home") {
		t.Fatalf("output: %q %v", b, err)
	}
}

func TestCheckFailsWithExitCode1(t *testing.T) {
	site := writeSite(t)
	os.WriteFile(filepath.Join(site, "pages", "index.html"), []byte(`{{define "body"}}{{t "x"}}{{end}}`), 0o644)
	c, _, e := do("check", "-site", site)
	if c != 1 || !strings.Contains(e, "problem") {
		t.Fatalf("%d %q", c, e)
	}
}

func TestUsageErrors(t *testing.T) {
	if c, _, e := do(); c != 2 || !strings.Contains(e, "Usage") {
		t.Errorf("no args: %d", c)
	}
	if c, _, _ := do("bogus"); c != 2 {
		t.Errorf("unknown cmd: %d", c)
	}
	if c, _, _ := do("build", "extra"); c != 2 {
		t.Errorf("extra arg: %d", c)
	}
	if c, _, _ := do("build", "-nope"); c != 2 {
		t.Errorf("bad flag: %d", c)
	}
	if c, _, e := do("dev", "-live", "-site", writeSite(t)); c != 1 || !strings.Contains(e, "dev.server") {
		t.Errorf("live without server: %d %q", c, e)
	}
	if c, _, _ := do("help"); c != 0 {
		t.Errorf("help: %d", c)
	}
}

func TestInitAndVersion(t *testing.T) {
	if c, o, _ := do("version"); c != 0 || !strings.HasPrefix(o, "hearth ") {
		t.Errorf("version: %d %q", c, o)
	}
	dir := filepath.Join(t.TempDir(), "My Site")
	if c, o, e := do("init", dir); c != 0 || !strings.Contains(o, "wrote") {
		t.Fatalf("init: %d %q %q", c, o, e)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "package.json")); !strings.Contains(string(b), `"name": "my-site"`) {
		t.Errorf("name not derived from dir: %s", b)
	}
	if c, o, e := do("check", "-site", dir); c != 0 {
		t.Fatalf("check on fresh starter: %d %q %q", c, o, e)
	}
	if c, _, e := do("init", dir); c != 1 || !strings.Contains(e, "refusing") {
		t.Errorf("second init: %d %q", c, e)
	}
	if c, _, _ := do("init", "a", "b"); c != 2 {
		t.Errorf("two dirs: %d", c)
	}
}

func TestCheckTreatsStubbedFuncsAsNotesAndBuildFailsOnThem(t *testing.T) {
	site := writeSite(t)
	os.WriteFile(filepath.Join(site, "hearth.config.json"), []byte(`{"funcs":["num"]}`), 0o644)
	os.WriteFile(filepath.Join(site, "pages", "index.html"), []byte(`{{define "body"}}{{num 1}}{{end}}`), 0o644)
	c, o, e := do("check", "-site", site)
	if c != 0 || !strings.Contains(e, "needs your Go program") || !strings.Contains(o, "ok") {
		t.Fatalf("check: %d %q %q", c, o, e)
	}
	// the CLI cannot bake a page that needs the Go program: it says so instead of writing a partial page
	if c, _, e := do("build", "-site", site); c != 1 || !strings.Contains(e, "provided by your Go program") {
		t.Fatalf("build: %d %q", c, e)
	}
}

func TestCheckRejectsUnknownSSREntry(t *testing.T) {
	site := writeSite(t)
	os.WriteFile(filepath.Join(site, "hearth.config.json"), []byte(`{"ssr":["/nope"]}`), 0o644)
	if c, _, e := do("check", "-site", site); c != 1 || !strings.Contains(e, `ssr entry "/nope"`) {
		t.Fatalf("%d %q", c, e)
	}
}

func TestCheckWarnsAboutEnvPrefixButStillPasses(t *testing.T) {
	site := writeSite(t)
	os.MkdirAll(filepath.Join(site, "src"), 0o755)
	os.WriteFile(filepath.Join(site, "src", "entry.ts"), []byte("fetch(import.meta.env.PUBLIC_API_URL)"), 0o644)
	c, o, e := do("check", "-site", site)
	if c != 0 || !strings.Contains(o, "ok") || !strings.Contains(e, "warning") || !strings.Contains(e, "PUBLIC_API_URL") {
		t.Fatalf("%d %q %q", c, o, e)
	}
	os.WriteFile(filepath.Join(site, "vite.config.ts"), []byte("export default { envPrefix: ['VITE_', 'PUBLIC_'] }"), 0o644)
	if c, _, e := do("check", "-site", site); c != 0 || strings.Contains(e, "warning") {
		t.Fatalf("%d %q", c, e)
	}
}
