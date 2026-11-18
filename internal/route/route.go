package route

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Page is a template discovered under pages/.
type Page struct {
	Pattern string            // route pattern without leading slash, e.g. "post/{id}", "" for index
	Params  []string          // parameter names in order
	File    string            // default-language template path inside the bundle
	Langs   map[string]string // per-language override templates, lang -> path
}

// IsDynamic reports whether the page has route parameters.
func (p *Page) IsDynamic() bool { return len(p.Params) > 0 }

// MockFile is the dev-mode data file next to the template: pages/post/[id].mock.json.
func (p *Page) MockFile() string { return strings.TrimSuffix(p.File, ".html") + ".mock.json" }

// URL returns the pattern as a URL path ("/post/{id}").
func (p *Page) URL() string { return "/" + p.Pattern }

// Table is the set of pages discovered in a bundle.
type Table struct {
	pages map[string]*Page
}

// Discover walks pagesDir in fsys. File path is the route:
// about.html -> about, index.html -> "", post/[id].html -> post/{id}.
// "name.<lang>.html" registers a language override of "name.html".
func Discover(fsys fs.FS, pagesDir string) (*Table, error) {
	r := &Table{pages: map[string]*Page{}}
	type found struct{ file, pattern, lang string }
	var all []found
	err := fs.WalkDir(fsys, pagesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, pagesDir), "/")
		pattern, lang, err := fileToPattern(rel)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		all = append(all, found{p, pattern, lang})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].file < all[j].file })
	for _, f := range all {
		if f.lang != "" {
			continue
		}
		if other, dup := r.pages[f.pattern]; dup {
			return nil, fmt.Errorf("duplicate route %q: %s and %s", "/"+f.pattern, other.File, f.file)
		}
		r.pages[f.pattern] = &Page{Pattern: f.pattern, Params: paramNames(f.pattern), File: f.file, Langs: map[string]string{}}
	}
	for _, f := range all {
		if f.lang == "" {
			continue
		}
		pg, ok := r.pages[f.pattern]
		if !ok {
			return nil, fmt.Errorf("%s: language override has no base template", f.file)
		}
		pg.Langs[f.lang] = f.file
	}
	return r, nil
}

// fileToPattern converts "post/[id].he.html" into ("post/{id}", "he").
func fileToPattern(rel string) (pattern, lang string, err error) {
	name := strings.TrimSuffix(rel, ".html")
	dir, base := path.Split(name)
	if i := strings.LastIndex(base, "."); i >= 0 {
		lang = base[i+1:]
		base = base[:i]
		if lang == "" || strings.Contains(lang, ".") {
			return "", "", fmt.Errorf("bad file name")
		}
	}
	if strings.Contains(base, ".mock") {
		return "", "", fmt.Errorf("bad file name")
	}
	segs := strings.Split(strings.TrimSuffix(dir, "/"), "/")
	if dir == "" {
		segs = nil
	}
	if base != "index" {
		segs = append(segs, base)
	}
	seen := map[string]bool{}
	for i, s := range segs {
		if s == "" {
			return "", "", fmt.Errorf("empty path segment")
		}
		if strings.HasPrefix(s, "[") || strings.HasSuffix(s, "]") {
			if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") || len(s) < 3 {
				return "", "", fmt.Errorf("malformed parameter %q", s)
			}
			n := s[1 : len(s)-1]
			if !isIdent(n) || seen[n] {
				return "", "", fmt.Errorf("bad or repeated parameter %q", n)
			}
			seen[n] = true
			segs[i] = "{" + n + "}"
		} else if strings.ContainsAny(s, "{}[]") {
			return "", "", fmt.Errorf("malformed segment %q", s)
		}
	}
	return strings.Join(segs, "/"), lang, nil
}

func isIdent(s string) bool {
	for i, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return s != ""
}

func paramNames(pattern string) []string {
	var out []string
	for _, s := range strings.Split(pattern, "/") {
		if strings.HasPrefix(s, "{") {
			out = append(out, s[1:len(s)-1])
		}
	}
	return out
}

// Lookup returns the page registered under an exact pattern.
func (r *Table) Lookup(pattern string) (*Page, bool) {
	p, ok := r.pages[strings.TrimPrefix(pattern, "/")]
	return p, ok
}

// Match resolves a request path to a page and its parameters.
// Static segments win over parameters at the same position.
func (r *Table) Match(urlPath string) (*Page, map[string]string, bool) {
	segs := splitPath(urlPath)
	var best *Page
	var bestParams map[string]string
	bestScore := -1
	for _, pg := range r.pages {
		psegs := splitPath(pg.Pattern)
		if len(psegs) != len(segs) {
			continue
		}
		params := map[string]string{}
		score, okAll := 0, true
		for i, ps := range psegs {
			switch {
			case strings.HasPrefix(ps, "{"):
				params[ps[1:len(ps)-1]] = segs[i]
			case ps == segs[i]:
				score += 1 << (len(psegs) - i) // earlier static segments weigh more
			default:
				okAll = false
			}
			if !okAll {
				break
			}
		}
		if okAll && score > bestScore {
			best, bestParams, bestScore = pg, params, score
		}
	}
	return best, bestParams, best != nil
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// Patterns lists all patterns, sorted.
func (r *Table) Patterns() []string {
	out := make([]string, 0, len(r.pages))
	for k := range r.pages {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CheckHandler verifies that an SSR pattern matches a discovered template.
// A typo fails at startup and in `hearth check`.
func (r *Table) CheckHandler(pattern string) error {
	pg, ok := r.Lookup(pattern)
	if !ok {
		return fmt.Errorf("hearth: handler pattern %q matches no template (have: %s)", pattern, strings.Join(r.Patterns(), ", "))
	}
	if !pg.IsDynamic() {
		return nil
	}
	return nil
}
