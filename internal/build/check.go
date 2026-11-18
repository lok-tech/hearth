package build

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/orelhadad/hearth/internal/config"
	"github.com/orelhadad/hearth/internal/data"
	"github.com/orelhadad/hearth/internal/i18n"
	"github.com/orelhadad/hearth/internal/route"
)

// Check validates a site without running Vite or writing output: routes,
// collections, mock files, catalog completeness, and a trial render of every
// prerendered page in every language. It returns all problems, not just the first.
func Check(siteDir string, cfg *config.Config) []error {
	var errs []error
	add := func(f string, a ...any) { errs = append(errs, fmt.Errorf(f, a...)) }
	src := os.DirFS(siteDir)

	routes, err := route.Discover(src, "pages")
	if err != nil {
		return []error{err}
	}
	var cats *i18n.Catalogs
	if _, err := fs.Stat(src, "locales"); err == nil {
		if cats, err = i18n.Load(src, "locales"); err != nil {
			return append(errs, err)
		}
		if !cats.Has(cfg.DefaultLang) {
			add("defaultLang %q has no locales/%s.json", cfg.DefaultLang, cfg.DefaultLang)
		} else {
			for _, m := range cats.Missing(cfg.DefaultLang) {
				add("locale key missing: %s", m)
			}
		}
		if cfg.I18n.Mode != "prefix" {
			for _, l := range cats.Langs() {
				if cfg.I18n.Hosts[l] == "" {
					add("i18n.hosts has no host for language %q", l)
				}
			}
		}
	}
	var dataFS fs.FS
	if st, err := fs.Stat(src, "data"); err == nil && st.IsDir() {
		dataFS, _ = fs.Sub(src, "data")
		_ = fs.WalkDir(dataFS, ".", func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".json") {
				if _, err := data.Read(dataFS, p); err != nil {
					errs = append(errs, err)
				}
			}
			return nil
		})
	}
	// mock files must be valid JSON objects
	for _, pat := range routes.Patterns() {
		pg, _ := routes.Lookup(pat)
		if !pg.IsDynamic() {
			continue
		}
		b, err := fs.ReadFile(src, pg.MockFile())
		if err != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(b, &m) != nil {
			add("%s: must be a JSON object mapping parameter value to data", filepath.ToSlash(pg.MockFile()))
		}
	}
	if len(errs) > 0 && dataFS == nil && len(cfg.Collections) > 0 {
		return errs
	}
	pl, err := planPages(src, cfg)
	if err != nil {
		return append(errs, err)
	}
	// The trial render is the real engine (hearth.Site), not a second renderer, so a page that
	// passes check renders the same way in the build and on a server.
	srv, cleanup, err := newSite(src, cfg, pl, true)
	if err != nil {
		return append(errs, err)
	}
	defer cleanup()
	for _, f := range srv.Prerender(context.Background(), pl.paths, runtime.NumCPU()).Failures {
		errs = append(errs, f)
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return errs
}
