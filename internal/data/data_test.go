package data

import (
	"testing"
	"testing/fstest"
)

func TestReadRejectsTraversal(t *testing.T) {
	fsys := fstest.MapFS{"posts.json": {Data: []byte(`[1,2]`)}}
	if v, err := Read(fsys, "posts"); err != nil || len(v.([]any)) != 2 {
		t.Fatalf("%v %v", v, err)
	}
	for _, n := range []string{"../x", "/etc/passwd", "a\\b", ""} {
		if _, err := Read(fsys, n); err == nil {
			t.Errorf("%q should fail", n)
		}
	}
}
