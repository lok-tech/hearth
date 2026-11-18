package i18n

import (
	"testing"
	"testing/fstest"
)

func testCatalogs(t *testing.T) *Catalogs {
	t.Helper()
	c, err := Load(fstest.MapFS{
		"locales/en.json": {Data: []byte(`{"hi":"Hello","items.one":"{n} item","items.other":"{n} items","only_en":"x"}`)},
		"locales/he.json": {Data: []byte(`{"hi":"שלום","items.one":"פריט","items.two":"שני פריטים","items.other":"{n} פריטים"}`)},
	}, "locales")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCatalogT(t *testing.T) {
	c := testCatalogs(t)
	if s, err := c.T("he", "hi"); err != nil || s != "שלום" {
		t.Fatalf("%q %v", s, err)
	}
	if _, err := c.T("he", "nope"); err == nil {
		t.Fatal("missing key must fail")
	}
	if _, err := c.T("fr", "hi"); err == nil {
		t.Fatal("unknown lang must fail")
	}
}

func TestPlural(t *testing.T) {
	c := testCatalogs(t)
	cases := []struct {
		lang string
		n    int
		want string
	}{{"en", 1, "1 item"}, {"en", 5, "5 items"}, {"en", 0, "0 items"}, {"he", 1, "פריט"}, {"he", 2, "שני פריטים"}, {"he", 7, "7 פריטים"}}
	for _, tc := range cases {
		got, err := c.Plural(tc.lang, "items", tc.n)
		if err != nil || got != tc.want {
			t.Errorf("Plural(%s,%d) = %q, %v; want %q", tc.lang, tc.n, got, err, tc.want)
		}
	}
}

func TestMissing(t *testing.T) {
	c := testCatalogs(t)
	got := c.Missing("en")
	if len(got) != 1 || got[0] != "he:only_en" {
		t.Fatalf("missing = %v", got)
	}
}
