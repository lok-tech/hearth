package cache

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// ErrUnsafePath is returned when a URL path cannot be mapped to a cache file safely.
var ErrUnsafePath = errors.New("hearth: unsafe cache path")

// RelPath maps a URL path to a relative file path inside a cache version
// directory: "/" -> "index.html", "/x" -> "x/index.html". A non-empty host
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

// Cache stores rendered pages in versioned directories. The "current" symlink
// points at the active version; flipping it invalidates everything at once.
type Cache struct {
	dir string
	mu  sync.Mutex
}

// New creates the cache root and, if absent, an initial version.
func New(dir string) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	c := &Cache{dir: dir}
	if _, err := c.Version(); err != nil {
		if err := c.SetVersion("v0"); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func ValidVersion(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return v != "." && v != ".."
}

// Version returns the active version name.
func (c *Cache) Version() (string, error) {
	t, err := os.Readlink(filepath.Join(c.dir, "current"))
	if err != nil {
		return "", err
	}
	return filepath.Base(t), nil
}

// SetVersion makes v the active version (creating its directory) by atomically
// replacing the symlink. Old version directories are left for Prune.
func (c *Cache) SetVersion(v string) error {
	if !ValidVersion(v) {
		return fmt.Errorf("hearth: invalid version %q", v)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	vdir := filepath.Join(c.dir, "versions", v)
	if err := os.MkdirAll(vdir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(c.dir, fmt.Sprintf(".current.%d.tmp", os.Getpid()))
	_ = os.Remove(tmp)
	if err := os.Symlink(filepath.Join("versions", v), tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(c.dir, "current")); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Root returns the directory of the active version.
func (c *Cache) Root() string { return filepath.Join(c.dir, "current") }

// Path returns the absolute file path of a cached page in the active version.
func (c *Cache) Path(host, urlPath string) (string, error) {
	rel, err := RelPath(host, urlPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(c.Root(), rel), nil
}

// Write stores body atomically (temp file in the same dir, then rename) in the
// version that was active when the render started, so a flip mid-render cannot
// leak stale content into the new version.
func (c *Cache) Write(version, host, urlPath string, body []byte) error {
	if !ValidVersion(version) {
		return fmt.Errorf("hearth: invalid version %q", version)
	}
	rel, err := RelPath(host, urlPath)
	if err != nil {
		return err
	}
	final := filepath.Join(c.dir, "versions", version, rel)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(final), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Prune removes version directories other than the active one and keep most recent others.
func (c *Cache) Prune(keep int) error {
	cur, err := c.Version()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(c.dir, "versions"))
	if err != nil {
		return err
	}
	type ve struct {
		name string
		mod  int64
	}
	var old []ve
	for _, e := range entries {
		if e.Name() == cur || !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		old = append(old, ve{e.Name(), info.ModTime().UnixNano()})
	}
	// newest first
	for i := 0; i < len(old); i++ {
		for j := i + 1; j < len(old); j++ {
			if old[j].mod > old[i].mod {
				old[i], old[j] = old[j], old[i]
			}
		}
	}
	for i, o := range old {
		if i >= keep {
			if err := os.RemoveAll(filepath.Join(c.dir, "versions", o.name)); err != nil {
				return err
			}
		}
	}
	return nil
}
