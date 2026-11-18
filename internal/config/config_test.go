package config

import (
	"testing"

	"github.com/orelhadad/hearth/internal/testutil"
)

func TestConfigValidation(t *testing.T) {
	for name, cfg := range map[string]string{
		"mode":    `{"i18n":{"mode":"weird"}}`,
		"hosts":   `{"i18n":{"mode":"domain"}}`,
		"badhost": `{"i18n":{"mode":"domain","hosts":{"en":"../x"}}}`,
		"unknown": `{"bogus":1}`,
		"vite":    `{"vite":{"command":[]}}`,
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
