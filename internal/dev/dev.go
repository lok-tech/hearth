package dev

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/lok-tech/hearth/internal/cache"
	"github.com/lok-tech/hearth/internal/config"
	"github.com/lok-tech/hearth/internal/mock"
	"github.com/lok-tech/hearth/internal/route"
	"github.com/lok-tech/hearth/pkg/hearth"
)

func parseProxyTarget(raw, what string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("hearth: %s %q must be an http(s) URL", what, raw)
	}
	return u, nil
}

// proxyTransport lets tests route proxied requests in memory; nil uses the default transport.
var proxyTransport http.RoundTripper

func newProxy(target *url.URL) *httputil.ReverseProxy {
	p := httputil.NewSingleHostReverseProxy(target)
	p.Transport = proxyTransport
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "hearth: upstream "+target.Host+" unreachable: "+err.Error(), http.StatusBadGateway)
	}
	return p
}

// NewLive proxies everything to the project's own Go server, which renders
// with the same templates (`hearth dev --live`).
func NewLive(server string) (http.Handler, error) {
	u, err := parseProxyTarget(server, "dev.server")
	if err != nil {
		return nil, err
	}
	return newProxy(u), nil
}

// NewPreview runs the production flow locally: a prebuilt file if one
// exists, otherwise the server renders (and writes its cache) and answers.
func NewPreview(publicDir, server string) (http.Handler, error) {
	u, err := parseProxyTarget(server, "dev.server")
	if err != nil {
		return nil, err
	}
	proxy := newProxy(u)
	files := http.FileServer(http.Dir(publicDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if rel, err := cache.RelPath("", r.URL.Path); err == nil {
				if st, err := os.Stat(filepath.Join(publicDir, rel)); err == nil && !st.IsDir() {
					r2 := r.Clone(r.Context())
					r2.URL.Path = strings.TrimSuffix(filepath.ToSlash(rel), "index.html")
					files.ServeHTTP(w, r2)
					return
				}
			}
			clean := filepath.Join(publicDir, filepath.FromSlash(strings.TrimPrefix(pathClean(r.URL.Path), "/")))
			if filepath.Ext(r.URL.Path) != "" && within(publicDir, clean) {
				if st, err := os.Stat(clean); err == nil && !st.IsDir() {
					files.ServeHTTP(w, r)
					return
				}
			}
		}
		proxy.ServeHTTP(w, r)
	}), nil
}

func pathClean(p string) string { return filepath.ToSlash(filepath.Clean("/" + p)) }

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// NewMock serves the source tree through a hearth.Site in Dev mode, rebuilt on every request so
// edits to templates, locales and mock files show without a restart. It is the same engine a
// production server runs; only the data differs. Paths under a dev.proxy prefix go to their backend first. Dynamic pages read pages/<path>.mock.json: an
// object mapping the parameter value to the page data, with "*" as the fallback. Paths that
// match no page go to the Vite dev server when configured (HMR assets).
func NewMock(siteDir string, cfg *config.Config) (http.Handler, error) {
	h, err := newMock(siteDir, cfg, nil)
	if err != nil {
		return nil, err
	}
	return withProxy(cfg.Dev.Proxy, h)
}

// newMock is NewMock with an optional watcher: when set, assets come from its Vite build output
// (hashed files, served by this handler) instead of a Vite dev server.
func newMock(siteDir string, cfg *config.Config, wa *watcher) (http.Handler, error) {
	var vite http.Handler
	devBase := ""
	if wa == nil && cfg.Vite != nil && cfg.Vite.DevURL != "" {
		u, err := parseProxyTarget(cfg.Vite.DevURL, "vite.devURL")
		if err != nil {
			return nil, err
		}
		vite, devBase = newProxy(u), strings.TrimRight(cfg.Vite.DevURL, "/")
	}
	cacheDir, err := os.MkdirTemp("", "hearth-dev-") // required by Site, never read or written in Dev
	if err != nil {
		return nil, err
	}
	src := os.DirFS(siteDir)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var bundle fs.FS = src
		if wa != nil {
			bundle = overlay{FS: src, manifest: wa.manifestJSON()}
		}
		opt := hearth.Options{
			Bundle:      bundle,
			CacheDir:    cacheDir,
			BaseURL:     cfg.BaseURL,
			DefaultLang: cfg.DefaultLang,
			I18n:        hearth.I18n{Mode: cfg.I18n.Mode, Hosts: cfg.I18n.Hosts},
			Dev:         true,
			DevAssetURL: devBase,
			SSR:         cfg.SSR,
			Funcs:       mock.Stubs(cfg.Funcs),
			Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		}
		if vite != nil {
			opt.Fallback = vite
		}
		if wa != nil {
			opt.Fallback = wa.static()
		}
		site, err := hearth.New(opt)
		if err != nil {
			http.Error(w, "hearth: "+err.Error(), http.StatusInternalServerError)
			return
		}
		routes, err := route.Discover(src, "pages")
		if err != nil {
			http.Error(w, "hearth: "+err.Error(), http.StatusInternalServerError)
			return
		}
		ssr := map[string]bool{}
		for _, p := range cfg.SSR {
			ssr[strings.TrimPrefix(p, "/")] = true
		}
		for _, pat := range routes.Patterns() {
			if pg, _ := routes.Lookup(pat); pg.IsDynamic() || ssr[pg.Pattern] {
				if err := site.Handle(pat, mock.Handler(src, pg)); err != nil {
					http.Error(w, "hearth: "+err.Error(), http.StatusInternalServerError)
					return
				}
			}
		}
		site.ServeHTTP(w, r)
	}), nil
}
