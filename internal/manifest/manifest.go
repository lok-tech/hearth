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
const BundleFormat = 1

// Manifest is dist/manifest.json.
type Manifest struct {
	Format  int                    `json:"format"`
	Assets  map[string]string      `json:"assets"`  // logical name -> hashed URL path
	Images  map[string][]ImageFile `json:"images"`  // logical name -> variants
	Version string                 `json:"version"` // build id

	// DevBase makes Asset return DevBase+"/"+name (Vite dev server). Lenient makes
	// unknown assets and images resolve to placeholders (hearth check, no Vite build).
	DevBase string `json:"-"`
	Lenient bool   `json:"-"`
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
