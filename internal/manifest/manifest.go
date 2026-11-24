package manifest

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"sort"
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

// HMR is false: a bundle manifest serves hashed files. Vite reports true.
func (m *Manifest) HMR() bool { return false }

// Asset resolves a logical asset name to its hashed URL; unknown names are an error.
func (m *Manifest) Asset(name string) (string, error) {
	u, ok := m.Assets[name]
	if !ok {
		return "", fmt.Errorf("hearth: asset %q not in manifest", name)
	}
	return u, nil
}

// Entry renders the tags that load a Vite entry: a stylesheet link per CSS file, a modulepreload
// link per imported chunk and the module script. URLs come from the manifest and are escaped.
// An unknown entry is an error.
func (m *Manifest) Entry(name string) (template.HTML, error) {
	e, ok := m.Entries[name]
	if !ok {
		return "", fmt.Errorf("hearth: entry %q not in manifest", name)
	}
	return entryTags(e), nil
}

// entryTags renders the tags of one known entry.
func entryTags(e Entry) template.HTML {
	esc := template.HTMLEscapeString
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
	return template.HTML(strings.TrimRight(b.String(), "\n"))
}

// formatGroup is every rendition of one image type (AVIF, WebP, the original format), narrowest first.
type formatGroup struct {
	typ   string
	files []ImageFile
}

// groupFormats groups an image's entries by type, in the order types first appear (the plugin writes
// AVIF, WebP, then the original format last), and sorts each group by width.
func groupFormats(name string, files []ImageFile) ([]formatGroup, error) {
	var groups []formatGroup
	for _, f := range files {
		gi := -1
		for i := range groups {
			if groups[i].typ == f.Type {
				gi = i
			}
		}
		if gi < 0 {
			groups = append(groups, formatGroup{typ: f.Type})
			gi = len(groups) - 1
		}
		groups[gi].files = append(groups[gi].files, f)
	}
	for _, g := range groups {
		if len(g.files) < 2 {
			continue
		}
		for _, f := range g.files {
			if f.Width <= 0 {
				return nil, fmt.Errorf("hearth: image %q: several %s renditions need a width each", name, g.typ)
			}
			if strings.ContainsAny(f.URL, " \t\r\n,") {
				return nil, fmt.Errorf("hearth: image %q: url %q cannot be used in a srcset", name, f.URL)
			}
		}
		sort.SliceStable(g.files, func(a, b int) bool { return g.files[a].Width < g.files[b].Width })
	}
	return groups, nil
}

// srcset is the bare URL for one rendition, or "url 480w, url 960w" for several.
func (g formatGroup) srcset() string {
	if len(g.files) == 1 {
		return template.HTMLEscapeString(g.files[0].URL)
	}
	parts := make([]string, len(g.files))
	for i, f := range g.files {
		parts[i] = fmt.Sprintf("%s %dw", template.HTMLEscapeString(f.URL), f.Width)
	}
	return strings.Join(parts, ", ")
}

func (g formatGroup) multi() bool { return len(g.files) > 1 }

// lookup returns an image's groups; with lenient set an unknown name becomes one placeholder file.
func (m *Manifest) lookup(name string, lenient bool) ([]formatGroup, error) {
	files, ok := m.Images[name]
	if (!ok || len(files) == 0) && lenient {
		files, ok = []ImageFile{{URL: "/" + name, Type: "image/*"}}, true
	}
	if !ok || len(files) == 0 {
		return nil, fmt.Errorf("hearth: image %q not in manifest", name)
	}
	return groupFormats(name, files)
}

// sizesAttr is the sizes attribute for a srcset that has width descriptors: the given value, or
// "100vw" (the browser's own default) when none was given. Empty for a single-width srcset.
func sizesAttr(g formatGroup, sizes string) string {
	if !g.multi() {
		return ""
	}
	if sizes == "" {
		sizes = "100vw"
	}
	return fmt.Sprintf(` sizes="%s"`, template.HTMLEscapeString(sizes))
}

func writeSource(b *strings.Builder, media string, g formatGroup, sizes string) {
	b.WriteString("<source")
	if media != "" {
		fmt.Fprintf(b, ` media="%s"`, template.HTMLEscapeString(media))
	}
	if g.typ != "image/*" {
		fmt.Fprintf(b, ` type="%s"`, template.HTMLEscapeString(g.typ))
	}
	fmt.Fprintf(b, ` srcset="%s"%s>`, g.srcset(), sizesAttr(g, sizes))
}

// writeImg writes the fallback <img>: the largest rendition of the last (original) format.
func writeImg(b *strings.Builder, groups []formatGroup, alt, sizes string, lazy bool) {
	g := groups[len(groups)-1]
	largest := g.files[len(g.files)-1]
	fmt.Fprintf(b, `<img src="%s"`, template.HTMLEscapeString(largest.URL))
	if g.multi() {
		fmt.Fprintf(b, ` srcset="%s"%s`, g.srcset(), sizesAttr(g, sizes))
	}
	fmt.Fprintf(b, ` alt="%s"`, template.HTMLEscapeString(alt))
	if largest.Width > 0 && largest.Height > 0 {
		fmt.Fprintf(b, ` width="%d" height="%d"`, largest.Width, largest.Height)
	}
	if lazy {
		b.WriteString(` loading="lazy" decoding="async"`)
	}
	b.WriteString(">")
}

// Picture renders a <picture> element with one <source> per non-fallback format and a fallback <img>.
// Several widths per format become a "url 480w, url 960w" srcset (with sizes, "100vw" when empty);
// one width is a bare URL, exactly as before widths existed. Width/height are emitted to prevent
// layout shift. lazy adds loading="lazy" decoding="async".
func (m *Manifest) Picture(name, alt, sizes string, lazy bool) (template.HTML, error) {
	return m.picture(false, name, alt, sizes, lazy)
}

func (m *Manifest) picture(lenient bool, name, alt, sizes string, lazy bool) (template.HTML, error) {
	groups, err := m.lookup(name, lenient)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("<picture>")
	for _, g := range groups[:len(groups)-1] {
		writeSource(&b, "", g, sizes)
	}
	writeImg(&b, groups, alt, sizes, lazy)
	b.WriteString("</picture>")
	return template.HTML(b.String()), nil
}

// ArtSource is one art-directed source of PictureSet: the image to use when Media matches.
type ArtSource struct {
	Media string
	Name  string
}

func validMedia(media string) error {
	for _, c := range media {
		if c == '"' || c < 0x20 || c == 0x7f {
			return fmt.Errorf("hearth: pictureSet: media %q must not contain a double quote or control characters", media)
		}
	}
	return nil
}

// PictureSet renders a <picture> whose sources switch image by media query. Every art source emits one
// <source media type srcset> per format; fallback supplies the format sources without media and the <img>.
func (m *Manifest) PictureSet(alt, sizes string, art []ArtSource, fallback string, lazy bool) (template.HTML, error) {
	return m.pictureSet(false, alt, sizes, art, fallback, lazy)
}

func (m *Manifest) pictureSet(lenient bool, alt, sizes string, art []ArtSource, fallback string, lazy bool) (template.HTML, error) {
	var b strings.Builder
	b.WriteString("<picture>")
	for _, a := range art {
		if a.Media == "" {
			return "", fmt.Errorf("hearth: pictureSet: only the last image may have an empty media")
		}
		if err := validMedia(a.Media); err != nil {
			return "", err
		}
		groups, err := m.lookup(a.Name, lenient)
		if err != nil {
			return "", err
		}
		for _, g := range groups {
			writeSource(&b, a.Media, g, sizes)
		}
	}
	groups, err := m.lookup(fallback, lenient)
	if err != nil {
		return "", err
	}
	for _, g := range groups[:len(groups)-1] {
		writeSource(&b, "", g, sizes)
	}
	writeImg(&b, groups, alt, sizes, lazy)
	b.WriteString("</picture>")
	return template.HTML(b.String()), nil
}

// PictureArgs is the template helper {{picture "name" "alt" ["sizes"] ["eager"]}}.
func (m *Manifest) PictureArgs(args ...string) (template.HTML, error) {
	return m.pictureArgs(false, args)
}

func (m *Manifest) pictureArgs(lenient bool, args []string) (template.HTML, error) {
	lazy := true
	if n := len(args); n >= 3 && args[n-1] == "eager" {
		lazy = false
		args = args[:n-1]
	}
	if len(args) < 2 || len(args) > 3 {
		return "", fmt.Errorf(`hearth: picture: want "name" "alt" ["sizes"] ["eager"], got %d arguments`, len(args))
	}
	sizes := ""
	if len(args) == 3 {
		if args[2] == "" {
			return "", fmt.Errorf("hearth: picture %q: sizes must not be empty (leave it out for the default)", args[0])
		}
		sizes = args[2]
	}
	return m.picture(lenient, args[0], args[1], sizes, lazy)
}

// PictureSetArgs is the template helper
// {{pictureSet "alt" "sizes" "(media)" "name" ... "" "fallback" ["eager"]}}.
func (m *Manifest) PictureSetArgs(args ...string) (template.HTML, error) {
	return m.pictureSetArgs(false, args)
}

func (m *Manifest) pictureSetArgs(lenient bool, args []string) (template.HTML, error) {
	if len(args) < 2 {
		return "", fmt.Errorf(`hearth: pictureSet: want "alt" "sizes" then "media" "name" pairs, got %d arguments`, len(args))
	}
	lazy := true
	if rest := args[2:]; len(rest)%2 == 1 {
		if rest[len(rest)-1] != "eager" {
			return "", fmt.Errorf(`hearth: pictureSet: the images after "alt" "sizes" come in "media" "name" pairs (the last argument %q is unpaired; "eager" is the only trailing option)`, rest[len(rest)-1])
		}
		lazy = false
		args = args[:len(args)-1]
	}
	alt, sizes, pairs := args[0], args[1], args[2:]
	if len(pairs) == 0 {
		return "", fmt.Errorf(`hearth: pictureSet: needs at least the fallback pair "" "name"`)
	}
	art := make([]ArtSource, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		art = append(art, ArtSource{Media: pairs[i], Name: pairs[i+1]})
	}
	last := art[len(art)-1]
	if last.Media != "" {
		return "", fmt.Errorf(`hearth: pictureSet: the last pair must be the fallback with an empty media ("" %q)`, last.Name)
	}
	return m.pictureSet(lenient, alt, sizes, art[:len(art)-1], last.Name, lazy)
}
