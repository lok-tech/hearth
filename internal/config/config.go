package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/orelhadad/hearth/internal/cache"
)

// Config is hearth.config.json.
type Config struct {
	BaseURL     string                `json:"baseURL"`
	AssetBase   string                `json:"assetBase"` // CDN/S3 origin or path prefix for hashed assets, e.g. https://cdn.example.com
	DefaultLang string                `json:"defaultLang"`
	I18n        I18nConfig            `json:"i18n"`
	Vite        *ViteConfig           `json:"vite"`
	Collections map[string]Collection `json:"collections"` // pattern -> bound local collection
	Dev         DevConfig             `json:"dev"`
}

// I18nConfig selects how languages map to URLs. Paths are identical in every language.
type I18nConfig struct {
	Mode  string            `json:"mode"`  // "prefix" (default), "subdomain" or "domain"
	Hosts map[string]string `json:"hosts"` // lang -> host, for subdomain/domain modes
}

// ViteConfig runs Vite before pages are rendered. Command is an argv (no shell).
type ViteConfig struct {
	Command []string `json:"command"` // e.g. ["npx","vite","build"]
	DevURL  string   `json:"devURL"`  // e.g. http://localhost:5173
}

// Collection binds a [param] page to a local JSON array so it is prerendered for every entry.
type Collection struct {
	Data  string `json:"data"`  // data/<name>.json holding an array of objects
	Param string `json:"param"` // field used as the route parameter
}

// DevConfig configures `hearth dev` and `hearth preview`.
type DevConfig struct {
	Server string `json:"server"` // URL of the project's Go server (live / preview)
	Addr   string `json:"addr"`   // listen address, default 127.0.0.1:3000
}

// Load reads hearth.config.json from siteDir. A missing file yields defaults.
func Load(siteDir string) (*Config, error) {
	c := &Config{}
	b, err := os.ReadFile(filepath.Join(siteDir, "hearth.config.json"))
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return nil, err
	default:
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(c); err != nil {
			return nil, fmt.Errorf("hearth.config.json: %w", err)
		}
	}
	if c.DefaultLang == "" {
		c.DefaultLang = "en"
	}
	if c.I18n.Mode == "" {
		c.I18n.Mode = "prefix"
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	c.AssetBase = strings.TrimRight(c.AssetBase, "/")
	if c.Dev.Addr == "" {
		c.Dev.Addr = "127.0.0.1:3000"
	}
	return c, c.validate()
}

func (c *Config) validate() error {
	switch c.I18n.Mode {
	case "prefix":
	case "subdomain", "domain":
		if len(c.I18n.Hosts) == 0 {
			return fmt.Errorf("hearth.config.json: i18n.mode %q needs i18n.hosts", c.I18n.Mode)
		}
		for l, h := range c.I18n.Hosts {
			if !cache.ValidHost(h) {
				return fmt.Errorf("hearth.config.json: i18n.hosts[%q] = %q is not a valid host", l, h)
			}
		}
	default:
		return fmt.Errorf("hearth.config.json: unknown i18n.mode %q", c.I18n.Mode)
	}
	for pat, col := range c.Collections {
		if col.Data == "" || col.Param == "" {
			return fmt.Errorf("hearth.config.json: collection %q needs data and param", pat)
		}
	}
	if b := c.AssetBase; b != "" {
		if u, err := url.Parse(b); err != nil || u.RawQuery != "" || u.Fragment != "" ||
			!(strings.HasPrefix(b, "/") && !strings.HasPrefix(b, "//") || (u.Scheme == "https" || u.Scheme == "http") && u.Host != "") {
			return fmt.Errorf("hearth.config.json: assetBase %q must be an http(s) origin or a path starting with /", b)
		}
	}
	if v := c.Vite; v != nil && len(v.Command) == 0 {
		return fmt.Errorf("hearth.config.json: vite.command must not be empty")
	}
	return nil
}
