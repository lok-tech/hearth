package sitemap

import (
	"strings"
	"testing"
	"time"
)

func TestXMLEscapesAndLists(t *testing.T) {
	out := XML([]URL{{Loc: "https://x.test/"}, {Loc: "https://x.test/a?b=1&c=<2>"}})
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
	if strings.Contains(out, "lastmod") {
		t.Errorf("no lastmod was set:\n%s", out)
	}
	if strings.Contains(XML(nil), "<url>") {
		t.Error("empty list must produce no entries")
	}
}

func TestLastModIsUTCAndOnlyWhenSet(t *testing.T) {
	zone := time.FixedZone("x", 2*3600)
	out := XML([]URL{
		{Loc: "https://x.test/a", LastMod: time.Date(2026, 3, 4, 1, 30, 5, 999, zone)},
		{Loc: "https://x.test/b"},
	})
	if !strings.Contains(out, "<url><loc>https://x.test/a</loc><lastmod>2026-03-03T23:30:05Z</lastmod></url>") {
		t.Errorf("lastmod:\n%s", out)
	}
	if !strings.Contains(out, "<url><loc>https://x.test/b</loc></url>") {
		t.Errorf("unset entry:\n%s", out)
	}
}

func TestSplit(t *testing.T) {
	var urls []URL
	for i := 0; i < 7; i++ {
		urls = append(urls, URL{Loc: "https://x.test/" + string(rune('a'+i))})
	}
	parts := Split(urls, 3, 0)
	if len(parts) != 3 || len(parts[0]) != 3 || len(parts[1]) != 3 || len(parts[2]) != 1 || parts[2][0].Loc != "https://x.test/g" {
		t.Fatalf("by count: %v", parts)
	}
	if got := Split(urls, 0, 0); len(got) != 1 || len(got[0]) != 7 {
		t.Fatalf("fits: %v", got)
	}
	// By size: a limit that holds the wrapper plus two entries.
	two := len(header+urlsetOpen+urlsetClose) + 2*len(entry(urls[0]))
	if got := Split(urls, 0, two); len(got) != 4 {
		t.Fatalf("by bytes: %d parts", len(got))
	}
	for _, p := range Split(urls, 0, two) {
		if len(XML(p)) > two {
			t.Errorf("part exceeds the byte limit: %d > %d", len(XML(p)), two)
		}
	}
	if got := Split(nil, 0, 0); len(got) != 1 || len(got[0]) != 0 {
		t.Fatalf("empty: %v", got)
	}
}

func TestIndex(t *testing.T) {
	out := Index([]string{"https://x.test/sitemap-1.xml", "https://x.test/sitemap-2.xml?a=1&b=2"})
	for _, want := range []string{"<sitemapindex ", "<sitemap><loc>https://x.test/sitemap-1.xml</loc></sitemap>", "a=1&amp;b=2", "</sitemapindex>"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}
