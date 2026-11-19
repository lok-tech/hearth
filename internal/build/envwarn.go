package build

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	envUse     = regexp.MustCompile(`import\.meta\.env(?:\.([A-Za-z_][A-Za-z0-9_]*)|\[\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]\s*\])`)
	envPrefix  = regexp.MustCompile(`envPrefix\s*:\s*(\[[^\]]*\]|'[^']*'|"[^"]*"|` + "`[^`]*`" + `)`)
	quoted     = regexp.MustCompile(`'([^']*)'|"([^"]*)"|` + "`([^`]*)`")
	blockCmt   = regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineCmt    = regexp.MustCompile(`(?m)(^|\s)//[^\n]*`)
	viteConfig = []string{"vite.config.ts", "vite.config.mts", "vite.config.cts", "vite.config.js", "vite.config.mjs", "vite.config.cjs"}
	// Vite's own variables are always defined, whatever envPrefix says.
	envBuiltin = map[string]bool{"MODE": true, "DEV": true, "PROD": true, "BASE_URL": true, "SSR": true}
	srcExt     = map[string]bool{".ts": true, ".tsx": true, ".mts": true, ".js": true, ".jsx": true, ".mjs": true, ".vue": true, ".svelte": true, ".html": true}
)

// Warnings returns advice from `hearth check` that never fails a check: a miss is acceptable, a
// false failure is not. Today it covers one thing: src/** reads import.meta.env.<NAME> but the
// site's vite.config.* has no envPrefix that covers NAME, so Vite leaves the value undefined in the
// client. It is a text scan; Vite is not run, and an envPrefix it cannot read as a literal (a
// variable, a spread) silences the warning.
func Warnings(siteDir string) []string {
	uses := map[string][]string{} // NAME -> files
	_ = filepath.WalkDir(filepath.Join(siteDir, "src"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !srcExt[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil || len(b) > 2<<20 {
			return nil
		}
		rel, _ := filepath.Rel(siteDir, p)
		for _, m := range envUse.FindAllStringSubmatch(string(b), -1) {
			name := m[1] + m[2]
			if !envBuiltin[name] {
				uses[name] = append(uses[name], filepath.ToSlash(rel))
			}
		}
		return nil
	})
	if len(uses) == 0 {
		return nil
	}
	prefixes, cfgName, known := readEnvPrefixes(siteDir)
	if !known {
		return nil
	}
	var names []string
	for n := range uses {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		covered := false
		for _, p := range prefixes {
			if p != "" && strings.HasPrefix(n, p) {
				covered = true
			}
		}
		if covered {
			continue
		}
		files := uses[n]
		sort.Strings(files)
		files = dedupe(files)
		where := cfgName + " has no envPrefix covering it"
		if cfgName == "" {
			where = "there is no vite.config.* (Vite exposes only VITE_ by default)"
		}
		out = append(out, fmt.Sprintf("%s: import.meta.env.%s stays undefined in the client: %s; add envPrefix: ['VITE_', 'PUBLIC_'] to the Vite config (every prefixed variable is public, never put a secret under one)",
			strings.Join(files, ", "), n, where))
	}
	return out
}

// readEnvPrefixes returns the prefixes the site's Vite config exposes (always VITE_), the config's
// file name (empty when there is none), and whether the answer is trustworthy (false when the
// config sets envPrefix to something that is not a literal).
func readEnvPrefixes(siteDir string) (prefixes []string, name string, known bool) {
	prefixes = []string{"VITE_"}
	for _, n := range viteConfig {
		b, err := os.ReadFile(filepath.Join(siteDir, n))
		if err != nil {
			continue
		}
		name = n
		text := blockCmt.ReplaceAllString(string(b), "")
		text = lineCmt.ReplaceAllString(text, "$1")
		if !strings.Contains(text, "envPrefix") {
			return prefixes, name, true
		}
		m := envPrefix.FindStringSubmatch(text)
		if m == nil {
			return nil, name, false
		}
		for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
			prefixes = append(prefixes, q[1]+q[2]+q[3])
		}
		return prefixes, name, true
	}
	return prefixes, "", true
}

func dedupe(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
