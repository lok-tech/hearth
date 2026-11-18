// Package data reads the local JSON files of a site's data/ folder.
package data

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// Read loads data/<name>.json. The name is validated so templates cannot read outside the folder.
func Read(fsys fs.FS, name string) (any, error) {
	if name == "" || strings.Contains(name, "..") || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
		return nil, fmt.Errorf("hearth: bad data name %q", name)
	}
	p := path.Clean(name)
	if !strings.HasSuffix(p, ".json") {
		p += ".json"
	}
	b, err := fs.ReadFile(fsys, p)
	if err != nil {
		return nil, fmt.Errorf("hearth: data %q: %w", name, err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("hearth: data %q: %w", name, err)
	}
	return v, nil
}
