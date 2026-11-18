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
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/orelhadad/hearth/internal/config"
	"github.com/orelhadad/hearth/internal/data"
	"github.com/orelhadad/hearth/internal/fsutil"
	"github.com/orelhadad/hearth/internal/manifest"
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
	man := &manifest.Manifest{Format: manifest.BundleFormat, Assets: map[string]string{}, Images: map[string][]manifest.ImageFile{}}
	if cfg.Vite != nil {
		o.logf("vite: %s", strings.Join(cfg.Vite.Command, " "))
		if man, err = runVite(ctx, site, pub, cfg.Vite, cfg.AssetBase, o.Log); err != nil {
			return nil, err
		}
	}
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
	pr := srv.Prerender(ctx, pl.paths, runtime.NumCPU())
	if err := failures(pr); err != nil {
		return nil, err
	}
	if err := srv.Export(pub); err != nil {
		return nil, err
	}
	rep := &Report{Out: out, Pages: pr.Pages}
	for _, pg := range rep.Pages {
		o.logf("page %s", pg)
	}
	if cfg.BaseURL != "" {
		if err := writeSitemapAndRobots(srv, cfg, pub); err != nil {
			return nil, err
		}
	}
	return rep, nil
}

// failures turns a prerender report into one error (the first problem, plus a count).
func failures(pr hearth.PrerenderReport) error {
	if len(pr.Failures) == 0 {
		return nil
	}
	if n := len(pr.Failures); n > 1 {
		return fmt.Errorf("%w (and %d more pages failed)", pr.Failures[0], n-1)
	}
	return pr.Failures[0]
}

// origins lists the host directories of the output: one root, or one per language host.
func origins(cfg *config.Config) []string {
	if cfg.I18n.Mode == "prefix" {
		return []string{""}
	}
	seen := map[string]bool{}
	var hosts []string
	for _, h := range cfg.I18n.Hosts {
		if !seen[h] {
			seen[h] = true
			hosts = append(hosts, h)
		}
	}
	sort.Strings(hosts)
	return hosts
}

// writeSitemapAndRobots saves what the Site itself answers for /sitemap.xml and /robots.txt,
// once per host, so the static files match the server's.
func writeSitemapAndRobots(site *hearth.Site, cfg *config.Config, pub string) error {
	for _, host := range origins(cfg) {
		dir := pub
		if host != "" {
			dir = filepath.Join(pub, host)
		}
		for _, name := range []string{"sitemap.xml", "robots.txt"} {
			target := filepath.Join(dir, name)
			if _, err := os.Stat(target); err == nil {
				continue // robots.txt shipped in public/ wins
			}
			req := httptest.NewRequest(http.MethodGet, "/"+name, nil)
			req.Host = host
			w := httptest.NewRecorder()
			site.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				return fmt.Errorf("hearth: /%s answered %d", name, w.Code)
			}
			if err := fsutil.WriteFileAtomic(target, w.Body.Bytes()); err != nil {
				return err
			}
		}
	}
	return nil
}

// plan is what a build has to render: language-neutral paths, plus the handlers that supply
// the data of bound collections (the build's stand-in for a server's database).
type plan struct {
	paths    []string                  // every page to prerender
	entries  []string                  // the collection pages among them, for the sitemap
	handlers map[string]hearth.Handler // [param] pattern -> handler
}

// planPages lists every page to bake: static pages, plus each entry of a bound collection for
// [param] pages. Unbound dynamic pages are SSR-only and are not prerendered.
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
	pl := &plan{handlers: map[string]hearth.Handler{}}
	for _, pat := range routes.Patterns() {
		pg, _ := routes.Lookup(pat)
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
		Bundle:         bundle,
		CacheDir:       dir,
		BaseURL:        cfg.BaseURL,
		DefaultLang:    cfg.DefaultLang,
		I18n:           hearth.I18n{Mode: cfg.I18n.Mode, Hosts: cfg.I18n.Hosts},
		LenientAssets:  lenient,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		SitemapEntries: func(context.Context) ([]string, error) { return pl.entries, nil },
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
	File string   `json:"file"`
	Src  string   `json:"src"`
	CSS  []string `json:"css"`
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
	b, err := os.ReadFile(filepath.Join(pubDir, ".vite", "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("hearth: vite produced no .vite/manifest.json: %w", err)
	}
	m, err := ConvertViteManifest(b)
	if err != nil {
		return nil, err
	}
	// The image step (hearth/vite) leaves its manifest in .hearth/images.json.
	if ib, err := os.ReadFile(filepath.Join(pubDir, ".hearth", "images.json")); err == nil {
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

// ConvertViteManifest maps Vite's manifest to Hearth's: assets["src/entry.ts"] =
// "/assets/entry-<hash>.js"; an entry's first CSS file is assets["src/entry.ts.css"].
func ConvertViteManifest(b []byte) (*manifest.Manifest, error) {
	var in map[string]viteEntry
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, fmt.Errorf("hearth: vite manifest: %w", err)
	}
	m := &manifest.Manifest{Format: manifest.BundleFormat, Assets: map[string]string{}, Images: map[string][]manifest.ImageFile{}}
	for key, e := range in {
		if e.File == "" {
			continue
		}
		m.Assets[key] = "/" + e.File
		if len(e.CSS) > 0 {
			m.Assets[key+".css"] = "/" + e.CSS[0]
		}
	}
	return m, nil
}
