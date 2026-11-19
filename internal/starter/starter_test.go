package starter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lok-tech/hearth/internal/build"
	"github.com/lok-tech/hearth/internal/config"

	"github.com/lok-tech/hearth/internal/testutil"
)

func TestInitStarterWritesWorkingSite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-site")
	files, err := Init(dir, "my-site")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 10 {
		t.Fatalf("files = %v", files)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err != nil {
		t.Error("dot_gitignore not renamed to .gitignore")
	}
	if _, err := os.Stat(filepath.Join(dir, "dot_gitignore")); err == nil {
		t.Error("dot_gitignore left behind")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "package.json")); !strings.Contains(string(b), `"name": "my-site"`) || strings.Contains(string(b), "{{name}}") {
		t.Errorf("package.json: %s", b)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if errs := build.Check(dir, cfg); len(errs) != 0 {
		t.Fatalf("starter fails its own check: %v", errs)
	}
	vc, _ := os.ReadFile(filepath.Join(dir, "vite.config.ts"))
	if !strings.Contains(string(vc), `envPrefix: ["VITE_", "PUBLIC_"]`) {
		t.Errorf("starter vite.config.ts lacks envPrefix:\n%s", vc)
	}
	// a PUBLIC_ variable in the starter's own sources raises no warning
	os.WriteFile(filepath.Join(dir, "src", "env.ts"), []byte("export const api = import.meta.env.PUBLIC_API_URL;"), 0o644)
	if w := build.Warnings(dir); len(w) != 0 {
		t.Errorf("starter warns about PUBLIC_: %v", w)
	}
}

// The build runs Vite as `<command> --outDir <dir> --emptyOutDir --manifest`. This stand-in writes what
// Vite and the image plugin would, so the whole build path is covered without Node.
const fakeVite = `mkdir -p "$1/.vite" "$1/.hearth" "$1/assets" &&
echo '{"src/entry.ts":{"file":"assets/entry-ab12.js","src":"src/entry.ts","isEntry":true,"css":["assets/entry-cd34.css"]}}' > "$1/.vite/manifest.json" &&
echo '{"hero":[{"url":"/assets/img/h.avif","type":"image/avif","width":10,"height":5},{"url":"/assets/img/h.jpg","type":"image/jpeg","width":10,"height":5}]}' > "$1/.hearth/images.json" &&
touch "$1/assets/entry-ab12.js"`

func TestStarterBuildsWithFakeVite(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	dir := filepath.Join(t.TempDir(), "s")
	if _, err := Init(dir, "s"); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(dir, "pages", "index.html")
	b, _ := os.ReadFile(idx)
	os.WriteFile(idx, []byte(strings.Replace(string(b), "</main>", `{{picture "hero" "x"}}</main>`, 1)), 0o644)
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Vite = &config.ViteConfig{Command: []string{"/bin/sh", "-c", fakeVite}}
	if _, err := build.Run(build.Options{SiteDir: dir, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	pub := filepath.Join(dir, "dist", "public")
	page := testutil.Read(t, pub, "he", "index.html")
	for _, want := range []string{`href="/assets/entry-cd34.css"`, `src="/assets/entry-ab12.js"`, `<source type="image/avif" srcset="/assets/img/h.avif">`, `width="10" height="5"`, `href="/he/about"`, `<a class="brand" href="/he">`, `dir="rtl"`} {
		if !strings.Contains(page, want) {
			t.Errorf("he/index.html missing %s", want)
		}
	}
	nf := testutil.Read(t, pub, "404.html")
	for _, want := range []string{`<meta name="robots" content="noindex">`, "Page not found", `href="/assets/entry-cd34.css"`, `<a class="brand" href="/">`} {
		if !strings.Contains(nf, want) {
			t.Errorf("404.html missing %s:\n%s", want, nf)
		}
	}
	if !strings.Contains(testutil.Read(t, pub, "he", "404.html"), "הדף לא נמצא") {
		t.Error("he/404.html is not Hebrew")
	}
	if strings.Contains(testutil.Read(t, pub, "index.html"), `name="robots"`) {
		t.Error("the home page must stay indexable")
	}
	for _, d := range []string{".vite", ".hearth"} {
		if _, err := os.Stat(filepath.Join(pub, d)); err == nil {
			t.Errorf("%s was deployed", d)
		}
	}
}

func TestInitStarterRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("mine"), 0o644)
	if _, err := Init(dir, "x"); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "package.json")); string(b) != "mine" {
		t.Fatal("existing file was modified")
	}
	if _, err := os.Stat(filepath.Join(dir, "pages")); err == nil {
		t.Fatal("partial write before refusing")
	}
}

func TestInitStarterRejectsBadName(t *testing.T) {
	for _, n := range []string{"Bad", "a b", "../x", "-x", `a"b`} {
		if _, err := Init(t.TempDir(), n); err == nil {
			t.Errorf("%q should fail", n)
		}
	}
}
