package sitemap

import (
	"strings"
	"testing"
)

func TestXMLEscapesAndLists(t *testing.T) {
	out := XML([]string{"https://x.test/", "https://x.test/a?b=1&c=<2>"})
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		"<loc>https://x.test/</loc>",
		"<loc>https://x.test/a?b=1&amp;c=&lt;2&gt;</loc>",
		"</urlset>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(XML(nil), "<url>") {
		t.Error("empty list must produce no entries")
	}
}
