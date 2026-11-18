package i18n

import (
	"net/url"
	"sort"
	"strings"
)

// URLs is the one place that knows how languages map to URLs. The server and the build both
// use it, so a page has the same address, canonical and alternates whichever of them made it.
// Paths are identical in every language (no localised slugs).
type URLs struct {
	Mode    string            // "prefix" (default), "subdomain" or "domain"
	Hosts   map[string]string // lang -> host, for subdomain/domain modes
	Default string            // default language
	BaseURL string            // https://example.com, no trailing slash; may be empty
	Langs   []string          // every language of the site, sorted
}

func (u URLs) hostMode() bool { return u.Mode == "subdomain" || u.Mode == "domain" }

// HostMode reports whether languages live on different hosts.
func (u URLs) HostMode() bool { return u.hostMode() }

// Location returns the cache/output host directory and the URL prefix of a language.
func (u URLs) Location(lang string) (host, prefix string) {
	if u.hostMode() {
		return u.Hosts[lang], ""
	}
	if lang == u.Default {
		return "", ""
	}
	return "", "/" + lang
}

// Path turns a language-neutral path ("/about") into the path requested in lang ("/he/about").
func (u URLs) Path(lang, neutral string) string {
	_, prefix := u.Location(lang)
	if neutral == "/" && prefix != "" {
		return prefix
	}
	return prefix + neutral
}

// Origin returns scheme://host of the site as seen in lang ("" when there is no BaseURL).
func (u URLs) Origin(lang string) string {
	if u.hostMode() {
		if h := u.Hosts[lang]; h != "" {
			scheme := "https"
			if strings.HasPrefix(u.BaseURL, "http://") {
				scheme = "http"
			}
			return scheme + "://" + h
		}
	}
	return u.BaseURL
}

// Abs returns the absolute URL of a language-neutral path in lang.
func (u URLs) Abs(lang, neutral string) string { return u.Origin(lang) + u.Path(lang, neutral) }

// Resolve works out which language and language-neutral path a request means.
// ok is false when the request cannot belong to this site (an unknown host).
func (u URLs) Resolve(host, urlPath string) (lang, neutral string, ok bool) {
	if u.hostMode() {
		host = strings.ToLower(host)
		bare := host
		if h, _, err := splitHostPort(host); err == nil {
			bare = h
		}
		for _, l := range u.Langs {
			if h := strings.ToLower(u.Hosts[l]); h != "" && (h == host || h == bare) {
				return l, urlPath, true
			}
		}
		return "", "", false
	}
	segs := strings.SplitN(strings.TrimPrefix(urlPath, "/"), "/", 2)
	if segs[0] != u.Default && u.has(segs[0]) {
		neutral = "/"
		if len(segs) == 2 {
			neutral += segs[1]
		}
		return segs[0], neutral, true
	}
	return u.Default, urlPath, true
}

func (u URLs) has(lang string) bool {
	i := sort.SearchStrings(u.Langs, lang)
	return i < len(u.Langs) && u.Langs[i] == lang
}

// Alternates returns lang -> absolute URL of neutral for every language, for hreflang tags.
// It is nil for single-language sites and when there is no BaseURL (hreflang must be absolute).
func (u URLs) Alternates(neutral string) map[string]string {
	if len(u.Langs) < 2 || u.BaseURL == "" && !u.hostMode() {
		return nil
	}
	m := make(map[string]string, len(u.Langs))
	for _, l := range u.Langs {
		m[l] = u.Abs(l, neutral)
	}
	return m
}

func splitHostPort(hostport string) (host, port string, err error) {
	u, err := url.Parse("//" + hostport)
	if err != nil {
		return "", "", err
	}
	return u.Hostname(), u.Port(), nil
}
