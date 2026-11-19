package build

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/orelhadad/hearth/internal/config"
	"github.com/orelhadad/hearth/internal/data"
	"github.com/orelhadad/hearth/internal/fsutil"
	"github.com/orelhadad/hearth/internal/manifest"
	"github.com/orelhadad/hearth/internal/mock"
	"github.com/orelhadad/hearth/internal/route"
	"github.com/orelhadad/hearth/pkg/hearth"
)

// Options configures Build.
type Options struct {
	SiteDir string
	OutDir  string // default <SiteDir>/dist
	Config  *config.Config
	Log     io.Writer // progress; may be nil
	Context context.Context
}

// Report lists what a build produced.
type Report struct {
	Pages []string // URL paths prerendered (with language prefix where it applies)
	Out   string
}

var bundleDirs = []string{"layouts", "partials", "pages", "locales", "data"}

func (o *Options) logf(f string, a ...any) {
	if o.Log != nil {
		fmt.Fprintf(o.Log, f+"\n", a...)
	}
}

// Run produces dist/public (prerendered pages, assets), dist/templates (the
// bundle for the Go server) and dist/manifest.json.
func Run(o Options) (*Report, error) {
	if o.SiteDir == "" {
		return nil, fmt.Errorf("hearth: build needs SiteDir")
	}
	site, err := filepath.Abs(o.SiteDir)
	if err != nil {
		return nil, err
	}
	out := o.OutDir
	if out == "" {
		out = filepath.Join(site, "dist")
	}
	if out, err = filepath.Abs(out); err != nil {
		return nil, err
	}
	if out == site || out == filepath.Dir(site) || out == string(filepath.Separator) || strings.HasPrefix(site, out+string(filepath.Separator)) {
		return nil, fmt.Errorf("hearth: refusing to use %s as output directory", out)
	}
	cfg := o.Config
	if cfg == nil {
		if cfg, err = config.Load(site); err != nil {
			return nil, err
		}
	}
	ctx := o.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if routes, err := route.Discover(os.DirFS(site), "pages"); err != nil {
		return nil, err
	} else if _, err := ssrPatterns(routes, cfg); err != nil {
		return nil, err
	}
	pub, tpl := filepath.Join(out, "public"), filepath.Join(out, "templates")
	for _, d := range []string{pub, tpl} {
		if err := os.RemoveAll(d); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	// Static files in public/ are served as-is.
	if _, err := os.Stat(filepath.Join(site, "public")); err == nil {
		if err := copyTree(os.DirFS(filepath.Join(site, "public")), ".", pub, nil); err != nil {
			return nil, err
		}
	}
	man := &manifest.Manifest{Format: manifest.BundleFormat, Assets: map[string]string{}, Entries: map[string]manifest.Entry{}, Images: map[string][]manifest.ImageFile{}}
	if cfg.Vite != nil {
		o.logf("vite: %s", strings.Join(cfg.Vite.Command, " "))
		if man, err = runVite(ctx, site, pub, cfg.Vite, cfg.AssetBase, o.Log); err != nil {
			return nil, err
		}
	}
	man.SSR = cfg.SSR
	man.Version = fmt.Sprintf("%x", hashDirNames(site))
	mb, _ := json.MarshalIndent(man, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "manifest.json"), mb, 0o644); err != nil {
		return nil, err
	}

	isHTML := func(p string) bool { return strings.HasSuffix(p, ".html") || !strings.HasPrefix(p, "pages/") }
	for _, d := range bundleDirs {
		if _, err := os.Stat(filepath.Join(site, d)); err != nil {
			continue
		}
		if err := copyTree(os.DirFS(site), d, tpl, isHTML); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(filepath.Join(tpl, "manifest.json"), mb, 0o644); err != nil {
		return nil, err
	}

	// Pages are not rendered here. The build hands the bundle to a hearth.Site, the same engine a
	// server runs, and exports what it produced: static output and SSR output cannot differ.
	bundle := os.DirFS(tpl)
	pl, err := planPages(bundle, cfg)
	if err != nil {
		return nil, err
	}
	srv, cleanup, err := newSite(bundle, cfg, pl, false)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	// hearth.Build is Prerender + Export + sitemap and robots: the one routine a Go program with
	// its own handlers and Funcs calls too.
	pr, err := hearth.Build(ctx, srv, pub)
	if err != nil {
		return nil, err
	}
	rep := &Report{Out: out, Pages: pr.Pages}
	for _, pg := range rep.Pages {
		o.logf("page %s", pg)
	}
	return rep, nil
}

// plan is what a build has to render: language-neutral paths, plus the handlers that supply
// the data of bound collections (the build's stand-in for a server's database).
type plan struct {
	paths    []string                  // every page to prerender (ssr pages are not among them)
	ssr      []*route.Page             // parameterless pages served by handlers
	entries  []string                  // the collection pages among them, for the sitemap
	handlers map[string]hearth.Handler // [param] pattern -> handler
}

// ssrPatterns checks the config's ssr list against the discovered routes and returns the pages.
// An unknown entry, or one with route parameters, is an error.
func ssrPatterns(routes *route.Table, cfg *config.Config) (map[string]*route.Page, error) {
	out := map[string]*route.Page{}
	for _, p := range cfg.SSR {
		pg, ok := routes.Lookup(p)
		switch {
		case !ok:
			return nil, fmt.Errorf("hearth: ssr entry %q matches no template (have: %s)", p, strings.Join(routes.Patterns(), ", "))
		case pg.IsDynamic():
			return nil, fmt.Errorf("hearth: ssr entry %q has route parameters; pages with [param] are already server-rendered", p)
		}
		out[pg.Pattern] = pg
	}
	return out, nil
}

// planPages lists every page to bake: static pages, plus each entry of a bound collection for
// [param] pages. Unbound dynamic pages and ssr pages are served by handlers and are not prerendered.
func planPages(bundle fs.FS, cfg *config.Config) (*plan, error) {
	routes, err := route.Discover(bundle, "pages")
	if err != nil {
		return nil, err
	}
	var dataFS fs.FS
	if st, err := fs.Stat(bundle, "data"); err == nil && st.IsDir() {
		dataFS, _ = fs.Sub(bundle, "data")
	}
	for pat := range cfg.Collections {
		pg, ok := routes.Lookup(pat)
		if !ok {
			return nil, fmt.Errorf("hearth: collection %q matches no template (have: %s)", pat, strings.Join(routes.Patterns(), ", "))
		}
		if len(pg.Params) != 1 {
			return nil, fmt.Errorf("hearth: collection %q: exactly one route parameter is supported", pat)
		}
	}
	ssr, err := ssrPatterns(routes, cfg)
	if err != nil {
		return nil, err
	}
	pl := &plan{handlers: map[string]hearth.Handler{}}
	for _, pat := range routes.Patterns() {
		pg, _ := routes.Lookup(pat)
		if ssr[pg.Pattern] != nil {
			pl.ssr = append(pl.ssr, pg)
			continue
		}
		if !pg.IsDynamic() {
			pl.paths = append(pl.paths, pg.URL())
			continue
		}
		col, ok := cfg.Collections[pat]
		if !ok {
			continue
		}
		if dataFS == nil {
			return nil, fmt.Errorf("hearth: collection %q needs a data/ folder", pat)
		}
		raw, err := data.Read(dataFS, col.Data)
		if err != nil {
			return nil, err
		}
		arr, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("hearth: data %q must be a JSON array", col.Data)
		}
		byValue := map[string]map[string]any{}
		for i, e := range arr {
			obj, ok := e.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("hearth: data %q entry %d is not an object", col.Data, i)
			}
			v, ok := obj[col.Param]
			if !ok {
				return nil, fmt.Errorf("hearth: data %q entry %d has no %q", col.Data, i, col.Param)
			}
			val := fmt.Sprint(v)
			if f, isNum := v.(float64); isNum && f == float64(int64(f)) {
				val = fmt.Sprint(int64(f))
			}
			if val == "" || strings.ContainsAny(val, "/\\?#%\x00") || val == "." || val == ".." || byValue[val] != nil {
				return nil, fmt.Errorf("hearth: data %q entry %d: unusable or duplicate %s %q", col.Data, i, col.Param, val)
			}
			byValue[val] = obj
			path := "/" + strings.Replace(pg.Pattern, "{"+col.Param+"}", val, 1)
			pl.paths = append(pl.paths, path)
			pl.entries = append(pl.entries, path)
		}
		param := col.Param
		pl.handlers[pat] = func(r *http.Request, p map[string]string) (any, hearth.Meta, error) {
			if e, ok := byValue[p[param]]; ok {
				return e, hearth.Meta{}, nil
			}
			return nil, hearth.Meta{}, hearth.ErrNotFound
		}
	}
	return pl, nil
}

// newSite builds the Site that renders a plan. Its cache is a throwaway directory.
func newSite(bundle fs.FS, cfg *config.Config, pl *plan, lenient bool) (*hearth.Site, func(), error) {
	dir, err := os.MkdirTemp("", "hearth-build-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	site, err := hearth.New(hearth.Options{
		Bundle:        bundle,
		CacheDir:      dir,
		BaseURL:       cfg.BaseURL,
		DefaultLang:   cfg.DefaultLang,
		I18n:          hearth.I18n{Mode: cfg.I18n.Mode, Hosts: cfg.I18n.Hosts},
		LenientAssets: lenient,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		SSR:           cfg.SSR,
		Funcs:         mock.Stubs(cfg.Funcs),
		SitemapURLs: func(context.Context) ([]hearth.SitemapEntry, error) {
			out := make([]hearth.SitemapEntry, len(pl.entries)) // collection data carries no dates
			for i, p := range pl.entries {
				out[i] = hearth.SitemapEntry{Path: p}
			}
			return out, nil
		},
	})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	for pat, h := range pl.handlers {
		if err := site.Handle(pat, h); err != nil {
			cleanup()
			return nil, nil, err
		}
	}
	return site, cleanup, nil
}

func copyTree(src fs.FS, root, dstRoot string, keep func(rel string) bool) error {
	return fs.WalkDir(src, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("hearth: refusing to copy symlink %s", p)
		}
		if keep != nil && !keep(p) {
			return nil
		}
		rel := p
		if root == "." {
			rel = p
		}
		b, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		return fsutil.WriteFileAtomic(filepath.Join(dstRoot, filepath.FromSlash(rel)), b)
	})
}

// hashDirNames derives a short build id from the site's file listing and sizes.
func hashDirNames(site string) uint32 {
	var h uint32 = 2166136261
	_ = filepath.WalkDir(site, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() && (d.Name() == "dist" || d.Name() == "node_modules" || d.Name() == ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		info, _ := d.Info()
		for _, c := range []byte(p) {
			h = (h ^ uint32(c)) * 16777619
		}
		if info != nil {
			h = (h ^ uint32(info.Size()) ^ uint32(info.ModTime().UnixNano())) * 16777619
		}
		return nil
	})
	return h
}

type viteEntry struct {
	File    string   `json:"file"`
	Src     string   `json:"src"`
	IsEntry bool     `json:"isEntry"`
	CSS     []string `json:"css"`
	Imports []string `json:"imports"` // static imports: keys of other manifest entries
}

// runVite runs the configured Vite command into pubDir and converts its manifest.
func runVite(ctx context.Context, site, pubDir string, v *config.ViteConfig, assetBase string, log io.Writer) (*manifest.Manifest, error) {
	args := append([]string{}, v.Command[1:]...)
	args = append(args, "--outDir", pubDir, "--emptyOutDir", "--manifest")
	if assetBase != "" {
		args = append(args, "--base", assetBase+"/")
	}
	cmd := exec.CommandContext(ctx, v.Command[0], args...)
	cmd.Dir = site
	if log != nil {
		cmd.Stdout, cmd.Stderr = log, log
	}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("hearth: vite build failed: %w", err)
	}
	m, err := LoadViteOutput(pubDir)
	if err != nil {
		return nil, err
	}
	if assetBase != "" {
		m.PrefixURLs(assetBase)
	}
	// Build-internal files are not deployed.
	for _, d := range []string{".vite", ".hearth"} {
		if err := os.RemoveAll(filepath.Join(pubDir, d)); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// LoadViteOutput reads what a Vite run left in dir: .vite/manifest.json, plus the image manifest
// .hearth/images.json written by the hearth/vite plugin. It deletes nothing and applies no base.
func LoadViteOutput(dir string) (*manifest.Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, ".vite", "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("hearth: vite produced no .vite/manifest.json: %w", err)
	}
	m, err := ConvertViteManifest(b)
	if err != nil {
		return nil, err
	}
	if ib, err := os.ReadFile(filepath.Join(dir, ".hearth", "images.json")); err == nil {
		var imgs map[string][]manifest.ImageFile
		if err := json.Unmarshal(ib, &imgs); err != nil {
			return nil, fmt.Errorf("hearth: .hearth/images.json: %w", err)
		}
		for name, files := range imgs {
			if len(files) == 0 {
				return nil, fmt.Errorf("hearth: image %q has no files", name)
			}
			m.Images[name] = files
		}
	}
	return m, nil
}

// ConvertViteManifest maps Vite's manifest to Hearth's. Every file keeps its key as a logical
// asset name (assets["src/entry.ts"] = "/assets/entry-<hash>.js"; an entry's first CSS file is
// assets["src/entry.ts.css"]). Every entry (isEntry) is also resolved into Entries: its script,
// its CSS and that of all statically imported chunks (cycle-safe, de-duplicated, imported chunks
// first so the cascade follows execution order), and the chunks to preload. Dynamic imports are
// not followed.
func ConvertViteManifest(b []byte) (*manifest.Manifest, error) {
	var in map[string]viteEntry
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, fmt.Errorf("hearth: vite manifest: %w", err)
	}
	m := &manifest.Manifest{Format: manifest.BundleFormat, Assets: map[string]string{}, Entries: map[string]manifest.Entry{}, Images: map[string][]manifest.ImageFile{}}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		e := in[key]
		if e.File == "" {
			continue
		}
		m.Assets[key] = "/" + e.File
		if len(e.CSS) > 0 {
			m.Assets[key+".css"] = "/" + e.CSS[0]
		}
	}
	for _, key := range keys {
		e := in[key]
		if !e.IsEntry || e.File == "" {
			continue
		}
		out := manifest.Entry{CSS: []string{}, Preload: []string{}}
		seenCSS := map[string]bool{}
		addCSS := func(files []string) {
			for _, f := range files {
				if !seenCSS[f] {
					seenCSS[f] = true
					out.CSS = append(out.CSS, "/"+f)
				}
			}
		}
		seen := map[string]bool{key: true}
		seenFile := map[string]bool{e.File: true}
		var walk func(from string, imports []string) error
		walk = func(from string, imports []string) error {
			for _, imp := range imports {
				c, ok := in[imp]
				if !ok {
					return fmt.Errorf("hearth: vite manifest: %q imports %q, which is not in the manifest", from, imp)
				}
				if seen[imp] {
					continue
				}
				seen[imp] = true
				if c.File != "" && !seenFile[c.File] {
					seenFile[c.File] = true
					out.Preload = append(out.Preload, "/"+c.File)
				}
				if err := walk(imp, c.Imports); err != nil {
					return err
				}
				addCSS(c.CSS)
			}
			return nil
		}
		if err := walk(key, e.Imports); err != nil {
			return nil, err
		}
		addCSS(e.CSS)
		if strings.HasSuffix(e.File, ".css") { // a CSS-only entry
			addCSS([]string{e.File})
		} else {
			out.JS = "/" + e.File
		}
		m.Entries[key] = out
	}
	return m, nil
}
