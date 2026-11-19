package starter

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lok-tech/hearth/internal/fsutil"
)

//go:embed all:template
var starterFS embed.FS

// Init writes the Vite starter into dir. It refuses to overwrite: every
// target file must be absent, and the whole check runs before anything is written.
// name replaces the {{name}} placeholder in package.json.
func Init(dir, name string) ([]string, error) {
	if name == "" {
		name = filepath.Base(dir)
	}
	if !validPackageName(name) {
		return nil, fmt.Errorf("hearth: %q is not a valid package name (lowercase letters, digits, - and _)", name)
	}
	sub, err := fs.Sub(starterFS, "template")
	if err != nil {
		return nil, err
	}
	var files []string
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(f))); err == nil {
			return nil, fmt.Errorf("hearth: %s already exists; refusing to overwrite", filepath.Join(dir, f))
		}
	}
	for _, f := range files {
		b, err := fs.ReadFile(sub, f)
		if err != nil {
			return nil, err
		}
		// Embedded dotfiles are stored with a "dot_" prefix (go:embed skips nothing with `all:`, but
		// npm and some tools rewrite .gitignore on publish).
		target := f
		if base := filepath.Base(f); strings.HasPrefix(base, "dot_") {
			target = filepath.Join(filepath.Dir(f), "."+strings.TrimPrefix(base, "dot_"))
		}
		b = []byte(strings.ReplaceAll(string(b), "{{name}}", name))
		if err := fsutil.WriteFileAtomic(filepath.Join(dir, filepath.FromSlash(target)), b); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func validPackageName(s string) bool {
	if s == "" || len(s) > 100 || s[0] == '-' || s[0] == '_' {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
