package config

import (
	"strings"
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

func TestDevProxyValidation(t *testing.T) {
	good := `{"dev":{"proxy":{"/api":"http://localhost:8080","/uploads":"https://cdn.test/files"}}}`
	cfg, err := Load(cfgSite(t, good))
	if err != nil || len(cfg.Dev.Proxy) != 2 {
		t.Fatalf("%v %+v", err, cfg)
	}
	for body, want := range map[string]string{
		`{"/":"http://x"}`:              "whole site",
		`{"api":"http://x"}`:            "start with /",
		`{"/api/":"http://x"}`:          "must not end with /",
		`{"/api//x":"http://x"}`:        "empty",
		`{"/a/../b":"http://x"}`:        "\"..\"",
		`{"/a b":"http://x"}`:           "plain path",
		`{"/api?x=1":"http://x"}`:       "plain path",
		`{"/healthz":"http://x"}`:       "Hearth itself",
		`{"/robots.txt":"http://x"}`:    "Hearth itself",
		`{"/sitemap.xml":"http://x"}`:   "Hearth itself",
		`{"/sitemap.xml/x":"http://x"}`: "Hearth itself",
		`{"/api":"localhost:8080"}`:     "absolute http(s)",
		`{"/api":"ftp://x"}`:            "absolute http(s)",
		`{"/api":"http://"}`:            "absolute http(s)",
		`{"/api":"/relative"}`:          "absolute http(s)",
		`{"/api":"http://x/#frag"}`:     "absolute http(s)",
	} {
		_, err := Load(cfgSite(t, `{"dev":{"proxy":`+body+`}}`))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", body, err, want)
		}
	}
}

func cfgSite(t *testing.T, cfg string) string {
	return testutil.WriteSite(t, map[string]string{"hearth.config.json": cfg})
}
