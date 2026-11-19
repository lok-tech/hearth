package mock

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/orelhadad/hearth/internal/route"
	"github.com/orelhadad/hearth/pkg/hearth"
)

func TestHandlerShapes(t *testing.T) {
	src := fstest.MapFS{
		"pages/list.html":        {Data: []byte("x")},
		"pages/list.mock.json":   {Data: []byte(`["a","b"]`)},
		"pages/p/[id].html":      {Data: []byte("x")},
		"pages/p/[id].mock.json": {Data: []byte(`{"1":"one","*":"any"}`)},
		"pages/bare.html":        {Data: []byte("x")},
		"pages/broken.html":      {Data: []byte("x")},
		"pages/broken.mock.json": {Data: []byte(`{`)},
	}
	routes, err := route.Discover(src, "pages")
	if err != nil {
		t.Fatal(err)
	}
	call := func(pat string, params map[string]string) (any, error) {
		pg, _ := routes.Lookup(pat)
		d, _, err := Handler(src, pg)(httptest.NewRequest("GET", "/x", nil), params)
		return d, err
	}
	if d, err := call("list", nil); err != nil || len(d.([]any)) != 2 {
		t.Errorf("list: %v %v", d, err)
	}
	if d, _ := call("p/{id}", map[string]string{"id": "1"}); d != "one" {
		t.Errorf("param: %v", d)
	}
	if d, _ := call("p/{id}", map[string]string{"id": "9"}); d != "any" {
		t.Errorf("fallback: %v", d)
	}
	if _, err := call("bare", nil); !errors.Is(err, hearth.ErrNotFound) || !strings.Contains(err.Error(), "bare.mock.json") {
		t.Errorf("missing: %v", err)
	}
	if _, err := call("broken", nil); err == nil || errors.Is(err, hearth.ErrNotFound) {
		t.Errorf("broken: %v", err)
	}
}

func TestStubsFailWhenCalled(t *testing.T) {
	fm := Stubs([]string{"num"})
	f := fm["num"].(func(...any) (any, error))
	if _, err := f(1); !errors.Is(err, ErrNeedsProgram) || !strings.Contains(err.Error(), `"num"`) {
		t.Errorf("stub error: %v", err)
	}
	if Stubs(nil) != nil {
		t.Error("no names, no map")
	}
}
