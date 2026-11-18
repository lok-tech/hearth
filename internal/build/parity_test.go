package build

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orelhadad/hearth/internal/config"
	"github.com/orelhadad/hearth/internal/testutil"
	"github.com/orelhadad/hearth/pkg/hearth"
)

// A page built by `hearth build` and the same page answered by a server running the built bundle
// must be identical, byte for byte, and so must sitemap.xml and robots.txt. The server side below
// is written independently of the build (its own handler, its own Site), so this test fails if the
// two ever stop sharing one engine.
func TestBuiltFilesEqualWhatAServerAnswers(t *testing.T) {
	configs := map[string]string{
		"prefix":    `{"baseURL":"https://x.test","collections":{"post/{id}":{"data":"posts","param":"id"}}}`,
		"subdomain": `{"baseURL":"https://x.test","i18n":{"mode":"subdomain","hosts":{"en":"x.test","he":"he.x.test"}},"collections":{"post/{id}":{"data":"posts","param":"id"}}}`,
	}
	for name, cfgJSON := range configs {
		t.Run(name, func(t *testing.T) {
			files := testutil.BaseSite()
			files["hearth.config.json"] = cfgJSON
			site := testutil.WriteSite(t, files)
			cfg, err := config.Load(site)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Run(Options{SiteDir: site, Config: cfg}); err != nil {
				t.Fatal(err)
			}
			pub := filepath.Join(site, "dist", "public")

			// The "server": its own data access, straight from the JSON file.
			var posts []map[string]any
			if err := json.Unmarshal([]byte(files["data/posts.json"]), &posts); err != nil {
				t.Fatal(err)
			}
			byID := map[string]map[string]any{}
			var postPaths []string
			for _, p := range posts {
				id := fmt.Sprint(p["id"])
				if f, ok := p["id"].(float64); ok {
					id = fmt.Sprint(int64(f))
				}
				byID[id] = p
				postPaths = append(postPaths, "/post/"+id)
			}
			srv, err := hearth.New(hearth.Options{
				Bundle:      os.DirFS(filepath.Join(site, "dist", "templates")),
				CacheDir:    t.TempDir(),
				BaseURL:     cfg.BaseURL,
				DefaultLang: cfg.DefaultLang,
				I18n:        hearth.I18n{Mode: cfg.I18n.Mode, Hosts: cfg.I18n.Hosts},
				Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
				SitemapEntries: func(context.Context) ([]string, error) {
					return postPaths, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			srv.MustHandle("post/{id}", func(r *http.Request, p map[string]string) (any, hearth.Meta, error) {
				if e, ok := byID[p["id"]]; ok {
					return e, hearth.Meta{}, nil
				}
				return nil, hearth.Meta{}, hearth.ErrNotFound
			})

			hostMode := cfg.I18n.Mode != "prefix"
			compared := 0
			err = filepath.WalkDir(pub, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				rel, _ := filepath.Rel(pub, p)
				rel = filepath.ToSlash(rel)
				host, inner := "", rel
				if hostMode { // the first directory is the host
					host, inner, _ = strings.Cut(rel, "/")
				}
				var urlPath string
				switch {
				case inner == "index.html":
					urlPath = "/"
				case strings.HasSuffix(inner, "/index.html"):
					urlPath = "/" + strings.TrimSuffix(inner, "/index.html")
				case inner == "sitemap.xml" || inner == "robots.txt":
					urlPath = "/" + inner
				default:
					return nil // favicon and other plain static files
				}
				req := httptest.NewRequest(http.MethodGet, urlPath, nil)
				req.Host = host
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, req)
				want, _ := os.ReadFile(p)
				if w.Code != 200 {
					t.Errorf("%s: server answered %d", rel, w.Code)
					return nil
				}
				if w.Body.String() != string(want) {
					t.Errorf("%s differs between the build and the server:\n--- built\n%s\n--- server\n%s", rel, want, w.Body)
				}
				compared++
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			// 4 static + 4 post pages (2 languages), plus a sitemap and robots.txt per origin
			want := 8 + 2
			if hostMode {
				want = 8 + 4
			}
			if compared != want {
				t.Errorf("compared %d files, want %d", compared, want)
			}
		})
	}
}
