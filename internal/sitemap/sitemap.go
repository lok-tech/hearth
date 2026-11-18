// Package sitemap renders sitemap.xml.
package sitemap

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// XML renders a sitemap for absolute URLs.
func XML(urls []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, u := range urls {
		var esc strings.Builder
		_ = xml.EscapeText(&esc, []byte(u))
		fmt.Fprintf(&b, "<url><loc>%s</loc></url>\n", esc.String())
	}
	b.WriteString("</urlset>\n")
	return b.String()
}
