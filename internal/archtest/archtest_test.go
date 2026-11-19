// Package archtest holds tests about the shape of the code, not its behaviour.
package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const mod = "github.com/lok-tech/hearth"

// importers returns, for every non-test Go file under root, the module-internal imports it makes
// (as package directories relative to the module root, e.g. "internal/render"), keyed by the
// importing package's directory.
func importers(t *testing.T, root string) map[string]map[string]bool {
	t.Helper()
	out := map[string]map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "template") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		dir, _ := filepath.Rel(root, filepath.Dir(p))
		dir = filepath.ToSlash(dir)
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(path, mod+"/") {
				if out[dir] == nil {
					out[dir] = map[string]bool{}
				}
				out[dir][strings.TrimPrefix(path, mod+"/")] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if dir == filepath.Dir(dir) {
			t.Fatal("go.mod not found")
		}
	}
}

// Rendering has exactly one entry point: pkg/hearth. The CLI, the build, check and dev drive a
// hearth.Site instead of rendering pages themselves, which is what keeps static output, dev and
// a production server identical. If this fails, someone added a second renderer.
func TestOnlyThePublicPackageImportsTheRenderer(t *testing.T) {
	var offenders []string
	for dir, imps := range importers(t, moduleRoot(t)) {
		if imps["internal/render"] && dir != "pkg/hearth" {
			offenders = append(offenders, dir)
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("these packages import internal/render directly; render through hearth.Site instead:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// The dependency points one way: cmd -> internal tools -> pkg/hearth -> internal core.
// The public package must never depend on the CLI-side packages.
func TestPublicPackageDoesNotDependOnTheTools(t *testing.T) {
	imps := importers(t, moduleRoot(t))["pkg/hearth"]
	for _, forbidden := range []string{"internal/build", "internal/dev", "internal/starter", "internal/config", "cmd/hearth"} {
		if imps[forbidden] {
			t.Errorf("pkg/hearth imports %s; the library must not depend on the CLI side", forbidden)
		}
	}
}
