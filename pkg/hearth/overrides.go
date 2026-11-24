package hearth

import (
	"fmt"
	"os"
	"strings"
)

// Overrides changes how a Site's layers behave. The zero value changes nothing: a Site starts as the
// production setup and each non-empty field replaces one default.
//
// New reads the HEARTH_* environment variables on top of Options.Overrides, and the environment
// always wins, so a developer can change a server's behavior without touching its code:
//
//	HEARTH_TEMPLATES    once | request                  request: re-read and re-parse the templates on every request
//	HEARTH_CACHE        disk | off                      off: render every request, keep nothing, send no-store
//	HEARTH_ASSETS       manifest | placeholder | vite:<url>
//	HEARTH_DIAGNOSTICS  0 | 1                           1: 404 reasons and render errors in the response
//	HEARTH_BUNDLE       <directory>                     read the templates from this directory instead of Options.Bundle
//	HEARTH_ENV          dev                             preset for request + off + diagnostics; the variables above win
//
// HEARTH_BUNDLE is how one server runs in both places: in production Options.Bundle is the build's
// dist/templates; in development HEARTH_BUNDLE=. (the site's source directory) makes request-mode edits show,
// and with no manifest.json there the "ssr" list is read from its hearth.config.json.
//
// A value that is not listed is an error from New. Other HEARTH_* names are not touched: deployments
// use their own (HEARTH_CACHE_DIR, HEARTH_TMP_DIR) for Options.CacheDir and Options.TmpDir.
type Overrides struct {
	Templates   string // "", "once" or "request"
	Cache       string // "", "disk" or "off"
	Assets      string // "", "manifest", "placeholder" or "vite:<url>"
	Diagnostics string // "", "0" or "1"
	Bundle      string // "", or a directory that replaces Options.Bundle
}

// OverridesFromEnv reads the HEARTH_* variables from the process environment.
func OverridesFromEnv() (Overrides, error) { return overridesFrom(os.Environ()) }

// overridesFrom parses KEY=VALUE pairs. The HEARTH_ENV preset is applied first, then the
// individual variables, so an individual variable wins.
func overridesFrom(environ []string) (Overrides, error) {
	vars := map[string]string{}
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			switch k {
			case "HEARTH_TEMPLATES", "HEARTH_CACHE", "HEARTH_ASSETS", "HEARTH_DIAGNOSTICS", "HEARTH_BUNDLE", "HEARTH_ENV":
				vars[k] = v
			}
		}
	}
	var ov Overrides
	switch env := vars["HEARTH_ENV"]; env {
	case "":
	case "dev":
		ov = Overrides{Templates: "request", Cache: "off", Diagnostics: "1"}
	default:
		return ov, fmt.Errorf("hearth: HEARTH_ENV=%q: want dev", env)
	}
	if v, ok := vars["HEARTH_TEMPLATES"]; ok && v != "" {
		ov.Templates = v
	}
	if v, ok := vars["HEARTH_CACHE"]; ok && v != "" {
		ov.Cache = v
	}
	if v, ok := vars["HEARTH_ASSETS"]; ok && v != "" {
		ov.Assets = v
	}
	if v, ok := vars["HEARTH_DIAGNOSTICS"]; ok && v != "" {
		ov.Diagnostics = v
	}
	if v, ok := vars["HEARTH_BUNDLE"]; ok && v != "" {
		ov.Bundle = v
	}
	return ov, ov.validate()
}

func (o Overrides) validate() error {
	oneOf := func(name, v string, ok ...string) error {
		if v == "" {
			return nil
		}
		for _, w := range ok {
			if v == w {
				return nil
			}
		}
		return fmt.Errorf("hearth: %s=%q: want %s", name, v, strings.Join(ok, " | "))
	}
	if err := oneOf("HEARTH_TEMPLATES", o.Templates, "once", "request"); err != nil {
		return err
	}
	if err := oneOf("HEARTH_CACHE", o.Cache, "disk", "off"); err != nil {
		return err
	}
	if err := oneOf("HEARTH_DIAGNOSTICS", o.Diagnostics, "0", "1"); err != nil {
		return err
	}
	if v := o.Assets; v != "" && v != "manifest" && v != "placeholder" {
		u, ok := strings.CutPrefix(v, "vite:")
		if !ok || !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") || strings.TrimRight(u, "/") == "" {
			return fmt.Errorf("hearth: HEARTH_ASSETS=%q: want manifest | placeholder | vite:<http(s) url>", v)
		}
	}
	return nil
}

// over returns a with every non-empty field of b on top.
func (a Overrides) over(b Overrides) Overrides {
	pick := func(x, y string) string {
		if y != "" {
			return y
		}
		return x
	}
	return Overrides{Templates: pick(a.Templates, b.Templates), Cache: pick(a.Cache, b.Cache),
		Assets: pick(a.Assets, b.Assets), Diagnostics: pick(a.Diagnostics, b.Diagnostics), Bundle: pick(a.Bundle, b.Bundle)}
}

// active lists the non-empty fields as name=value, for the startup warning.
func (o Overrides) active() []any {
	var out []any
	for _, f := range []struct{ name, v string }{{"templates", o.Templates}, {"cache", o.Cache}, {"assets", o.Assets}, {"diagnostics", o.Diagnostics}, {"bundle", o.Bundle}} {
		if f.v != "" {
			out = append(out, f.name, f.v)
		}
	}
	return out
}
