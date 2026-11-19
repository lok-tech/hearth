package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/lok-tech/hearth/internal/cache"
)

// Config is hearth.config.json.
type Config struct {
	BaseURL     string                `json:"baseURL"`
	AssetBase   string                `json:"assetBase"` // CDN/S3 origin or path prefix for hashed assets, e.g. https://cdn.example.com
	DefaultLang string                `json:"defaultLang"`
	I18n        I18nConfig            `json:"i18n"`
	Vite        *ViteConfig           `json:"vite"`
	Collections map[string]Collection `json:"collections"` // pattern -> bound local collection
	// SSR lists parameterless pages ("/candidates", "/") whose data comes from a Go handler. They
	// are not prerendered by `hearth build`, so no file shadows the handler.
	SSR []string `json:"ssr"`
	// Funcs names the custom template functions your Go program registers (hearth.Options.Funcs).
	// The CLI registers stubs for them so templates still parse; calling one is an error.
	Funcs []string  `json:"funcs"`
	Dev   DevConfig `json:"dev"`
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
	// Proxy maps a path prefix to a backend URL for `hearth dev` (mock and watch modes):
	// {"/api": "http://localhost:8080"}. Dev only; preview and production ignore it.
	Proxy map[string]string `json:"proxy"`
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
	seen := map[string]bool{}
	for _, p := range c.SSR {
		if !strings.HasPrefix(p, "/") || p != "/" && strings.HasSuffix(p, "/") || strings.ContainsAny(p, "{}[]") {
			return fmt.Errorf("hearth.config.json: ssr entry %q must be a page path such as \"/candidates\" (no parameters, no trailing slash)", p)
		}
		if seen[p] {
			return fmt.Errorf("hearth.config.json: ssr entry %q is listed twice", p)
		}
		seen[p] = true
	}
	seen = map[string]bool{}
	for _, n := range c.Funcs {
		if n == "" || strings.ContainsAny(n, " \t.{}()\"") || seen[n] {
			return fmt.Errorf("hearth.config.json: funcs entry %q must be a unique function name", n)
		}
		seen[n] = true
	}
	if b := c.AssetBase; b != "" {
		if u, err := url.Parse(b); err != nil || u.RawQuery != "" || u.Fragment != "" ||
			!(strings.HasPrefix(b, "/") && !strings.HasPrefix(b, "//") || (u.Scheme == "https" || u.Scheme == "http") && u.Host != "") {
			return fmt.Errorf("hearth.config.json: assetBase %q must be an http(s) origin or a path starting with /", b)
		}
	}
	if err := ValidateProxy(c.Dev.Proxy); err != nil {
		return err
	}
	if v := c.Vite; v != nil && len(v.Command) == 0 {
		return fmt.Errorf("hearth.config.json: vite.command must not be empty")
	}
	return nil
}

// reservedProxyPaths are answered by Hearth itself and can never be proxied.
var reservedProxyPaths = []string{"/healthz", "/robots.txt", "/sitemap.xml"}

// ValidateProxy checks dev.proxy: every prefix is an absolute path of plain segments without a
// trailing slash (so "/api" and "/api/" cannot both be listed), never "/" (it would swallow the
// site) and never one of Hearth's own paths; every target is an absolute http(s) URL.
func ValidateProxy(m map[string]string) error {
	for prefix, target := range m {
		bad := func(why string) error {
			return fmt.Errorf("hearth.config.json: dev.proxy prefix %q %s", prefix, why)
		}
		switch {
		case prefix == "/":
			return bad("would proxy the whole site; list the backend paths (such as \"/api\") instead")
		case !strings.HasPrefix(prefix, "/"):
			return bad("must start with /")
		case strings.HasSuffix(prefix, "/"):
			return bad("must not end with / (write \"/api\", it already matches \"/api/x\")")
		case strings.ContainsAny(prefix, "?#{}[]\\ \t") || strings.Contains(prefix, "%"):
			return bad("must be a plain path (no query, fragment, spaces, braces or escapes)")
		}
		for _, seg := range strings.Split(prefix[1:], "/") {
			if seg == "" || seg == "." || seg == ".." {
				return bad("must not contain empty, \".\" or \"..\" segments")
			}
		}
		for _, r := range reservedProxyPaths {
			if prefix == r || strings.HasPrefix(prefix, r+"/") {
				return bad("is served by Hearth itself and cannot be proxied")
			}
		}
		u, err := url.Parse(target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Fragment != "" {
			return fmt.Errorf("hearth.config.json: dev.proxy[%q] = %q must be an absolute http(s) URL such as \"http://localhost:8080\"", prefix, target)
		}
	}
	return nil
}
