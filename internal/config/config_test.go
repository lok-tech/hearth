package config

import (
	"testing"

	"github.com/orelhadad/hearth/internal/testutil"
)

func TestConfigValidation(t *testing.T) {
	for name, cfg := range map[string]string{
		"mode":     `{"i18n":{"mode":"weird"}}`,
		"hosts":    `{"i18n":{"mode":"domain"}}`,
		"badhost":  `{"i18n":{"mode":"domain","hosts":{"en":"../x"}}}`,
		"unknown":  `{"bogus":1}`,
		"vite":     `{"vite":{"command":[]}}`,
		"ssrrel":   `{"ssr":["candidates"]}`,
		"ssrtrail": `{"ssr":["/candidates/"]}`,
		"ssrparam": `{"ssr":["/post/{id}"]}`,
		"ssrdup":   `{"ssr":["/a","/a"]}`,
		"funcname": `{"funcs":["a b"]}`,
		"funcdup":  `{"funcs":["num","num"]}`,
	} {
		if _, err := Load(testutil.WriteSite(t, map[string]string{"hearth.config.json": cfg})); err == nil {
			t.Errorf("%s should fail", name)
		}
	}
}

func TestAssetBaseValidation(t *testing.T) {
	for _, ok := range []string{"", "https://cdn.example.com", "https://cdn.example.com/", "/static", "http://localhost:9000/b"} {
		if _, err := Load(testutil.WriteSite(t, map[string]string{"hearth.config.json": `{"assetBase":"` + ok + `"}`})); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"cdn.example.com", "//cdn.example.com", "ftp://x", "https://x?a=1", "javascript:alert(1)", "https://"} {
		if _, err := Load(testutil.WriteSite(t, map[string]string{"hearth.config.json": `{"assetBase":"` + bad + `"}`})); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestSSRAndFuncsAreLoaded(t *testing.T) {
	c, err := Load(testutil.WriteSite(t, map[string]string{"hearth.config.json": `{"ssr":["/","/candidates"],"funcs":["num","dec"]}`}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.SSR) != 2 || len(c.Funcs) != 2 {
		t.Errorf("ssr=%v funcs=%v", c.SSR, c.Funcs)
	}
}
