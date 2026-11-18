package i18n

import "testing"

func prefixURLs() URLs {
	return URLs{Mode: "prefix", Default: "en", BaseURL: "https://x.test", Langs: []string{"en", "he"}}
}

func hostURLs() URLs {
	return URLs{Mode: "subdomain", Default: "en", BaseURL: "https://x.test",
		Hosts: map[string]string{"en": "x.test", "he": "he.x.test"}, Langs: []string{"en", "he"}}
}

func TestPrefixLocationAndAbs(t *testing.T) {
	u := prefixURLs()
	if h, p := u.Location("en"); h != "" || p != "" {
		t.Errorf("default = %q %q", h, p)
	}
	if h, p := u.Location("he"); h != "" || p != "/he" {
		t.Errorf("he = %q %q", h, p)
	}
	cases := map[[2]string]string{
		{"en", "/"}:      "https://x.test/",
		{"en", "/about"}: "https://x.test/about",
		{"he", "/"}:      "https://x.test/he",
		{"he", "/about"}: "https://x.test/he/about",
	}
	for in, want := range cases {
		if got := u.Abs(in[0], in[1]); got != want {
			t.Errorf("Abs(%v) = %q, want %q", in, got, want)
		}
	}
	if u.Path("he", "/") != "/he" || u.Path("he", "/a") != "/he/a" || u.Path("en", "/a") != "/a" {
		t.Error("Path wrong")
	}
}

func TestHostModeLocationAndAbs(t *testing.T) {
	u := hostURLs()
	if h, p := u.Location("he"); h != "he.x.test" || p != "" {
		t.Errorf("he = %q %q", h, p)
	}
	if got := u.Abs("he", "/"); got != "https://he.x.test/" {
		t.Errorf("Abs = %q", got)
	}
	if got := u.Abs("en", "/about"); got != "https://x.test/about" {
		t.Errorf("Abs = %q", got)
	}
	u.BaseURL = "http://x.test"
	if got := u.Abs("he", "/a"); got != "http://he.x.test/a" {
		t.Errorf("scheme not kept: %q", got)
	}
}

func TestResolvePrefix(t *testing.T) {
	u := prefixURLs()
	cases := []struct{ path, lang, neutral string }{
		{"/", "en", "/"}, {"/about", "en", "/about"}, {"/he", "he", "/"}, {"/he/about", "he", "/about"},
		{"/en/about", "en", "/en/about"}, // the default language has no prefix
		{"/hex", "en", "/hex"}, {"/he/post/4", "he", "/post/4"},
	}
	for _, c := range cases {
		l, n, ok := u.Resolve("anything", c.path)
		if !ok || l != c.lang || n != c.neutral {
			t.Errorf("Resolve(%q) = %q %q %v; want %q %q", c.path, l, n, ok, c.lang, c.neutral)
		}
	}
}

func TestResolveHost(t *testing.T) {
	u := hostURLs()
	if l, n, ok := u.Resolve("he.x.test", "/about"); !ok || l != "he" || n != "/about" {
		t.Errorf("he host: %q %q %v", l, n, ok)
	}
	if l, _, ok := u.Resolve("HE.X.TEST:8443", "/"); !ok || l != "he" {
		t.Errorf("case and port: %q %v", l, ok)
	}
	if _, _, ok := u.Resolve("evil.test", "/"); ok {
		t.Error("unknown host must not resolve")
	}
	if _, _, ok := u.Resolve("", "/"); ok {
		t.Error("empty host must not resolve")
	}
	// a path prefix means nothing in host mode
	if l, n, ok := u.Resolve("x.test", "/he/about"); !ok || l != "en" || n != "/he/about" {
		t.Errorf("prefix in host mode: %q %q %v", l, n, ok)
	}
}

func TestAlternates(t *testing.T) {
	a := prefixURLs().Alternates("/about")
	if a["en"] != "https://x.test/about" || a["he"] != "https://x.test/he/about" || len(a) != 2 {
		t.Errorf("%v", a)
	}
	if hostURLs().Alternates("/")["he"] != "https://he.x.test/" {
		t.Error("host alternates")
	}
	single := URLs{Default: "en", BaseURL: "https://x.test", Langs: []string{"en"}}
	if single.Alternates("/") != nil {
		t.Error("single language must have no alternates")
	}
	if (URLs{Default: "en", Langs: []string{"en", "he"}}).Alternates("/") != nil {
		t.Error("without BaseURL there are no absolute alternates")
	}
}
