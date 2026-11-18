package render

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"path"
	"sort"
	"sync"

	"github.com/orelhadad/hearth/internal/data"
	"github.com/orelhadad/hearth/internal/i18n"
	"github.com/orelhadad/hearth/internal/manifest"
	"github.com/orelhadad/hearth/internal/route"
)

// LayoutName is the shared layout template. It defines the blocks
// title, description, canonical, head and body; pages override them with {{define}}.
const LayoutName = "layouts/base.html"

// PageData is what templates receive as ".".
type PageData struct {
	Lang       string
	Dir        string // "ltr" or "rtl"
	Params     map[string]string
	Data       any
	Canonical  string
	Alternates map[string]string // lang -> absolute URL, for hreflang
	Prefix     string            // URL prefix of this language ("/he", or "" for default/other i18n modes)
	Dev        bool              // true under `hearth dev` (Vite serves assets, no hashed CSS file)
}

// Link returns path inside the current language: Link "/about" -> "/he/about",
// Link "/" -> "/he" (no trailing slash, matching the canonical URL).
func (d PageData) Link(path string) string {
	if path == "/" && d.Prefix != "" {
		return d.Prefix
	}
	return d.Prefix + path
}

// Renderer parses and renders templates from a bundle. Parsed sets are cached per (page, lang).
type Renderer struct {
	fsys     fs.FS
	routes   *route.Table
	manifest *manifest.Manifest
	catalogs *i18n.Catalogs
	data     fs.FS // data/ folder for the "data" helper; may be nil

	mu    sync.RWMutex
	cache map[string]*template.Template
}

// Options configures New. Manifest, Catalogs and Data are optional.
type Options struct {
	FS       fs.FS
	Routes   *route.Table
	Manifest *manifest.Manifest
	Catalogs *i18n.Catalogs
	Data     fs.FS
}

// New validates the bundle layout and returns a Renderer.
func New(o Options) (*Renderer, error) {
	if o.FS == nil || o.Routes == nil {
		return nil, fmt.Errorf("hearth: renderer needs FS and route.Table")
	}
	if _, err := fs.Stat(o.FS, LayoutName); err != nil {
		return nil, fmt.Errorf("hearth: missing %s: %w", LayoutName, err)
	}
	return &Renderer{fsys: o.FS, routes: o.Routes, manifest: o.Manifest, catalogs: o.Catalogs, data: o.Data,
		cache: map[string]*template.Template{}}, nil
}

func (r *Renderer) funcs(lang string) template.FuncMap {
	return template.FuncMap{
		"t": func(key string) (string, error) {
			if r.catalogs == nil {
				return "", fmt.Errorf("hearth: no locales loaded")
			}
			return r.catalogs.T(lang, key)
		},
		"plural": func(key string, n int) (string, error) {
			if r.catalogs == nil {
				return "", fmt.Errorf("hearth: no locales loaded")
			}
			return r.catalogs.Plural(lang, key, n)
		},
		"asset": func(name string) (string, error) {
			if r.manifest == nil {
				return "", fmt.Errorf("hearth: no manifest loaded")
			}
			return r.manifest.Asset(name)
		},
		"picture": func(name, alt string) (template.HTML, error) {
			if r.manifest == nil {
				return "", fmt.Errorf("hearth: no manifest loaded")
			}
			return r.manifest.Picture(name, alt, true)
		},
		"json": JSONForScript,
		"data": func(name string) (any, error) {
			if r.data == nil {
				return nil, fmt.Errorf("hearth: no data folder")
			}
			return data.Read(r.data, name)
		},
	}
}

func (r *Renderer) get(pg *route.Page, lang string) (*template.Template, error) {
	file := pg.File
	if f, ok := pg.Langs[lang]; ok {
		file = f
	}
	key := file
	r.mu.RLock()
	t, ok := r.cache[key+"|"+lang]
	r.mu.RUnlock()
	if ok {
		return t, nil
	}
	files := []string{LayoutName}
	if parts, err := fs.Glob(r.fsys, "partials/*.html"); err == nil {
		sort.Strings(parts)
		files = append(files, parts...)
	}
	files = append(files, file)
	t = template.New(path.Base(LayoutName)).Funcs(r.funcs(lang)).Option("missingkey=error")
	for _, f := range files {
		b, err := fs.ReadFile(r.fsys, f)
		if err != nil {
			return nil, err
		}
		// Layout is the root template; others are parsed into the same set.
		var tt *template.Template
		if f == LayoutName {
			tt = t
		} else {
			tt = t.New(path.Base(f))
		}
		if _, err := tt.Parse(string(b)); err != nil {
			return nil, fmt.Errorf("hearth: %s: %w", f, err)
		}
	}
	r.mu.Lock()
	r.cache[key+"|"+lang] = t
	r.mu.Unlock()
	return t, nil
}

// Catalogs returns the loaded translation catalogs, or nil when the site has no locales.
func (r *Renderer) Catalogs() *i18n.Catalogs { return r.catalogs }

// Render executes the page inside the shared layout.
func (r *Renderer) Render(pg *route.Page, d PageData) ([]byte, error) {
	if d.Dir == "" {
		d.Dir = "ltr"
	}
	t, err := r.get(pg, d.Lang)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return nil, fmt.Errorf("hearth: render %s: %w", pg.File, err)
	}
	return buf.Bytes(), nil
}
