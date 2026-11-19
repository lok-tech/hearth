package manifest

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"strings"
)

// BundleFormat is the bundle format version. The build and the server both
// record it; a mismatch fails loudly.
//
// Format 3: manifest.json gains "entries" (every CSS file, preload hint and script of a Vite entry,
// for the {{entry}} helper). Format 2 added "ssr".
const BundleFormat = 3

// Manifest is dist/manifest.json.
type Manifest struct {
	Format int                    `json:"format"`
	Assets map[string]string      `json:"assets"` // logical name -> hashed URL path
	Images map[string][]ImageFile `json:"images"` // logical name -> variants
	// Entries maps a Vite entry ("src/site.ts") to everything a page needs to load it.
	Entries map[string]Entry `json:"entries"`
	Version string           `json:"version"` // build id
	// SSR lists the parameterless pages (URL paths such as "/candidates") that a Go handler
	// serves. The build does not prerender them; the server knows without reading the config.
	SSR []string `json:"ssr,omitempty"`

	// DevBase makes Asset return DevBase+"/"+name (Vite dev server). Lenient makes
	// unknown assets and images resolve to placeholders (hearth check, no Vite build).
	DevBase string `json:"-"`
	Lenient bool   `json:"-"`
}

// Entry is one Vite entry resolved against the whole Vite manifest: the script (JS is empty for a
// CSS-only entry), every stylesheet in cascade order (imported chunks first, the entry's own last),
// and the imported chunks to preload. Static imports only; dynamic imports are loaded on demand.
type Entry struct {
	JS      string   `json:"js"`
	CSS     []string `json:"css"`
	Preload []string `json:"preload"`
}

// ImageFile is one rendition of an image (AVIF, WebP, fallback).
type ImageFile struct {
	URL    string `json:"url"`
	Type   string `json:"type"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// Parse decodes and validates a manifest.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("hearth: manifest: %w", err)
	}
	if m.Format != BundleFormat {
		return nil, fmt.Errorf("hearth: bundle format %d, this library expects %d (rebuild with a matching hearth version)", m.Format, BundleFormat)
	}
	return &m, nil
}

// Load reads manifest.json from fsys.
func Load(fsys fs.FS, name string) (*Manifest, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("hearth: %w", err)
	}
	return Parse(b)
}

// PrefixURLs prepends base (a CDN origin or path) to every asset and image URL.
func (m *Manifest) PrefixURLs(base string) {
	for k, u := range m.Assets {
		m.Assets[k] = base + u
	}
	for k, e := range m.Entries {
		if e.JS != "" {
			e.JS = base + e.JS
		}
		for i := range e.CSS {
			e.CSS[i] = base + e.CSS[i]
		}
		for i := range e.Preload {
			e.Preload[i] = base + e.Preload[i]
		}
		m.Entries[k] = e
	}
	for _, files := range m.Images {
		for i := range files {
			files[i].URL = base + files[i].URL
		}
	}
}

// Asset resolves a logical asset name to its hashed URL; unknown names are an error.
func (m *Manifest) Asset(name string) (string, error) {
	if m.DevBase != "" {
		return m.DevBase + "/" + name, nil
	}
	u, ok := m.Assets[name]
	if !ok {
		if m.Lenient {
			return "/" + name, nil
		}
		return "", fmt.Errorf("hearth: asset %q not in manifest", name)
	}
	return u, nil
}

// Entry renders the tags that load a Vite entry: a stylesheet link per CSS file, a modulepreload
// link per imported chunk and the module script. URLs come from the manifest and are escaped.
// With a Vite dev server (DevBase) it emits the Vite client and the module script instead, as the
// dev server serves and hot-reloads the CSS. Lenient mode resolves an unknown entry to a bare
// script placeholder; otherwise an unknown entry is an error.
func (m *Manifest) Entry(name string) (template.HTML, error) {
	esc := template.HTMLEscapeString
	if m.DevBase != "" {
		return template.HTML(fmt.Sprintf("<script type=\"module\" src=\"%s\"></script>\n<script type=\"module\" src=\"%s\"></script>",
			esc(m.DevBase+"/@vite/client"), esc(m.DevBase+"/"+name))), nil
	}
	e, ok := m.Entries[name]
	if !ok {
		if m.Lenient {
			return template.HTML(fmt.Sprintf("<script type=\"module\" src=\"%s\"></script>", esc("/"+name))), nil
		}
		return "", fmt.Errorf("hearth: entry %q not in manifest", name)
	}
	var b strings.Builder
	for _, u := range e.CSS {
		fmt.Fprintf(&b, "<link rel=\"stylesheet\" href=\"%s\">\n", esc(u))
	}
	for _, u := range e.Preload {
		fmt.Fprintf(&b, "<link rel=\"modulepreload\" href=\"%s\">\n", esc(u))
	}
	if e.JS != "" {
		fmt.Fprintf(&b, "<script type=\"module\" src=\"%s\"></script>", esc(e.JS))
	}
	return template.HTML(strings.TrimRight(b.String(), "\n")), nil
}

// Picture renders a <picture> element with AVIF/WebP sources and a fallback <img>.
// Width/height are emitted to prevent layout shift; alt is required.
func (m *Manifest) Picture(name, alt string, lazy bool) (template.HTML, error) {
	files, ok := m.Images[name]
	if (!ok || len(files) == 0) && (m.Lenient || m.DevBase != "") {
		files, ok = []ImageFile{{URL: "/" + name, Type: "image/*"}}, true
	}
	if !ok || len(files) == 0 {
		return "", fmt.Errorf("hearth: image %q not in manifest", name)
	}
	var b strings.Builder
	b.WriteString("<picture>")
	last := files[len(files)-1]
	for _, f := range files[:len(files)-1] {
		fmt.Fprintf(&b, `<source type="%s" srcset="%s">`, template.HTMLEscapeString(f.Type), template.HTMLEscapeString(f.URL))
	}
	fmt.Fprintf(&b, `<img src="%s" alt="%s"`, template.HTMLEscapeString(last.URL), template.HTMLEscapeString(alt))
	if last.Width > 0 && last.Height > 0 {
		fmt.Fprintf(&b, ` width="%d" height="%d"`, last.Width, last.Height)
	}
	if lazy {
		b.WriteString(` loading="lazy" decoding="async"`)
	}
	b.WriteString("></picture>")
	return template.HTML(b.String()), nil
}
