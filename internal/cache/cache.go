package cache

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ErrUnsafePath is returned when a URL path cannot be mapped to a cache file safely.
var ErrUnsafePath = errors.New("hearth: unsafe cache path")

// RelPath maps a URL path to a relative file path inside the cache directory: "/" -> "index.html", "/x" -> "x/index.html". A non-empty host
// (subdomain/domain i18n) becomes the first directory. Anything that could
// escape the directory, or collide with it, is rejected.
func RelPath(host, urlPath string) (string, error) {
	if strings.ContainsRune(urlPath, 0) || strings.ContainsRune(urlPath, '\\') {
		return "", ErrUnsafePath
	}
	if urlPath == "" || urlPath[0] != '/' {
		return "", ErrUnsafePath
	}
	for _, seg := range strings.Split(urlPath, "/") {
		if seg == ".." || seg == "." {
			return "", ErrUnsafePath
		}
	}
	clean := path.Clean(urlPath)
	rel := strings.TrimPrefix(clean, "/")
	if rel == "" {
		rel = "index.html"
	} else {
		rel += "/index.html"
	}
	if host != "" {
		if !ValidHost(host) {
			return "", ErrUnsafePath
		}
		rel = host + "/" + rel
	}
	return filepath.FromSlash(rel), nil
}

func ValidHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.', r == ':':
		default:
			return false
		}
	}
	return !strings.HasPrefix(h, ".") && !strings.Contains(h, "..")
}

// Cache stores rendered pages as plain files under one directory, laid out as
// <host>/<path>/index.html, so a web server can serve them directly. There are no versions: a
// full invalidation is the deploy replacing the directory, and Invalidate drops single pages.
type Cache struct {
	dir     string
	tmpDir  string // "" writes temp files next to the page; otherwise a directory on the same mount
	stripes [stripeCount]stripe
}

// stripeCount bounds the memory used to guard per-path invalidation: paths hash into a fixed
// set of slots, so nothing grows with the number of pages. Two paths sharing a slot can only
// cause a skipped cache write (the page is rendered again later), never a stale page.
const stripeCount = 256

// stripe guards the pages that hash to it. gen counts invalidations; mu makes "check gen, then
// rename into place" atomic against "bump gen, then delete the file".
type stripe struct {
	mu  sync.Mutex
	gen uint64
}

func (c *Cache) stripe(rel string) *stripe {
	h := fnv.New32a()
	_, _ = h.Write([]byte(rel))
	return &c.stripes[h.Sum32()%stripeCount]
}

// staleTemp is how old a file in the temp directory must be before New removes it. A live write
// holds its temp file for milliseconds, so anything this old was left behind by a crash.
const staleTemp = 10 * time.Minute

// New creates the cache directory. With tmpDir set, pages are written to a temp file there and
// renamed into place, so no temp file ever appears in the served directory. tmpDir must be on the
// same mount as dir (rename does not cross mounts); New checks that by renaming a probe file and
// fails with a clear error if it cannot. Files left in tmpDir by a crash are removed. An empty
// tmpDir keeps temp files in the page's own directory as ".tmp-*" dotfiles, which a web server
// must then never serve.
func New(dir, tmpDir string) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	c := &Cache{dir: dir}
	if tmpDir == "" {
		return c, nil
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, err
	}
	if err := probe(dir, tmpDir); err != nil {
		return nil, err
	}
	sweep(tmpDir, staleTemp)
	c.tmpDir = tmpDir
	return c, nil
}

// probe renames a file from tmpDir into dir, which is exactly what every cache write does.
func probe(dir, tmpDir string) error {
	f, err := os.CreateTemp(tmpDir, "probe-*")
	if err != nil {
		return fmt.Errorf("hearth: temp directory %s is not writable: %w", tmpDir, err)
	}
	name := f.Name()
	_ = f.Close()
	dst := filepath.Join(dir, ".hearth-probe-"+filepath.Base(name))
	if err := os.Rename(name, dst); err != nil {
		_ = os.Remove(name)
		if errors.Is(err, syscall.EXDEV) {
			return fmt.Errorf("hearth: temp directory %s and cache directory %s must be on the same mount: %w", tmpDir, dir, err)
		}
		return fmt.Errorf("hearth: cannot move a file from %s into %s: %w", tmpDir, dir, err)
	}
	return os.Remove(dst)
}

// sweep removes regular files in dir last modified more than age ago.
func sweep(dir string, age time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cut := time.Now().Add(-age)
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cut) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// Root returns the cache directory.
func (c *Cache) Root() string { return c.dir }

// Path returns the absolute file path of a cached page.
func (c *Cache) Path(host, urlPath string) (string, error) {
	rel, err := RelPath(host, urlPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(c.dir, rel), nil
}

// Gen returns the invalidation generation of a page. Take it before reading the data a page
// is rendered from, and hand it to WriteGen.
func (c *Cache) Gen(host, urlPath string) (uint64, error) {
	rel, err := RelPath(host, urlPath)
	if err != nil {
		return 0, err
	}
	st := c.stripe(rel)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.gen, nil
}

// Invalidate drops one page. It bumps the page's generation first, under
// the lock WriteGen takes, so a render that began earlier cannot put its (possibly stale) output
// back after the delete: its WriteGen sees the new generation and skips the write.
func (c *Cache) Invalidate(host, urlPath string) error {
	rel, err := RelPath(host, urlPath)
	if err != nil {
		return err
	}
	st := c.stripe(rel)
	st.mu.Lock()
	defer st.mu.Unlock()
	st.gen++
	if err := os.Remove(filepath.Join(c.dir, rel)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Write stores body atomically (temp file, then rename), so readers never see a partial page.
func (c *Cache) Write(host, urlPath string, body []byte) error {
	_, err := c.write(host, urlPath, body, false, 0)
	return err
}

// WriteGen is Write that only takes effect if the page was not invalidated since gen was read
// with Gen. It reports whether the file was written.
func (c *Cache) WriteGen(host, urlPath string, gen uint64, body []byte) (bool, error) {
	return c.write(host, urlPath, body, true, gen)
}

func (c *Cache) write(host, urlPath string, body []byte, check bool, gen uint64) (bool, error) {
	rel, err := RelPath(host, urlPath)
	if err != nil {
		return false, err
	}
	final := filepath.Join(c.dir, rel)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return false, err
	}
	tmpIn, pattern := c.tmpDir, "page-*"
	if tmpIn == "" {
		tmpIn, pattern = filepath.Dir(final), ".tmp-*"
	}
	f, err := os.CreateTemp(tmpIn, pattern)
	if err != nil {
		return false, err
	}
	tmp := f.Name()
	if _, err := f.Write(body); err != nil {
		f.Close()
		os.Remove(tmp)
		return false, err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return false, err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return false, err
	}
	st := c.stripe(rel)
	st.mu.Lock()
	defer st.mu.Unlock()
	if check && st.gen != gen {
		os.Remove(tmp)
		return false, nil
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return false, err
	}
	return true, nil
}
