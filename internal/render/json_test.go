package render

import (
	"strings"
	"testing"
)

func TestJSONForScriptEscapes(t *testing.T) {
	h, err := JSONForScript(map[string]string{"x": "</script><b>& "})
	if err != nil {
		t.Fatal(err)
	}
	s := string(h)
	if strings.ContainsAny(s, "<>& ") {
		t.Fatalf("unescaped output: %s", s)
	}
}
