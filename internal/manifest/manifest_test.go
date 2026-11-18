package manifest

import (
	"strings"
	"testing"
)

func TestParseManifest(t *testing.T) {
	m, err := Parse([]byte(`{"format":1,"assets":{"entry.js":"/assets/entry-abc.js"},"images":{"hero":[{"url":"/i/h.avif","type":"image/avif"},{"url":"/i/h.jpg","type":"image/jpeg","width":800,"height":600}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if u, err := m.Asset("entry.js"); err != nil || u != "/assets/entry-abc.js" {
		t.Fatalf("asset = %q, %v", u, err)
	}
	if _, err := m.Asset("missing.js"); err == nil {
		t.Fatal("unknown asset must fail")
	}
	pic, err := m.Picture("hero", `a "quote"`, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`<source type="image/avif" srcset="/i/h.avif">`, `width="800" height="600"`, `alt="a &#34;quote&#34;"`, `loading="lazy"`} {
		if !strings.Contains(string(pic), s) {
			t.Errorf("picture missing %s: %s", s, pic)
		}
	}
}

func TestManifestFormatMismatch(t *testing.T) {
	if _, err := Parse([]byte(`{"format":99}`)); err == nil || !strings.Contains(err.Error(), "format") {
		t.Fatalf("err = %v", err)
	}
	if _, err := Parse([]byte(`{"format":1,"bogus":1}`)); err == nil {
		t.Fatal("unknown fields must fail")
	}
	if _, err := Parse([]byte(`nope`)); err == nil {
		t.Fatal("bad json must fail")
	}
}

func TestPrefixURLs(t *testing.T) {
	m := &Manifest{Assets: map[string]string{"a": "/assets/a.js"}, Images: map[string][]ImageFile{"h": {{URL: "/assets/img/h.jpg"}}}}
	m.PrefixURLs("https://cdn.test")
	if m.Assets["a"] != "https://cdn.test/assets/a.js" || m.Images["h"][0].URL != "https://cdn.test/assets/img/h.jpg" {
		t.Fatalf("%+v", m)
	}
}
