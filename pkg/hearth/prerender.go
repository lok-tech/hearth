package hearth

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/orelhadad/hearth/internal/fsutil"
)

// PrerenderFailure is one page that did not render.
type PrerenderFailure struct {
	Path   string // language-neutral path, e.g. "/post/42"
	Lang   string
	Status int   // HTTP status the page would have answered; 0 when the render itself failed
	Err    error // why
}

func (f PrerenderFailure) Error() string { return fmt.Sprintf("%s [%s]: %v", f.Path, f.Lang, f.Err) }

// Unwrap exposes the cause, so errors.Is and errors.As see through a failure.
func (f PrerenderFailure) Unwrap() error { return f.Err }

// PrerenderReport summarises a Prerender run.
type PrerenderReport struct {
	OK       int
	Pages    []string // request paths that rendered, language prefix included, sorted
	Failures []PrerenderFailure
}

// Prerender renders every language-neutral path (such as "/about" or "/post/42") in every language
// of the site, exactly as a visitor's request would, and leaves the files in the active cache
// version. `hearth build` is this call plus Export, so the static output and the server cannot differ.
// A path that does not answer 200 is a failure. concurrency bounds parallel pages.
func (s *Site) Prerender(ctx context.Context, paths []string, concurrency int) PrerenderReport {
	if concurrency <= 0 {
		concurrency = 4
	}
	type job struct{ path, lang string }
	var jobs []job
	for _, p := range paths {
		for _, l := range s.urls.Langs {
			jobs = append(jobs, job{p, l})
		}
	}
	var (
		mu  sync.Mutex
		rep PrerenderReport
		wg  sync.WaitGroup
		sem = make(chan struct{}, concurrency)
	)
	fail := func(j job, status int, err error) {
		mu.Lock()
		rep.Failures = append(rep.Failures, PrerenderFailure{Path: j.path, Lang: j.lang, Status: status, Err: err})
		mu.Unlock()
	}
	for _, j := range jobs {
		if err := ctx.Err(); err != nil {
			fail(j, 0, err)
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			defer func() { <-sem }()
			if !strings.HasPrefix(j.path, "/") {
				fail(j, 0, fmt.Errorf("path must start with /"))
				return
			}
			host, _ := s.urls.Location(j.lang)
			reqPath := s.urls.Path(j.lang, j.path)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
			if err != nil {
				fail(j, 0, err)
				return
			}
			req.URL.Path, req.Host = reqPath, host
			res, err := s.resolve(req)
			switch {
			case err != nil:
				fail(j, 0, err)
			case res.status != http.StatusOK:
				msg := fmt.Sprintf("answers %d %s", res.status, http.StatusText(res.status))
				if res.to != "" {
					msg += " to " + res.to
				}
				if res.note != "" {
					msg += ": " + res.note
				}
				fail(j, res.status, fmt.Errorf("%s", msg))
			default:
				mu.Lock()
				rep.OK++
				rep.Pages = append(rep.Pages, reqPath)
				mu.Unlock()
			}
		}(j)
	}
	wg.Wait()
	sort.Strings(rep.Pages)
	sort.Slice(rep.Failures, func(i, j int) bool {
		a, b := rep.Failures[i], rep.Failures[j]
		return a.Path < b.Path || a.Path == b.Path && a.Lang < b.Lang
	})
	return rep
}

// Export copies every page in the active cache version to dst, keeping the cache layout
// (<host>/<path>/index.html), so it can be deployed to any static host. Files are written
// atomically; existing files in dst are overwritten and others are left alone.
func (s *Site) Export(dst string) error {
	root, err := filepath.EvalSymlinks(s.cache.Root())
	if err != nil {
		return err
	}
	src := os.DirFS(root)
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") { // in-flight temp files are never exported
			return nil
		}
		b, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		return fsutil.WriteFileAtomic(filepath.Join(dst, filepath.FromSlash(p)), b)
	})
}
