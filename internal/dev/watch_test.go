package dev

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lok-tech/hearth/internal/config"
	"github.com/lok-tech/hearth/internal/testutil"
)

// Stand-in for `vite build --watch`: args after the script are --watch --outDir <dir> ..., it
// writes one build and then stays alive like a watcher.
const fakeWatchVite = `D="$3"
mkdir -p "$D/.vite" "$D/assets"
echo '{"src/entry.ts":{"file":"assets/entry-ab12.js","src":"src/entry.ts","isEntry":true,"css":["assets/entry-cd34.css"]}}' > "$D/.vite/manifest.json"
echo 'body{color:red}' > "$D/assets/entry-cd34.css"
exec sleep 30`

func TestWatchServesPagesAndBuiltAssetsOnOnePort(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	f := testutil.BaseSite()
	f["pages/styled.html"] = `{{define "body"}}<link rel="stylesheet" href="{{asset "src/entry.ts.css"}}"><script type="module" src="{{asset "src/entry.ts"}}"></script>{{if .HMR}}VITE-CLIENT{{end}}{{end}}`
	f["pages/entried.html"] = `{{define "body"}}{{entry "src/entry.ts"}}{{end}}`
	f["public/robots-dev.txt"] = "public file"
	site := testutil.WriteSite(t, f)
	cfg := &config.Config{DefaultLang: "en", I18n: config.I18nConfig{Mode: "prefix"},
		Vite: &config.ViteConfig{Command: []string{"sh", "-c", fakeWatchVite, "vite"}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, stop, err := NewWatch(ctx, site, cfg, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	body := testutil.Get(h, "/styled").Body.String()
	for _, want := range []string{`href="/assets/entry-cd34.css"`, `src="/assets/entry-ab12.js"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s: %s", want, body)
		}
	}
	if e := testutil.Get(h, "/entried").Body.String(); !strings.Contains(e, `<link rel="stylesheet" href="/assets/entry-cd34.css">`) || !strings.Contains(e, `<script type="module" src="/assets/entry-ab12.js"></script>`) {
		t.Errorf("{{entry}} in watch mode: %s", e)
	}
	if strings.Contains(body, "VITE-CLIENT") {
		t.Error("watch mode must not behave like a Vite dev server page")
	}
	if w := testutil.Get(h, "/assets/entry-cd34.css"); w.Code != 200 || !strings.Contains(w.Body.String(), "color:red") {
		t.Errorf("built asset: %d %s", w.Code, w.Body)
	}
	if w := testutil.Get(h, "/robots-dev.txt"); w.Code != 200 || w.Body.String() != "public file" {
		t.Errorf("public file: %d %s", w.Code, w.Body)
	}
	for _, p := range []string{"/.vite/manifest.json", "/assets/../.vite/manifest.json"} {
		if w := testutil.Get(h, p); w.Code != 404 {
			t.Errorf("%s must not be served: %d", p, w.Code)
		}
	}
	// a new build with new hashes shows up on the next request, templates reload too
	d := filepath.Join(site, "node_modules", ".cache", "hearth-dev")
	os.WriteFile(filepath.Join(d, ".vite", "manifest.json"), []byte(`{"src/entry.ts":{"file":"assets/entry-ZZ.js","src":"src/entry.ts","isEntry":true,"css":["assets/entry-YY.css"]}}`), 0o644)
	if b := testutil.Get(h, "/styled").Body.String(); !strings.Contains(b, "entry-YY.css") {
		t.Errorf("new manifest not picked up: %s", b)
	}
	// while a rebuild empties the output directory, the last good manifest is kept
	os.RemoveAll(filepath.Join(d, ".vite"))
	if b := testutil.Get(h, "/styled").Body.String(); !strings.Contains(b, "entry-YY.css") {
		t.Errorf("rebuild window broke the page: %s", b)
	}
}

func TestWatchFailsWhenViteExitsEarly(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	site := testutil.WriteSite(t, testutil.BaseSite())
	cfg := &config.Config{DefaultLang: "en", Vite: &config.ViteConfig{Command: []string{"sh", "-c", "exit 3", "vite"}}}
	done := make(chan error, 1)
	go func() { _, _, err := NewWatch(context.Background(), site, cfg, io.Discard); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "exited before") {
			t.Errorf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("NewWatch hung")
	}
}
