package hearth

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/lok-tech/hearth/internal/fsutil"
)

// Build writes the static site of s into outDir: every parameterless page that is not in the ssr
// list, plus every path Options.SitemapURLs (or SitemapEntries) names, in every language, followed by sitemap.xml
// and robots.txt per host when BaseURL is set (an existing robots.txt in outDir wins).
// It is Prerender plus Export, the same calls `hearth build` makes, so a program that registers
// its own Funcs and handlers gets a build identical to what it serves. ssr pages are never
// written: a file there would shadow their handler.
//
// Any page that does not answer 200 fails the build; the error names the first and counts the rest.
// The prerendered files stay in the site's active cache version too.
func Build(ctx context.Context, s *Site, outDir string) (PrerenderReport, error) {
	if !s.keepsPages() { // fail before rendering every page for nothing
		return PrerenderReport{}, errCacheOff
	}
	paths := map[string]bool{}
	for _, pat := range s.routes.Patterns() {
		if pg, _ := s.routes.Lookup(pat); !pg.IsDynamic() && !s.ssr[pg.Pattern] {
			paths[pg.URL()] = true
		}
	}
	extra, err := s.sitemapEntries(ctx)
	if err != nil {
		return PrerenderReport{}, fmt.Errorf("hearth: sitemap entries: %w", err)
	}
	for _, e := range extra {
		paths[e.Path] = true
	}
	list := make([]string, 0, len(paths))
	for p := range paths {
		list = append(list, p)
	}
	sort.Strings(list)
	rep := s.Prerender(ctx, list, runtime.NumCPU())
	if n := len(rep.Failures); n > 0 {
		if n > 1 {
			return rep, fmt.Errorf("%w (and %d more pages failed)", rep.Failures[0], n-1)
		}
		return rep, rep.Failures[0]
	}
	if err := s.Export(outDir); err != nil {
		return rep, err
	}
	if err := s.writeErrorPage(outDir); err != nil {
		return rep, err
	}
	if s.opt.BaseURL != "" {
		if err := s.writeSitemapAndRobots(outDir); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// writeErrorPage saves the rendered 404 template as 404.html, the file a web server serves for a
// missing path (Caddy handle_errors, nginx error_page). Nothing is written when the bundle has no
// pages/404.html. A stale 404.html in outDir is replaced.
//
// Layout of the files: one 404.html at the root of the default language (prefix mode, where the
// root serves every language) or in each host's directory (subdomain and domain modes, one per
// language). Prefix mode also writes <lang>/404.html for each other language, so a server can
// answer a missing /he/... path in Hebrew; a server that only knows one file uses the root one.
func (s *Site) writeErrorPage(dst string) error {
	type target struct{ lang, file string }
	var targets []target
	if s.urls.HostMode() {
		for _, l := range s.urls.Langs {
			h, _ := s.urls.Location(l)
			targets = append(targets, target{l, filepath.Join(h, "404.html")})
		}
	} else {
		targets = append(targets, target{s.urls.Default, "404.html"})
		for _, l := range s.urls.Langs {
			if l != s.urls.Default {
				targets = append(targets, target{l, filepath.Join(l, "404.html")})
			}
		}
	}
	for _, t := range targets {
		body, found, err := s.errorBody(t.lang, http.StatusNotFound)
		if err != nil {
			return fmt.Errorf("hearth: render 404 page [%s]: %w", t.lang, err)
		}
		if !found {
			return nil
		}
		if err := fsutil.WriteFileAtomic(filepath.Join(dst, t.file), body); err != nil {
			return err
		}
	}
	return nil
}

// hosts lists the host directories of the output: one root, or one per language host.
func (s *Site) hosts() []string {
	if !s.urls.HostMode() {
		return []string{""}
	}
	seen := map[string]bool{}
	var hosts []string
	for _, l := range s.urls.Langs {
		if h, _ := s.urls.Location(l); !seen[h] {
			seen[h] = true
			hosts = append(hosts, h)
		}
	}
	sort.Strings(hosts)
	return hosts
}

// bufWriter records a response, so the site's own answers can be saved as files.
type bufWriter struct {
	h    http.Header
	code int
	body []byte
}

func (b *bufWriter) Header() http.Header         { return b.h }
func (b *bufWriter) WriteHeader(c int)           { b.code = c }
func (b *bufWriter) Write(p []byte) (int, error) { b.body = append(b.body, p...); return len(p), nil }

// writeSitemapAndRobots saves what the Site itself answers for /sitemap.xml and /robots.txt,
// once per host, so the static files match the server's.
func (s *Site) writeSitemapAndRobots(dst string) error {
	for _, host := range s.hosts() {
		dir := dst
		if host != "" {
			dir = filepath.Join(dst, host)
		}
		for _, name := range []string{"sitemap.xml", "robots.txt"} {
			target := filepath.Join(dir, name)
			if _, err := os.Stat(target); err == nil {
				continue // a file shipped in public/ wins (and then so does its own idea of parts)
			}
			body, err := s.answer(host, "/"+name)
			if err != nil {
				return err
			}
			if err := fsutil.WriteFileAtomic(target, body); err != nil {
				return err
			}
			// Above the sitemap limits /sitemap.xml is an index; write the files it points at.
			if name == "sitemap.xml" && bytes.Contains(body, []byte("<sitemapindex")) {
				for n := 1; n <= bytes.Count(body, []byte("<sitemap>")); n++ {
					part := fmt.Sprintf("sitemap-%d.xml", n)
					pb, err := s.answer(host, "/"+part)
					if err != nil {
						return err
					}
					if err := fsutil.WriteFileAtomic(filepath.Join(dir, part), pb); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// answer returns the body the site serves for GET path on host; anything but 200 is an error.
func (s *Site) answer(host, path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	req.Host = host
	w := &bufWriter{h: http.Header{}, code: http.StatusOK}
	s.ServeHTTP(w, req)
	if w.code != http.StatusOK {
		return nil, fmt.Errorf("hearth: %s answered %d", path, w.code)
	}
	return w.body, nil
}
