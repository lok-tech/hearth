package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "b", "f.html")
	if err := WriteFileAtomic(p, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "two" {
		t.Fatalf("content = %q", b)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestWriteFileAtomicFailsWhenParentIsAFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "file")
	os.WriteFile(f, []byte("x"), 0o644)
	if err := WriteFileAtomic(filepath.Join(f, "child"), []byte("y")); err == nil {
		t.Fatal("expected an error")
	}
}
