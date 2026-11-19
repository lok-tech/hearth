package render

import (
	"bytes"
	"encoding/json"
	"html"
	"html/template"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// jsonSamples cover the characters that break naive embedding.
func jsonSamples() map[string]any {
	return map[string]any{
		"quotes":  `he said "hi" and 'bye'`,
		"tags":    `</script><b onclick="x">&amp; &lt;`,
		"amp":     "a & b &#34; &quot;",
		"lsep":    "a b c",
		"hebrew":  "הַבְּחִירוֹת – מועמדים \"בכירים\"",
		"nested":  map[string]any{"s": `{"k":"v \"q\""}`, "n": []any{1.5, true, nil}},
		"newline": "line1\nline2\ttab",
	}
}

func execJSONTemplate(t *testing.T, tpl string, data any) string {
	t.Helper()
	tm, err := template.New("t").Funcs(template.FuncMap{"json": JSONForScript}).Parse(tpl)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := tm.Execute(&buf, map[string]any{"X": data}); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TestJSONInAttribute checks `data-x="{{json .X}}"`: the quoting must survive and an HTML parser
// (here: take the attribute text up to the closing quote, then unescape entities) must give back the JSON.
func TestJSONInAttribute(t *testing.T) {
	re := regexp.MustCompile(`^<div data-x="([^"]*)">$`)
	for name, v := range jsonSamples() {
		out := execJSONTemplate(t, `<div data-x="{{json .X}}">`, v)
		m := re.FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("%s: attribute quoting broken: %s", name, out)
		}
		raw := html.UnescapeString(m[1])
		var got any
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("%s: attribute value is not JSON: %v\n%s", name, err, out)
		}
		want := roundTrip(t, v)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: round trip differs\n got %#v\nwant %#v\nhtml %s", name, got, want, out)
		}
		if strings.ContainsAny(m[1], "<>\u2028\u2029") {
			t.Fatalf("%s: raw <, > or line separator in attribute: %s", name, out)
		}
	}
}

// TestJSONInScript checks <script type="application/json">: nothing can close the tag and the
// content parses as the original JSON.
func TestJSONInScript(t *testing.T) {
	re := regexp.MustCompile(`(?s)^<script type="application/json" id="d">(.*)</script>$`)
	for name, v := range jsonSamples() {
		out := execJSONTemplate(t, `<script type="application/json" id="d">{{json .X}}</script>`, v)
		m := re.FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("%s: script block broken: %s", name, out)
		}
		if strings.ContainsAny(m[1], "<>&  ") {
			t.Fatalf("%s: unescaped character in script body: %s", name, m[1])
		}
		var got any
		if err := json.Unmarshal([]byte(m[1]), &got); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got, roundTrip(t, v)) {
			t.Fatalf("%s: round trip differs", name)
		}
	}
}

func roundTrip(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
