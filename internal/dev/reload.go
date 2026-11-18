package dev

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const reloadPath = "/__hearth/reload"

// reloadScript is injected into every HTML page in watch mode. It reloads the page when the server
// says something changed, and again when the connection comes back after `hearth dev` restarted.
const reloadScript = `<script>(()=>{const e=new EventSource("` + reloadPath + `");let o=false;` +
	`e.onopen=()=>{if(o)location.reload();o=true};e.addEventListener("reload",()=>location.reload())})()</script>`

var (
	reloadPoll     = 300 * time.Millisecond // how often sources are checked
	reloadSettle   = 150 * time.Millisecond // a change must hold still this long before browsers reload
	reloadKeepWarm = 15 * time.Second       // SSE comment so proxies and browsers keep the stream open
)

// sourceDirs are the site directories whose changes reload the browser directly. src/ is left out on
// purpose: Vite rebuilds it, and the reload follows when the build's manifest changes, so the
// browser never reloads onto stale assets.
var sourceDirs = []string{"pages", "layouts", "partials", "locales", "data", "public"}

type reloader struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func newReloader() *reloader { return &reloader{subs: map[chan struct{}]struct{}{}} }

func (r *reloader) subscribe() (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	r.mu.Lock()
	r.subs[ch] = struct{}{}
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.subs, ch)
		r.mu.Unlock()
	}
}

func (r *reloader) broadcast() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for ch := range r.subs {
		select {
		case ch <- struct{}{}:
		default: // a reload is already pending for this browser
		}
	}
}

// fingerprint summarises names, sizes and times of the watched files. Changing any of them
// changes the value. A missing Vite manifest (mid-rebuild) keeps the previous manifest value.
func fingerprint(site, out string, prevManifest *string) string {
	h := fnv.New64a()
	for _, d := range sourceDirs {
		root := filepath.Join(site, d)
		filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if e.IsDir() {
				if strings.HasPrefix(e.Name(), ".") && p != root {
					return filepath.SkipDir
				}
				return nil
			}
			if st, err := e.Info(); err == nil {
				fmt.Fprintf(h, "%s|%d|%d;", p, st.Size(), st.ModTime().UnixNano())
			}
			return nil
		})
	}
	if st, err := os.Stat(filepath.Join(out, ".vite", "manifest.json")); err == nil {
		*prevManifest = strconv.FormatInt(st.ModTime().UnixNano(), 10) + "|" + strconv.FormatInt(st.Size(), 10)
	}
	fmt.Fprint(h, *prevManifest)
	return strconv.FormatUint(h.Sum64(), 16)
}

// watch polls until ctx ends and broadcasts once per settled change.
func (r *reloader) watch(ctx context.Context, site, out string) {
	var manifest string
	last := fingerprint(site, out, &manifest)
	var pendingSince time.Time
	var pending string
	t := time.NewTicker(reloadPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cur := fingerprint(site, out, &manifest)
		switch {
		case cur != last && cur != pending:
			pending, pendingSince = cur, time.Now()
		case cur == pending && cur != last && time.Since(pendingSince) >= reloadSettle:
			last, pending = cur, ""
			r.broadcast()
		}
	}
}

// middleware serves the event stream and injects the client script into HTML responses.
func (r *reloader) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == reloadPath {
			r.stream(w, req)
			return
		}
		iw := &injectWriter{ResponseWriter: w}
		next.ServeHTTP(iw, req)
		iw.finish()
	})
}

func (r *reloader) stream(w http.ResponseWriter, req *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": hearth live reload\n\n")
	fl.Flush()
	ch, unsub := r.subscribe()
	defer unsub()
	keep := time.NewTicker(reloadKeepWarm)
	defer keep.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-ch:
			fmt.Fprint(w, "event: reload\ndata: 1\n\n")
		case <-keep.C:
			fmt.Fprint(w, ": keep-alive\n\n")
		}
		fl.Flush()
	}
}

// injectWriter holds back HTML responses so the script can be added before </body>; everything
// else passes through untouched.
type injectWriter struct {
	http.ResponseWriter
	status  int
	html    bool
	started bool
	buf     bytes.Buffer
}

func (w *injectWriter) WriteHeader(code int) {
	if w.started {
		return
	}
	w.started, w.status = true, code
	w.html = strings.HasPrefix(w.Header().Get("Content-Type"), "text/html")
	if !w.html {
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *injectWriter) Write(b []byte) (int, error) {
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}
	if w.html {
		return w.buf.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *injectWriter) finish() {
	if !w.html {
		return
	}
	body := w.buf.Bytes()
	if i := bytes.LastIndex(body, []byte("</body>")); i >= 0 {
		body = append(append(append([]byte{}, body[:i]...), reloadScript...), body[i:]...)
	} else {
		body = append(body, reloadScript...)
	}
	w.Header().Del("Content-Length")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.ResponseWriter.WriteHeader(w.status)
	w.ResponseWriter.Write(body)
}
