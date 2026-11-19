// Package sitemap renders sitemap.xml and the sitemap index that replaces it above the protocol's
// limits.
package sitemap

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// The sitemap protocol allows 50,000 URLs and 50 MB (uncompressed) per file.
const (
	MaxURLs  = 50000
	MaxBytes = 50 * 1024 * 1024
)

// URL is one sitemap entry. A zero LastMod writes no <lastmod>.
type URL struct {
	Loc     string
	LastMod time.Time
}

const (
	header      = `<?xml version="1.0" encoding="UTF-8"?>` + "\n"
	urlsetOpen  = `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n"
	indexOpen   = `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n"
	urlsetClose = "</urlset>\n"
)

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// entry renders one <url>; <lastmod> is a W3C datetime in UTC, written only when set.
func entry(u URL) string {
	if u.LastMod.IsZero() {
		return fmt.Sprintf("<url><loc>%s</loc></url>\n", esc(u.Loc))
	}
	return fmt.Sprintf("<url><loc>%s</loc><lastmod>%s</lastmod></url>\n", esc(u.Loc), u.LastMod.UTC().Truncate(time.Second).Format(time.RFC3339))
}

// XML renders a sitemap (urlset) for the URLs, in the order given.
func XML(urls []URL) string {
	var b strings.Builder
	b.WriteString(header + urlsetOpen)
	for _, u := range urls {
		b.WriteString(entry(u))
	}
	b.WriteString(urlsetClose)
	return b.String()
}

// Split cuts urls into consecutive files that each stay within maxURLs entries and maxBytes of
// rendered XML. Zero limits mean the protocol's. The input order is kept, so a sorted list gives
// a stable split. One file comes back when everything fits.
func Split(urls []URL, maxURLs, maxBytes int) [][]URL {
	if maxURLs <= 0 {
		maxURLs = MaxURLs
	}
	if maxBytes <= 0 {
		maxBytes = MaxBytes
	}
	overhead := len(header) + len(urlsetOpen) + len(urlsetClose)
	var out [][]URL
	var cur []URL
	size := overhead
	for _, u := range urls {
		n := len(entry(u))
		if len(cur) > 0 && (len(cur) >= maxURLs || size+n > maxBytes) {
			out = append(out, cur)
			cur, size = nil, overhead
		}
		cur = append(cur, u)
		size += n
	}
	if len(cur) > 0 || len(out) == 0 {
		out = append(out, cur)
	}
	return out
}

// Index renders a sitemapindex that points at the given absolute file URLs.
func Index(locs []string) string {
	var b strings.Builder
	b.WriteString(header + indexOpen)
	for _, l := range locs {
		fmt.Fprintf(&b, "<sitemap><loc>%s</loc></sitemap>\n", esc(l))
	}
	b.WriteString("</sitemapindex>\n")
	return b.String()
}
