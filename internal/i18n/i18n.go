package i18n

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// Catalogs holds JSON translation catalogs (locales/<lang>.json), flat key -> text.
// Plurals use CLDR category suffixes: "items.one", "items.other", ...
type Catalogs struct {
	langs map[string]map[string]string
}

// Load reads every <dir>/<lang>.json from fsys.
func Load(fsys fs.FS, dir string) (*Catalogs, error) {
	c := &Catalogs{langs: map[string]map[string]string{}}
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("hearth: locales: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, err
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("hearth: locales/%s: %w (catalogs are flat string maps)", e.Name(), err)
		}
		c.langs[strings.TrimSuffix(e.Name(), ".json")] = m
	}
	if len(c.langs) == 0 {
		return nil, fmt.Errorf("hearth: no catalogs in %s", dir)
	}
	return c, nil
}

// Langs lists the available languages, sorted.
func (c *Catalogs) Langs() []string {
	out := make([]string, 0, len(c.langs))
	for l := range c.langs {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Has reports whether lang has a catalog.
func (c *Catalogs) Has(lang string) bool { _, ok := c.langs[lang]; return ok }

// T looks up key. A missing key is an error (the build fails; no silent fallback).
func (c *Catalogs) T(lang, key string) (string, error) {
	m, ok := c.langs[lang]
	if !ok {
		return "", fmt.Errorf("hearth: unknown language %q", lang)
	}
	s, ok := m[key]
	if !ok {
		return "", fmt.Errorf("hearth: missing translation %q for %q", key, lang)
	}
	return s, nil
}

// Plural picks key.<category> for n, falling back to key.other.
// "{n}" in the text is replaced by n.
func (c *Catalogs) Plural(lang, key string, n int) (string, error) {
	cat := pluralCategory(lang, n)
	s, err := c.T(lang, key+"."+cat)
	if err != nil {
		var err2 error
		s, err2 = c.T(lang, key+".other")
		if err2 != nil {
			return "", err
		}
	}
	return strings.ReplaceAll(s, "{n}", fmt.Sprint(n)), nil
}

// pluralCategory implements CLDR integer rules for a small set of languages;
// unknown languages use the English rule.
func pluralCategory(lang string, n int) string {
	if n < 0 {
		n = -n
	}
	switch lang {
	case "he", "iw":
		switch {
		case n == 1:
			return "one"
		case n == 2:
			return "two"
		default:
			return "other"
		}
	case "ar":
		switch {
		case n == 0:
			return "zero"
		case n == 1:
			return "one"
		case n == 2:
			return "two"
		case n%100 >= 3 && n%100 <= 10:
			return "few"
		case n%100 >= 11:
			return "many"
		default:
			return "other"
		}
	case "ru", "uk":
		switch {
		case n%10 == 1 && n%100 != 11:
			return "one"
		case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
			return "few"
		default:
			return "many"
		}
	case "ja", "zh", "ko", "vi", "th", "id":
		return "other"
	case "fr", "pt":
		if n == 0 || n == 1 {
			return "one"
		}
		return "other"
	default:
		if n == 1 {
			return "one"
		}
		return "other"
	}
}

// Missing returns keys present in the reference language but absent in others,
// as "lang:key". `hearth check` uses it to fail before a build.
func (c *Catalogs) Missing(ref string) []string {
	var out []string
	for l, m := range c.langs {
		if l == ref {
			continue
		}
		for k := range c.langs[ref] {
			if _, ok := m[k]; !ok {
				out = append(out, l+":"+k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// IsRTL reports whether lang is written right to left.
func IsRTL(lang string) bool {
	switch lang {
	case "he", "ar", "fa", "ur":
		return true
	}
	return false
}
