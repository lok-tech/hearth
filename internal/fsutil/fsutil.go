// Package fsutil holds small file helpers shared by the build and the starter.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes body to path through a temp file in the same directory and a rename,
// creating parent directories, so readers never see a half-written file.
func WriteFileAtomic(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return err
	}
	if err := os.Chmod(f.Name(), 0o644); err != nil {
		os.Remove(f.Name())
		return err
	}
	return os.Rename(f.Name(), path)
}
