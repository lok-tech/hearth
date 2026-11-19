package render

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"path"
	"reflect"
	"sort"
	"strings"
	"sync"
	"unicode"

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
	Status     int               // HTTP status of this response: 200, or 404/410 in the error templates
	NoIndex    bool              // the layout emits <meta name="robots" content="noindex">; see Render
	Dev        bool              // true under `hearth dev` with a Vite dev server (it serves assets, no hashed CSS file)
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
	extra    template.FuncMap

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
	// Funcs are extra template functions. Names must not shadow a built-in helper or a Go template
	// builtin; each value is a func with one result, or a result plus error.
	Funcs template.FuncMap
}

// New validates the bundle layout and returns a Renderer.
func New(o Options) (*Renderer, error) {
	if o.FS == nil || o.Routes == nil {
		return nil, fmt.Errorf("hearth: renderer needs FS and route.Table")
	}
	if _, err := fs.Stat(o.FS, LayoutName); err != nil {
		return nil, fmt.Errorf("hearth: missing %s: %w", LayoutName, err)
	}
	r := &Renderer{fsys: o.FS, routes: o.Routes, manifest: o.Manifest, catalogs: o.Catalogs, data: o.Data,
		cache: map[string]*template.Template{}}
	if err := r.checkFuncs(o.Funcs); err != nil {
		return nil, err
	}
	r.extra = o.Funcs
	return r, nil
}

// goBuiltins are the predefined functions of text/template; a custom function may not shadow them.
var goBuiltins = []string{"and", "call", "html", "index", "slice", "js", "len", "not", "or", "print",
	"printf", "println", "urlquery", "eq", "ge", "gt", "le", "lt", "ne"}

var errType = reflect.TypeOf((*error)(nil)).Elem()

// checkFuncs enforces the rules for custom functions, so a mistake fails at New and not (or worse,
// panics) inside a template.
func (r *Renderer) checkFuncs(fm template.FuncMap) error {
	reserved := map[string]bool{}
	for _, n := range goBuiltins {
		reserved[n] = true
	}
	for n := range r.builtinFuncs("") {
		reserved[n] = true
	}
	names := make([]string, 0, len(fm))
	for n := range fm {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !validFuncName(n) {
			return fmt.Errorf("hearth: Funcs: %q is not a valid function name", n)
		}
		if reserved[n] {
			return fmt.Errorf("hearth: Funcs: %q is a built-in template function or Hearth helper and cannot be replaced", n)
		}
		v := reflect.ValueOf(fm[n])
		if v.Kind() != reflect.Func || v.IsNil() {
			return fmt.Errorf("hearth: Funcs: %q must be a function", n)
		}
		t := v.Type()
		if t.NumOut() == 0 || t.NumOut() > 2 || t.NumOut() == 2 && !t.Out(1).Implements(errType) {
			return fmt.Errorf("hearth: Funcs: %q must return one value, or a value and an error", n)
		}
	}
	return nil
}

func validFuncName(n string) bool {
	if n == "" {
		return false
	}
	for i, c := range n {
		if !(c == '_' || unicode.IsLetter(c) || i > 0 && unicode.IsDigit(c)) {
			return false
		}
	}
	return true
}

func (r *Renderer) funcs(lang string) template.FuncMap {
	fm := r.builtinFuncs(lang)
	for n, f := range r.extra {
		fm[n] = f
	}
	return fm
}

func (r *Renderer) builtinFuncs(lang string) template.FuncMap {
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
		"entry": func(name string) (template.HTML, error) {
			if r.manifest == nil {
				return "", fmt.Errorf("hearth: no manifest loaded")
			}
			return r.manifest.Entry(name)
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
	if d.Status == 0 {
		d.Status = 200
	}
	// A static page can ask for noindex with {{define "robots"}}noindex{{end}}. It is folded into
	// NoIndex here, so a handler's Meta.NoIndex and the template take the same path and the layout
	// emits exactly one tag.
	if rt := t.Lookup("robots"); rt != nil {
		var rb bytes.Buffer
		if err := rt.Execute(&rb, d); err != nil {
			return nil, fmt.Errorf("hearth: render %s: robots: %w", pg.File, err)
		}
		switch strings.TrimSpace(rb.String()) {
		case "":
		case "noindex":
			d.NoIndex = true
		default:
			return nil, fmt.Errorf("hearth: %s: {{define \"robots\"}} must produce \"noindex\" or nothing, got %q", pg.File, strings.TrimSpace(rb.String()))
		}
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return nil, fmt.Errorf("hearth: render %s: %w", pg.File, err)
	}
	return buf.Bytes(), nil
}
