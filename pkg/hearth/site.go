package hearth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"

	"github.com/orelhadad/hearth/internal/cache"
	"github.com/orelhadad/hearth/internal/i18n"
	"github.com/orelhadad/hearth/internal/manifest"
	"github.com/orelhadad/hearth/internal/render"
	"github.com/orelhadad/hearth/internal/route"
	"github.com/orelhadad/hearth/internal/sitemap"
)

// Meta is returned by a handler next to its data.
type Meta struct {
	Canonical string // overrides the computed canonical URL
	NoIndex   bool   // adds X-Robots-Tag: noindex
}

// Handler loads data for a dynamic route. Return ErrNotFound, ErrGone or a
// *RedirectError to produce 404 / 410 / 301-302; anything else is a 500.
type Handler func(r *http.Request, params map[string]string) (data any, meta Meta, err error)

var (
	ErrNotFound = errors.New("hearth: not found")
	ErrGone     = errors.New("hearth: gone")
)

// RedirectError asks the site to redirect (301 resolver for renamed slugs).
type RedirectError struct {
	Code int
	To   string
}

func (e *RedirectError) Error() string { return fmt.Sprintf("hearth: redirect %d to %s", e.Code, e.To) }

// Redirect builds a *RedirectError. Code must be 301, 302, 307 or 308.
func Redirect(code int, to string) error { return &RedirectError{Code: code, To: to} }

// I18n selects how languages map to URLs. Paths are identical in every language.
type I18n struct {
	// Mode is "prefix" (default: non-default languages under /<lang>/...), "subdomain" or "domain".
	Mode string
	// Hosts maps each language to its host, for the subdomain and domain modes.
	Hosts map[string]string
}

// Options configures a Site.
type Options struct {
	Bundle      fs.FS // dist/templates (embed.FS sub or os.DirFS)
	CacheDir    string
	BaseURL     string // https://example.com, no trailing slash; used for canonical, hreflang, sitemap
	DefaultLang string // default "en"
	I18n        I18n

	MaxConcurrent int           // concurrent renders; default 8
	RenderTimeout time.Duration // default 10s
	CacheControl  string        // default "public, max-age=0, must-revalidate" ("no-store" in Dev)
	KeepVersions  int           // old cache versions kept on disk after a flip; default 2
	Logger        *slog.Logger

	// Dev never reads or writes the cache, and manifest.json may be missing.
	Dev bool
	// DevAssetURL is the Vite dev server; in Dev, {{asset "x"}} resolves to DevAssetURL + "/x" and
	// templates see .Dev == true. Without it, Dev uses the bundle manifest like a production site.
	DevAssetURL string
	// LenientAssets makes unknown assets and images resolve to placeholders instead of failing
	// the render (used by `hearth check`, which runs without a Vite build).
	LenientAssets bool
	// Fallback answers requests that match no page (for example the Vite dev server).
	Fallback http.Handler

	// Funcs adds template functions next to the built-in helpers (t, plural, asset, entry, picture,
	// json, data). A name may not shadow a helper or a Go template builtin, and each value must
	// be a func with one result, or a result plus error; New reports violations. A returned
	// error fails the render, so no partial page is ever cached.
	//
	// Functions must be pure and safe for concurrent use: pages render in parallel, and the
	// same output is cached for everyone. A function may return template.HTML, which is not
	// escaped: build it only from trusted values (numbers formatted with strconv), never from
	// text a user wrote.
	Funcs template.FuncMap

	// SSR names parameterless pages ("/candidates") served by handlers, in addition to the
	// ones the bundle's manifest lists. A production server needs nothing here: `hearth build`
	// records the ssr list from hearth.config.json in manifest.json. It is for tools that run
	// a Site over a source tree with no manifest (hearth check, hearth dev).
	SSR []string

	// SitemapURLs adds language-neutral pages (such as "/post/42") to the sitemap, each with an
	// optional last modification time. It is also what Build prerenders and Warm warms for
	// pages with route parameters. Return only dates you know (a row's updated_at); a zero
	// LastMod writes no <lastmod>, and Hearth never invents one. The same path listed twice keeps
	// the latest LastMod.
	SitemapURLs func(ctx context.Context) ([]SitemapEntry, error)

	// SitemapEntries adds language-neutral paths to the sitemap, without lastmod.
	//
	// Deprecated: use SitemapURLs. Both may be set; their paths are merged.
	SitemapEntries func(ctx context.Context) ([]string, error)
}

// SitemapEntry is one extra sitemap page: a language-neutral path and when it last changed.
type SitemapEntry struct {
	Path    string
	LastMod time.Time // zero: unknown, no <lastmod> is written
}

// sitemapEntries merges SitemapURLs and SitemapEntries into one list sorted by path, one entry per
// path with the latest LastMod.
func (s *Site) sitemapEntries(ctx context.Context) ([]SitemapEntry, error) {
	var all []SitemapEntry
	if s.opt.SitemapEntries != nil {
		paths, err := s.opt.SitemapEntries(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			all = append(all, SitemapEntry{Path: p})
		}
	}
	if s.opt.SitemapURLs != nil {
		urls, err := s.opt.SitemapURLs(ctx)
		if err != nil {
			return nil, err
		}
		all = append(all, urls...)
	}
	latest := make(map[string]time.Time, len(all))
	for _, e := range all {
		if t, ok := latest[e.Path]; !ok || e.LastMod.After(t) {
			latest[e.Path] = e.LastMod
		}
	}
	out := make([]SitemapEntry, 0, len(latest))
	for p, t := range latest {
		out = append(out, SitemapEntry{Path: p, LastMod: t})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Counters are monotonic counters exposed for /healthz and metrics.
type Counters struct {
	Hits, Renders, Errors, NotFound, Redirects, StaleSkips atomic.Int64
}

// Site is an http.Handler that serves cached pages and renders on a miss.
type Site struct {
	opt        Options
	routes     *route.Table
	renderer   *render.Renderer
	cache      *cache.Cache
	urls       i18n.URLs
	handlers   map[string]Handler
	ssr        map[string]bool           // parameterless page patterns served by handlers
	bundleVer  string                    // manifest build id; cache versions are "<bundleVer>.<data version>"
	sitemapMax struct{ urls, bytes int } // zero: the protocol's limits (50,000 URLs, 50 MB); tests lower them
	flight     singleflight.Group
	sem        *semaphore.Weighted
	log        *slog.Logger
	Counters   Counters
}

// New builds a Site from a bundle. Handlers are registered afterwards with Handle.
func New(o Options) (*Site, error) {
	if o.Bundle == nil || o.CacheDir == "" {
		return nil, errors.New("hearth: Bundle and CacheDir are required")
	}
	if o.DefaultLang == "" {
		o.DefaultLang = "en"
	}
	if o.MaxConcurrent <= 0 {
		o.MaxConcurrent = 8
	}
	if o.RenderTimeout <= 0 {
		o.RenderTimeout = 10 * time.Second
	}
	if o.KeepVersions <= 0 {
		o.KeepVersions = 2
	}
	if o.CacheControl == "" {
		o.CacheControl = "public, max-age=0, must-revalidate"
		if o.Dev {
			o.CacheControl = "no-store"
		}
	}
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	o.DevAssetURL = strings.TrimRight(o.DevAssetURL, "/")
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	man, err := manifest.Load(o.Bundle, "manifest.json")
	if err != nil {
		if !(o.Dev || o.LenientAssets) || !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		man = &manifest.Manifest{Format: manifest.BundleFormat, Assets: map[string]string{}, Entries: map[string]manifest.Entry{}, Images: map[string][]manifest.ImageFile{}}
	}
	if o.Dev || o.LenientAssets {
		man.Lenient = true
	}
	if o.Dev && o.DevAssetURL != "" {
		man.DevBase = o.DevAssetURL
	}
	routes, err := route.Discover(o.Bundle, "pages")
	if err != nil {
		return nil, err
	}
	var cats *i18n.Catalogs
	if _, err := fs.Stat(o.Bundle, "locales"); err == nil {
		if cats, err = i18n.Load(o.Bundle, "locales"); err != nil {
			return nil, err
		}
	}
	langs := []string{o.DefaultLang}
	if cats != nil {
		langs = cats.Langs()
		if !cats.Has(o.DefaultLang) {
			return nil, fmt.Errorf("hearth: defaultLang %q has no locales/%s.json", o.DefaultLang, o.DefaultLang)
		}
	}
	urls, err := newURLs(o, langs)
	if err != nil {
		return nil, err
	}
	var dataFS fs.FS
	if st, err := fs.Stat(o.Bundle, "data"); err == nil && st.IsDir() {
		if dataFS, err = fs.Sub(o.Bundle, "data"); err != nil {
			return nil, err
		}
	}
	ssr := map[string]bool{}
	for _, p := range append(append([]string{}, man.SSR...), o.SSR...) {
		pg, ok := routes.Lookup(p)
		switch {
		case !strings.HasPrefix(p, "/") || !ok:
			return nil, fmt.Errorf("hearth: ssr page %q matches no template (have: %s)", p, strings.Join(routes.Patterns(), ", "))
		case pg.IsDynamic():
			return nil, fmt.Errorf("hearth: ssr page %q has route parameters; only parameterless pages are listed in ssr", p)
		}
		ssr[pg.Pattern] = true
	}
	rend, err := render.New(render.Options{FS: o.Bundle, Routes: routes, Manifest: man, Catalogs: cats, Data: dataFS, Funcs: o.Funcs})
	if err != nil {
		return nil, err
	}
	pageCache, err := cache.New(o.CacheDir)
	if err != nil {
		return nil, err
	}
	bundleVer := man.Version
	if !cache.ValidVersion(bundleVer) {
		bundleVer = "dev"
	}
	// A new bundle (new templates, new asset hashes) must never be served pages rendered by
	// the old one. A restart with the same bundle keeps its warm cache.
	if cur, err := pageCache.Version(); err != nil || !strings.HasPrefix(cur, bundleVer+".") {
		if err := pageCache.SetVersion(bundleVer + ".0"); err != nil {
			return nil, err
		}
		_ = pageCache.Prune(o.KeepVersions)
	}
	return &Site{opt: o, routes: routes, renderer: rend, cache: pageCache, urls: urls, handlers: map[string]Handler{}, ssr: ssr,
		bundleVer: bundleVer, sem: semaphore.NewWeighted(int64(o.MaxConcurrent)), log: o.Logger}, nil
}

func newURLs(o Options, langs []string) (i18n.URLs, error) {
	u := i18n.URLs{Mode: o.I18n.Mode, Hosts: o.I18n.Hosts, Default: o.DefaultLang, BaseURL: o.BaseURL, Langs: langs}
	switch u.Mode {
	case "":
		u.Mode = "prefix"
	case "prefix":
	case "subdomain", "domain":
		for _, l := range langs {
			if h := u.Hosts[l]; !cache.ValidHost(h) {
				return u, fmt.Errorf("hearth: I18n.Hosts[%q] = %q is not a valid host", l, h)
			}
		}
	default:
		return u, fmt.Errorf("hearth: unknown I18n.Mode %q", u.Mode)
	}
	return u, nil
}

// Handle registers an SSR handler by pattern. The pattern must match a discovered
// template; a typo is reported here (and by `hearth check`) instead of at request time.
//
// A page without route parameters ("/candidates") takes a handler only when it is listed in the
// config's ssr list: otherwise `hearth build` prerenders it and the file would shadow the handler.
func (s *Site) Handle(pattern string, h Handler) error {
	if err := s.routes.CheckHandler(pattern); err != nil {
		return err
	}
	if pg, _ := s.routes.Lookup(pattern); !pg.IsDynamic() && !s.ssr[pg.Pattern] {
		return fmt.Errorf("hearth: handler for %q would be shadowed by the prebuilt file: list %q in \"ssr\" in hearth.config.json and rebuild", pattern, pg.URL())
	}
	s.handlers[strings.TrimPrefix(pattern, "/")] = h
	return nil
}

// Validate reports ssr pages that have no handler yet; they answer 404 until one is registered.
// Call it after the Handle calls at startup and log or fail on the result.
func (s *Site) Validate() error {
	var missing []string
	for pat := range s.ssr {
		if _, ok := s.handlers[pat]; !ok {
			missing = append(missing, "/"+pat)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return fmt.Errorf("hearth: ssr pages without a handler (they answer 404): %s", strings.Join(missing, ", "))
	}
	return nil
}

// MustHandle is Handle that panics at startup on a bad pattern.
func (s *Site) MustHandle(pattern string, h Handler) {
	if err := s.Handle(pattern, h); err != nil {
		panic(err)
	}
}

// SetVersion invalidates the cache: flips to version v; pages refill lazily.
func (s *Site) SetVersion(v string) error {
	if !cache.ValidVersion(v) {
		return fmt.Errorf("hearth: invalid version %q", v)
	}
	if err := s.cache.SetVersion(s.bundleVer + "." + v); err != nil {
		return err
	}
	if err := s.cache.Prune(s.opt.KeepVersions); err != nil {
		s.log.Warn("prune old cache versions", "err", err)
	}
	s.log.Info("cache version set", "version", v, "bundle", s.bundleVer)
	return nil
}

// Invalidate drops cached pages by language-neutral path ("/post/42"), in every language, and
// leaves the rest of the cache alone. Pages refill lazily on the next request.
//
// Call it after the data change is visible to your handlers (after the database commit). A
// render already in flight when Invalidate runs is not cached, and new requests do not join it.
// Use SetVersion for a bulk change; Invalidate is for known pages.
func (s *Site) Invalidate(paths ...string) error {
	var errs []error
	ver, _ := s.cache.Version()
	for _, p := range paths {
		if !strings.HasPrefix(p, "/") {
			errs = append(errs, fmt.Errorf("hearth: Invalidate %q: path must start with /", p))
			continue
		}
		for _, lang := range s.urls.Langs {
			host, _ := s.urls.Location(lang)
			key := s.urls.Path(lang, p)
			if err := s.cache.Invalidate(host, key); err != nil {
				errs = append(errs, fmt.Errorf("hearth: Invalidate %q: %w", key, err))
				continue
			}
			s.flight.Forget(ver + "|" + host + "|" + key)
		}
	}
	return errors.Join(errs...)
}

// Source reports external changes (the server decides what triggers one).
type Source interface {
	Watch(ctx context.Context, notify func(version string)) error
}

// Subscribe runs src.Watch until ctx ends, flipping the version on each notification.
func (s *Site) Subscribe(ctx context.Context, src Source) error {
	return src.Watch(ctx, func(v string) {
		if err := s.SetVersion(v); err != nil {
			s.log.Error("set version", "err", err)
		}
	})
}

type pollSource struct {
	fn       func(ctx context.Context) (string, error)
	interval time.Duration
}

// Poll is a stdlib-only Source: it calls fn every interval and notifies when the string changes.
func Poll(fn func(ctx context.Context) (string, error), interval time.Duration) Source {
	return &pollSource{fn: fn, interval: interval}
}

func (p *pollSource) Watch(ctx context.Context, notify func(string)) error {
	var last string
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		if v, err := p.fn(ctx); err == nil && v != last {
			if last != "" {
				notify(v)
			}
			last = v
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

type rendered struct {
	body      []byte
	status    int
	to        string
	noindex   bool
	note      string // why a 404 happened, shown in Dev
	unmatched bool   // no page matched the request (Options.Fallback gets those)
	version   string
}

func (s *Site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		s.healthz(w)
		return
	case "/robots.txt":
		s.robots(w, r)
		return
	case "/sitemap.xml":
		s.sitemap(w, r, 0)
		return
	}
	if n, ok := sitemapPart(r.URL.Path); ok {
		s.sitemap(w, r, n)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	res, err := s.resolve(r)
	if err != nil {
		s.Counters.Errors.Add(1)
		s.log.Error("render failed", "path", r.URL.Path, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	switch {
	case res.unmatched && s.opt.Fallback != nil:
		s.opt.Fallback.ServeHTTP(w, r)
	case res.to != "":
		s.Counters.Redirects.Add(1)
		http.Redirect(w, r, res.to, res.status)
	case res.status == http.StatusOK:
		s.write(w, r, http.StatusOK, res.body, res.noindex)
	default:
		s.Counters.NotFound.Add(1)
		s.writeError(w, r, res)
	}
}

// writeError answers a 404 or 410: the page's error template inside the layout when the bundle has
// one and the language is known, plain text otherwise. These bodies are never cached anywhere.
func (s *Site) writeError(w http.ResponseWriter, r *http.Request, res *rendered) {
	if lang, _, ok := s.urls.Resolve(r.Host, r.URL.Path); ok {
		if body, found, err := s.errorBody(lang, res.status); err != nil {
			s.log.Error("render error page", "status", res.status, "err", err)
		} else if found {
			h := w.Header()
			h.Set("Content-Type", "text/html; charset=utf-8")
			h.Set("Cache-Control", "no-store")
			h.Set("X-Robots-Tag", "noindex")
			w.WriteHeader(res.status)
			if r.Method != http.MethodHead {
				_, _ = w.Write(body)
			}
			return
		}
	}
	msg := http.StatusText(res.status)
	if s.opt.Dev && res.note != "" {
		msg = res.note
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, msg, res.status)
}

// ErrorPage renders pages/404.html or pages/410.html (status 404 or 410) for lang, the body Site
// answers with. found is false when the bundle has no such template. `hearth check` uses it to
// trial-render the templates, which no route reaches.
func (s *Site) ErrorPage(lang string, status int) (body []byte, found bool, err error) {
	return s.errorBody(lang, status)
}

// Langs lists the site's languages, sorted.
func (s *Site) Langs() []string { return append([]string(nil), s.urls.Langs...) }

// errorBody renders pages/<status>.html (or its language override) for lang. found is false when
// the bundle has no such template. The page is noindex and carries no canonical or hreflang links.
func (s *Site) errorBody(lang string, status int) (body []byte, found bool, err error) {
	pg, ok := s.routes.ErrorPage(status)
	if !ok {
		return nil, false, nil
	}
	dir := "ltr"
	if i18n.IsRTL(lang) {
		dir = "rtl"
	}
	_, prefix := s.urls.Location(lang)
	body, err = s.renderer.Render(pg, render.PageData{Lang: lang, Dir: dir, Params: map[string]string{}, Prefix: prefix,
		Status: status, NoIndex: true, Dev: s.opt.Dev && s.opt.DevAssetURL != ""})
	return body, err == nil, err
}

// resolve is the one request path: ServeHTTP and Prerender both go through it, so a page is
// the same whether a visitor triggered it or the build did. A non-nil error is a failed render.
func (s *Site) resolve(r *http.Request) (*rendered, error) {
	lang, urlPath, ok := s.urls.Resolve(r.Host, r.URL.Path)
	if !ok {
		return &rendered{status: http.StatusNotFound, unmatched: true}, nil
	}
	if _, err := cache.RelPath("", urlPath); err != nil {
		return &rendered{status: http.StatusNotFound, unmatched: true}, nil
	}
	if strings.HasSuffix(r.URL.Path, "/") && r.URL.Path != "/" {
		return &rendered{status: http.StatusMovedPermanently, to: strings.TrimRight(r.URL.Path, "/")}, nil
	}
	host, _ := s.urls.Location(lang)
	cacheKey := r.URL.Path
	if !s.opt.Dev {
		if p, err := s.cache.Path(host, cacheKey); err == nil {
			if b, err := os.ReadFile(p); err == nil {
				s.Counters.Hits.Add(1)
				return &rendered{body: b, status: http.StatusOK}, nil
			}
		}
	}
	ver, _ := s.cache.Version()
	key := ver + "|" + host + "|" + cacheKey
	v, err, _ := s.flight.Do(key, func() (any, error) { return s.render(r, lang, urlPath, host, cacheKey, ver) })
	if err != nil {
		return nil, err
	}
	return v.(*rendered), nil
}

func (s *Site) render(r *http.Request, lang, urlPath, host, cacheKey, ver string) (*rendered, error) {
	pg, params, ok := s.routes.Match(urlPath)
	if !ok {
		return &rendered{status: http.StatusNotFound, unmatched: true}, nil
	}
	// Before anything reads data: an Invalidate after this point makes the write below skip.
	gen, _ := s.cache.Gen(host, cacheKey)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), s.opt.RenderTimeout)
	defer cancel()
	if err := s.sem.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	defer s.sem.Release(1)

	var data any
	var meta Meta
	if h, ok := s.handlers[pg.Pattern]; ok || pg.IsDynamic() || s.ssr[pg.Pattern] {
		if !ok {
			return &rendered{status: http.StatusNotFound}, nil // SSR-only page without handler
		}
		var err error
		data, meta, err = h(r.WithContext(ctx), params)
		var re *RedirectError
		switch {
		case errors.Is(err, ErrNotFound):
			return &rendered{status: http.StatusNotFound, note: err.Error()}, nil
		case errors.Is(err, ErrGone):
			return &rendered{status: http.StatusGone}, nil
		case errors.As(err, &re):
			switch re.Code {
			case 301, 302, 307, 308:
				return &rendered{status: re.Code, to: re.To}, nil
			}
			return nil, fmt.Errorf("invalid redirect code %d", re.Code)
		case err != nil:
			return nil, err
		}
	}
	body, err := s.renderer.Render(pg, s.pageData(lang, urlPath, params, data, meta))
	if err != nil {
		return nil, err
	}
	s.Counters.Renders.Add(1)
	// Only 200s reach this point; write under the version the render began with.
	if !s.opt.Dev {
		switch ok, err := s.cache.WriteGen(ver, host, cacheKey, gen, body); {
		case err != nil:
			s.log.Error("cache write", "path", cacheKey, "err", err)
		case !ok:
			s.Counters.StaleSkips.Add(1) // invalidated mid-render: serve it, do not keep it
		}
	}
	return &rendered{body: body, status: http.StatusOK, noindex: meta.NoIndex, version: ver}, nil
}

// pageData is the single place that decides what a template sees about its language and address.
func (s *Site) pageData(lang, neutral string, params map[string]string, data any, meta Meta) render.PageData {
	canonical := meta.Canonical
	if canonical == "" && s.urls.Origin(lang) != "" {
		canonical = s.urls.Abs(lang, neutral)
	}
	dir := "ltr"
	if i18n.IsRTL(lang) {
		dir = "rtl"
	}
	_, prefix := s.urls.Location(lang)
	return render.PageData{Lang: lang, Dir: dir, Params: params, Data: data, Canonical: canonical, Status: http.StatusOK, NoIndex: meta.NoIndex,
		Alternates: s.urls.Alternates(neutral), Prefix: prefix, Dev: s.opt.Dev && s.opt.DevAssetURL != ""}
}

func (s *Site) write(w http.ResponseWriter, r *http.Request, status int, body []byte, noindex bool) {
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", s.opt.CacheControl)
	h.Set("ETag", etag)
	if noindex {
		h.Set("X-Robots-Tag", "noindex")
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func (s *Site) healthz(w http.ResponseWriter) {
	v, _ := s.cache.Version()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, "ok\nversion %s\nhits %d\nrenders %d\nerrors %d\nnotfound %d\nredirects %d\n", v,
		s.Counters.Hits.Load(), s.Counters.Renders.Load(), s.Counters.Errors.Load(),
		s.Counters.NotFound.Load(), s.Counters.Redirects.Load())
}

func (s *Site) robots(w http.ResponseWriter, r *http.Request) {
	lang, _, ok := s.urls.Resolve(r.Host, "/robots.txt")
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "User-agent: *\nAllow: /\n")
	if origin := s.urls.Origin(lang); origin != "" {
		fmt.Fprintf(w, "Sitemap: %s/sitemap.xml\n", origin)
	}
}

// sitemapPart parses "/sitemap-N.xml" (N >= 1).
func sitemapPart(p string) (int, bool) {
	rest, ok := strings.CutPrefix(p, "/sitemap-")
	if !ok {
		return 0, false
	}
	num, ok := strings.CutSuffix(rest, ".xml")
	if !ok || num == "" || num[0] == '0' || len(num) > 9 {
		return 0, false
	}
	n := 0
	for _, c := range num {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// sitemap lists every page in every language of the host being asked (all languages in prefix
// mode, the host's own language in subdomain/domain mode). part 0 is /sitemap.xml: the whole
// list, or, when it exceeds 50,000 URLs or 50 MB, a sitemapindex of /sitemap-1.xml, /sitemap-2.xml,
// ... which are part 1, 2, ... of the same sorted list. A part that does not exist is a 404, and
// so is every part while the list fits one file.
func (s *Site) sitemap(w http.ResponseWriter, r *http.Request, part int) {
	name := "/sitemap.xml"
	if part > 0 {
		name = fmt.Sprintf("/sitemap-%d.xml", part)
	}
	lang, _, ok := s.urls.Resolve(r.Host, name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	langs := s.urls.Langs
	if s.urls.HostMode() {
		langs = []string{lang}
	}
	var paths []SitemapEntry
	for _, pat := range s.routes.Patterns() {
		pg, _ := s.routes.Lookup(pat)
		if !pg.IsDynamic() {
			paths = append(paths, SitemapEntry{Path: pg.URL()})
		}
	}
	extra, err := s.sitemapEntries(r.Context())
	if err != nil {
		s.log.Error("sitemap entries", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	paths = append(paths, extra...)
	byURL := map[string]time.Time{}
	for _, p := range paths {
		for _, l := range langs {
			u := s.urls.Abs(l, p.Path)
			if t, seen := byURL[u]; !seen || p.LastMod.After(t) {
				byURL[u] = p.LastMod
			}
		}
	}
	urls := make([]sitemap.URL, 0, len(byURL))
	for u, t := range byURL {
		urls = append(urls, sitemap.URL{Loc: u, LastMod: t})
	}
	sort.Slice(urls, func(i, j int) bool { return urls[i].Loc < urls[j].Loc })
	parts := sitemap.Split(urls, s.sitemapMax.urls, s.sitemapMax.bytes)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	switch {
	case len(parts) == 1 && part == 0:
		fmt.Fprint(w, sitemap.XML(parts[0]))
	case part == 0:
		locs := make([]string, len(parts))
		for i := range parts {
			locs[i] = fmt.Sprintf("%s/sitemap-%d.xml", s.urls.Origin(lang), i+1)
		}
		fmt.Fprint(w, sitemap.Index(locs))
	case part <= len(parts) && len(parts) > 1:
		fmt.Fprint(w, sitemap.XML(parts[part-1]))
	default:
		w.Header().Del("Content-Type")
		http.NotFound(w, r)
	}
}
