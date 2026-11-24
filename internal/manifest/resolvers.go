package manifest

import (
	"fmt"
	"html/template"
)

// The bundle manifest (*Manifest) is the strict resolver: an asset, entry or image it does not list is
// an error. Placeholder and Vite are the two others. All three satisfy render.Assets.

// Placeholder resolves what the manifest lists the way the manifest does and turns anything unknown
// into a placeholder, so a page renders without a Vite build (`hearth check`, a source tree).
type Placeholder struct{ M *Manifest }

func (Placeholder) HMR() bool { return false }

func (p Placeholder) Asset(name string) (string, error) {
	if u, err := p.M.Asset(name); err == nil {
		return u, nil
	}
	return "/" + name, nil
}

func (p Placeholder) Entry(name string) (template.HTML, error) {
	if e, ok := p.M.Entries[name]; ok {
		return entryTags(e), nil
	}
	return template.HTML(fmt.Sprintf("<script type=\"module\" src=\"%s\"></script>", template.HTMLEscapeString("/"+name))), nil
}

func (p Placeholder) PictureArgs(args ...string) (template.HTML, error) {
	return p.M.pictureArgs(true, args)
}
func (p Placeholder) PictureSetArgs(args ...string) (template.HTML, error) {
	return p.M.pictureSetArgs(true, args)
}

// Vite resolves assets and entries to a Vite dev server at Base (no trailing slash): the server
// serves and hot-reloads them, so there is no hashed file to look up. Images still come from the
// manifest when it lists them and are placeholders otherwise.
type Vite struct {
	M    *Manifest
	Base string
}

func (Vite) HMR() bool { return true }

func (v Vite) Asset(name string) (string, error) { return v.Base + "/" + name, nil }

func (v Vite) Entry(name string) (template.HTML, error) {
	esc := template.HTMLEscapeString
	return template.HTML(fmt.Sprintf("<script type=\"module\" src=\"%s\"></script>\n<script type=\"module\" src=\"%s\"></script>",
		esc(v.Base+"/@vite/client"), esc(v.Base+"/"+name))), nil
}

func (v Vite) PictureArgs(args ...string) (template.HTML, error) { return v.M.pictureArgs(true, args) }
func (v Vite) PictureSetArgs(args ...string) (template.HTML, error) {
	return v.M.pictureSetArgs(true, args)
}
