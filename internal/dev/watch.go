package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/orelhadad/hearth/internal/build"
	"github.com/orelhadad/hearth/internal/config"
)

// NewWatch is `hearth dev` without a Vite dev server: it runs `vite build --watch` into a cache
// directory and serves everything from one address. Pages are rendered from source on every
// request (templates reload), with the hashed asset URLs of the latest Vite build; the built files
// and the site's public/ directory are served by this handler. There is no hot module
// replacement, but the browser reloads itself when a page source or a Vite build changes (see reload.go). It returns once the first build has finished.
//
// The returned stop function ends the Vite process and waits for it; call it on every exit path,
// or the watcher outlives the server.
func NewWatch(ctx context.Context, siteDir string, cfg *config.Config, log io.Writer) (http.Handler, func(), error) {
	if cfg.Vite == nil || len(cfg.Vite.Command) == 0 {
		return nil, nil, fmt.Errorf("hearth: watch mode needs vite.command in hearth.config.json")
	}
	site, err := filepath.Abs(siteDir)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	out := filepath.Join(site, "node_modules", ".cache", "hearth-dev")
	if err := os.RemoveAll(out); err != nil {
		cancel()
		return nil, nil, err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		cancel()
		return nil, nil, err
	}
	args := append(append([]string{}, cfg.Vite.Command[1:]...), "--watch", "--outDir", out, "--emptyOutDir", "--manifest")
	cmd := exec.CommandContext(ctx, cfg.Vite.Command[0], args...)
	cmd.Dir = site
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 3 * time.Second
	if log != nil {
		cmd.Stdout, cmd.Stderr = log, log
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("hearth: cannot start vite: %w", err)
	}
	exited := make(chan error, 1)
	done := make(chan struct{})
	go func() { exited <- cmd.Wait(); close(done) }()
	stop := func() { cancel(); <-done }
	fail := func(err error) (http.Handler, func(), error) {
		stop()
		return nil, nil, err
	}

	wa := &watcher{out: out, public: filepath.Join(site, "public")}
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(filepath.Join(out, ".vite", "manifest.json")); err == nil {
			break
		}
		select {
		case err := <-exited:
			return fail(fmt.Errorf("hearth: vite exited before its first build finished (%v)", err))
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-deadline.C:
			return fail(fmt.Errorf("hearth: vite did not finish a first build in 2 minutes"))
		case <-time.After(100 * time.Millisecond):
		}
	}
	if _, err := wa.load(); err != nil {
		return fail(err)
	}
	h, err := newMock(siteDir, cfg, wa)
	if err != nil {
		return fail(err)
	}
	rl := newReloader()
	go rl.watch(ctx, site, out)
	// the proxy sits outside live reload, so backend responses are never rewritten
	ph, err := withProxy(cfg.Dev.Proxy, rl.middleware(h))
	if err != nil {
		return fail(err)
	}
	return ph, stop, nil
}

// watcher reads the latest Vite output and serves its files.
type watcher struct {
	out, public string

	mu   sync.Mutex
	last []byte // last manifest that parsed; kept while a rebuild empties the output directory
}

func (w *watcher) load() ([]byte, error) {
	m, err := build.LoadViteOutput(w.out)
	if err != nil {
		return nil, err
	}
	m.Version = "dev"
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	w.last = b
	w.mu.Unlock()
	return b, nil
}

func (w *watcher) manifestJSON() []byte {
	if b, err := w.load(); err == nil {
		return b
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last
}

// static serves the Vite output, then public/. Dotfiles (.vite, .hearth) are never served.
func (w *watcher) static() http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(rw, r)
			return
		}
		p := path.Clean("/" + r.URL.Path)
		for _, seg := range strings.Split(p, "/") {
			if strings.HasPrefix(seg, ".") {
				http.NotFound(rw, r)
				return
			}
		}
		for _, root := range []string{w.out, w.public} {
			name := filepath.Join(root, filepath.FromSlash(p))
			if !within(root, name) {
				continue
			}
			f, err := os.Open(name)
			if err != nil {
				continue
			}
			st, err := f.Stat()
			if err != nil || st.IsDir() {
				f.Close()
				continue
			}
			rw.Header().Set("Cache-Control", "no-store")
			http.ServeContent(rw, r, st.Name(), st.ModTime(), f)
			f.Close()
			return
		}
		http.NotFound(rw, r)
	})
}

// overlay is the site source tree plus an in-memory manifest.json.
type overlay struct {
	fs.FS
	manifest []byte
}

func (o overlay) Open(name string) (fs.File, error) {
	if name == "manifest.json" && o.manifest != nil {
		return &memFile{Reader: bytes.NewReader(o.manifest), size: int64(len(o.manifest))}, nil
	}
	return o.FS.Open(name)
}

type memFile struct {
	*bytes.Reader
	size int64
}

func (f *memFile) Stat() (fs.FileInfo, error) { return f, nil }
func (f *memFile) Close() error               { return nil }
func (f *memFile) Name() string               { return "manifest.json" }
func (f *memFile) Size() int64                { return f.size }
func (f *memFile) Mode() fs.FileMode          { return 0o444 }
func (f *memFile) ModTime() time.Time         { return time.Time{} }
func (f *memFile) IsDir() bool                { return false }
func (f *memFile) Sys() any                   { return nil }
