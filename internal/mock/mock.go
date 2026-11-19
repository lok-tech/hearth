// Package mock supplies what the CLI uses in place of your Go program: data from *.mock.json
// files and stub template functions. It exists so `hearth check` and `hearth dev` can drive a
// hearth.Site without running your handlers or functions.
package mock

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/orelhadad/hearth/internal/route"
	"github.com/orelhadad/hearth/pkg/hearth"
)

// ErrNeedsProgram is wrapped by the error a stub function returns when a page calls it.
var ErrNeedsProgram = errors.New("needs your Go program")

// Stubs returns a function map with one stub per name. A stub parses (so templates are checked)
// but fails when called, because the real function lives in the Go program that serves the site.
func Stubs(names []string) template.FuncMap {
	if len(names) == 0 {
		return nil
	}
	fm := template.FuncMap{}
	for _, n := range names {
		fm[n] = func(...any) (any, error) {
			return nil, fmt.Errorf("function %q is provided by your Go program; run this page through it (%w)", n, ErrNeedsProgram)
		}
	}
	return fm
}

// Handler answers a page from its .mock.json file. A page with route parameters reads an object
// mapping the parameter value to the page data, with "*" as the fallback. A page without
// parameters (an ssr page) uses the whole file as its data.
func Handler(src fs.FS, pg *route.Page) hearth.Handler {
	return func(r *http.Request, params map[string]string) (any, hearth.Meta, error) {
		missing := fmt.Errorf("%w: no mock data for %s (add %s)", hearth.ErrNotFound, r.URL.Path, pg.MockFile())
		b, err := fs.ReadFile(src, pg.MockFile())
		if err != nil {
			return nil, hearth.Meta{}, missing
		}
		if !pg.IsDynamic() {
			var v any
			if err := json.Unmarshal(b, &v); err != nil {
				return nil, hearth.Meta{}, fmt.Errorf("%s: %w", pg.MockFile(), err)
			}
			return v, hearth.Meta{}, nil
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, hearth.Meta{}, fmt.Errorf("%s: %w", pg.MockFile(), err)
		}
		if len(pg.Params) == 1 {
			if v, ok := m[params[pg.Params[0]]]; ok {
				return v, hearth.Meta{}, nil
			}
		}
		if v, ok := m["*"]; ok {
			return v, hearth.Meta{}, nil
		}
		return nil, hearth.Meta{}, missing
	}
}
