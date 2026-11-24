# Migrating from Astro to Hearth

A practical guide, with voters.co.il as the worked example (Hebrew, RTL, about 10,000 candidate pages from a
database, a few hundred static-ish pages, one Go backend). Nothing here was run against the real site; the Go
snippets are compiled and run by [`pkg/hearth/migrating_test.go`](../pkg/hearth/migrating_test.go), and
the claims about Hearth were checked against the code. Items that could not be checked are listed at the end.

The mental model: an Astro page is a component that runs at build time or on request. A Hearth page is a Go
`html/template` file plus, for pages with data, a Go handler that returns the data. Hearth renders the page
once, writes it to a file, and your web server serves the file from then on. There are no islands: scripts are
plain Vite entries that enhance a page that already works without them.

## 1. Pages and routes

| Astro | Hearth |
|---|---|
| `src/pages/about.astro` | `pages/about.html` -> `/about` |
| `src/pages/index.astro` | `pages/index.html` -> `/` |
| `src/pages/candidates/[id].astro` | `pages/candidates/[id].html` -> `/candidates/{id}`, data from `site.Handle("/candidates/{id}", h)` |
| a page that queries the database on every request (`export const prerender = false`) | a page listed in `"ssr"` in `hearth.config.json` plus `site.Handle("/candidates", h)` |
| `getStaticPaths()` over local data | a `collections` entry in `hearth.config.json` (prerendered for every row) |
| `getStaticPaths()` over a database | **do not** use a collection (see "Redirects" below): handler only |
| Hebrew copy in the page | `locales/he.json` and `{{t "key"}}`; per-language override file `about.he.html` |

For voters this gives: `pages/index.html`, `pages/candidates.html`, `pages/parties.html`, `pages/elections.html`,
`pages/coalition.html` (all in `"ssr"`, because their data comes from the database), and
`pages/candidates/[id].html` (a `[param]` page, server-rendered by definition). Fully static pages
(`about`, `privacy`) need no handler.

```json
{
  "baseURL": "https://voters.co.il",
  "defaultLang": "he",
  "ssr": ["/", "/candidates", "/parties", "/elections", "/coalition"],
  "funcs": ["num", "dec", "trend"]
}
```

Rules to remember: a handler on a parameterless page that is not in `ssr` is an error (the prebuilt file would
shadow it); an `ssr` page with no handler answers 404 (`site.Validate()` reports it at startup); `hearth check`
and `hearth dev` render `ssr` pages from `pages/candidates.mock.json`.

The handler is where the Astro frontmatter's data fetching goes. Validate every route parameter first:

```go
func (s store) loadCandidate(r *http.Request, p map[string]string) (any, hearth.Meta, error) {
	id := p["id"]
	if !slugRe.MatchString(id) || len(id) > 80 { // validate before anything else
		return nil, hearth.Meta{}, hearth.ErrNotFound
	}
	c, ok := s.byID[id]
	if !ok {
		return nil, hearth.Meta{}, hearth.ErrNotFound
	}
	return c, hearth.Meta{NoIndex: c.Votes == 0}, nil
}
```

Register with `site.MustHandle("/candidates/{id}", s.loadCandidate)` (pattern syntax is `{param}`; the file name
is `[param]`).

## 2. Layouts and slots

An Astro layout with `<slot />` and named slots becomes `layouts/base.html` with `block`s; a page fills them with
`define`. There is one layout for the whole site.

```html
<!-- layouts/base.html -->
<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}">
<head>
  <meta charset="utf-8">
  <title>{{block "title" .}}{{t "site.name"}}{{end}}</title>
  <meta name="description" content="{{block "description" .}}{{t "site.description"}}{{end}}">
  {{if .NoIndex}}<meta name="robots" content="noindex">{{end}}
  <link rel="canonical" href="{{.Canonical}}">
  {{entry "src/site.ts"}}
  {{block "head" .}}{{end}}
</head>
<body>{{block "body" .}}{{end}}</body>
</html>
```

```html
<!-- pages/candidates/[id].html -->
{{define "title"}}{{.Data.Name}}{{end}}
{{define "body"}}<main class="candidate"><h1>{{.Data.Name}}</h1></main>{{end}}
```

Astro props become `.Data` (what the handler returned). `Astro.params` becomes `.Params` (`{{index .Params "id"}}`).
Components become partials: `partials/header.html` with `{{define "header"}}...{{end}}` and `{{template "header" .}}`.
Use `{{.Link "/candidates"}}` for internal links so they stay in the current language.

## 3. `<style>` blocks (no scoped styles)

Astro scopes `<style>` to the component. Hearth has no scoping and no inline style blocks in pages. Move each
block (`404.astro`, `Legal.astro`, `admin-upload-file.astro`) into a CSS file, prefix the selectors with a class
on the page's root element, and import the file from the Vite entry:

```css
/* src/styles/legal.css */
.legal h1 { margin-block: 1rem; }
.legal ol { padding-inline-start: 1.5rem; }
```

```ts
// src/site.ts
import "./styles/site.css";
import "./styles/legal.css";
```

```html
{{define "body"}}<main class="legal">...</main>{{end}}
```

`{{entry "src/site.ts"}}` emits a `<link rel="stylesheet">` for every CSS file the entry imports (including CSS of
shared chunks), a `modulepreload` hint per imported chunk and the script, all with hashed URLs. For CSS that only
some pages need, make a second entry (`src/admin.ts`) and call `{{entry "src/admin.ts"}}` in that page's `head`
block next to the layout's call. A CSS file shared by two entries is emitted twice. Use `{{asset "favicon.svg"}}`
for single files.

Use logical properties (`margin-inline-start`, `padding-inline-end`) so one stylesheet works in both directions.

## 4. `define:vars` and embedded data

`<style define:vars={{ ... }}>` and `<script define:vars={{ ... }}>` inject server values into CSS and JS. Hearth's
replacement is data, not code: the template writes the data, the entry script reads it.

Three ways, all through the `json` helper (it escapes `<`, `>`, `&`, U+2028 and U+2029, so data cannot close a
tag):

```html
<!-- a JSON block read once -->
<script type="application/json" id="candidate-data">{{json .Data}}</script>

<!-- small values on the element that uses them -->
<main class="candidate" data-id="{{json .Data.ID}}" data-history="{{json .Data.History}}">
```

```ts
const data = JSON.parse(document.getElementById("candidate-data")!.textContent!);
const history: number[] = JSON.parse(document.querySelector<HTMLElement>("[data-history]")!.dataset.history!);
```

For a plain string or number in an attribute you do not need `json` at all: `data-id="{{.Data.ID}}"` is escaped
for the attribute context, and `el.dataset.id` is the string.

Checked by tests (`internal/render/jsonattr_test.go`): `json` output in `data-x="{{json .X}}"` and inside
`<script type="application/json">` round-trips quotes, `<`, `>`, `&`, U+2028/2029, nested quoted strings and
Hebrew text back to the original JSON. The attribute form comes out as `data-x="&#34;..."`; the browser
unescapes it, and `JSON.parse` works on the attribute value. **Finding:** `json` used to return `template.HTML`,
which `html/template` quotes a second time inside `<script type="application/json">` (the page got a JSON
string of a JSON string). It now returns `template.JS`, which is inserted verbatim in a script and
attribute-escaped in an attribute. No separate `jsonAttr` helper is needed.

CSS variables from data (`define:vars` on `<style>`): put the value in a data attribute and set the variable from
the entry script (`el.style.setProperty("--score", el.dataset.score!)`), or write it in `style="--score: {{.Data.Score}}"`
only for numbers (`html/template` filters unsafe values in a `style` attribute to `ZgotmplZ`, which fails
safe).

## 4b. `import.meta.env.PUBLIC_*` and other build-time values

Astro exposes `PUBLIC_*` variables to client code; Vite exposes only `VITE_*` unless told otherwise, so a ported
`import.meta.env.PUBLIC_API_URL` is silently `undefined`. Fix: in the site's `vite.config.ts`

```ts
export default defineConfig({
  envPrefix: ["VITE_", "PUBLIC_"], // Hearth's starter already has this line
  // ...
});
```

- Set the values in `.env` / `.env.production` or the build environment; they are read when `hearth build` runs
  Vite and baked into the bundle.
- **Secrets rule:** everything under a prefix is public JavaScript. Keep tokens, database URLs and keys in the Go
  server's environment, never under `PUBLIC_` or `VITE_`.
- `hearth check` warns (never fails) when `src/**` uses `import.meta.env.NAME` and `vite.config.*` has no
  `envPrefix` covering it.
- A value that must change **without a rebuild** (an API origin per environment, a feature flag) should not be
  baked in. Put it in page data in the handler and hand it to the script with `json`, as in section 4:
  `<script type="application/json" id="config">{{json .Data.Config}}</script>`. Changing it needs
  `site.Invalidate(...)`, not a JavaScript build.

## 5. The `/?t=` redirect script

voters has an inline script that reads `?t=` and sends old links to the new page. It becomes an entry script. The
template provides the map as data, the script contains no data and no inline code:

```html
<!-- pages/index.html, in the body -->
<main data-legacy-redirect="{{json .Data.Legacy}}">...</main>
```

```ts
// src/legacy-redirect.ts (import it from src/site.ts)
const root = document.querySelector<HTMLElement>("[data-legacy-redirect]");
if (root) {
  const targets: Record<string, string> = JSON.parse(root.dataset.legacyRedirect ?? "{}");
  const t = new URLSearchParams(location.search).get("t");
  const to = t !== null && Object.hasOwn(targets, t) ? targets[t] : "";
  // only same-site paths: "/x" yes, "//evil.example" and "https://..." no
  if (/^\/(?!\/)/.test(to)) location.replace(to);
}
```

This exact script was run under Node with a fake `document` and `location` (redirects for a known key, ignores
unknown keys, `constructor`, and `//host` targets). The query string must never change the HTML (Hearth caches by
path), so the redirect has to happen in the browser; if the old URLs are a fixed set, a server-side 301 from the
handler or Caddy is better for SEO.

## 6. Numbers and sorting: `Options.Funcs`

`toLocaleString("he-IL")`, `toFixed(2)` and `localeCompare` have no template equivalent. Hearth ships no
formatting helpers (no `x/text` in the library); you add functions in your own Go program:

```go
func group(digits string) string {
	for i := len(digits) - 3; i > 0; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	return digits
}

func num(n int) string { // {{num .Votes}} -> 1,234,567
	if n < 0 {
		return "-" + group(strconv.Itoa(-n))
	}
	return group(strconv.Itoa(n))
}

func dec(f float64, places int) string { // {{dec .Score 1}} -> 4.6, {{dec .Score 2}} -> 4.57
	s := strconv.FormatFloat(f, 'f', places, 64)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, hasFrac := strings.Cut(s, ".")
	out := group(whole)
	if hasFrac {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

site, err := hearth.New(hearth.Options{
	// ...
	Funcs: template.FuncMap{"num": num, "dec": dec, "trend": trend},
})
```

he-IL groups thousands with a comma and uses a dot for decimals, the same as en-US, so no locale data is needed.
Another locale means another separator, still a few lines of Go.

Rules (enforced by `New`): a name may not shadow a helper (`t`, `plural`, `asset`, `entry`, `picture`, `pictureSet`, `json`,
`data`) or a Go template builtin; each value must be a func with one result, or a result and an `error`. An
error fails the render (no partial page is cached). Functions must be pure and safe for concurrent use: no clock,
no request, no shared mutable state, because the output is cached for everyone.

**Sorting belongs in the handler**, not the template. Plain `sort.Slice` on strings is byte order, which puts
Hebrew letters in the right order but ignores Latin mixed with Hebrew, final letters and niqqud. For real Hebrew
collation use `golang.org/x/text/collate` in the **site's own module** (never in Hearth):

```go
import (
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func sortByName(list []candidate) {
	cl := collate.New(language.Hebrew) // a Collator is not safe for concurrent use: make one per call
	sort.Slice(list, func(i, j int) bool { return cl.CompareString(list[i].Name, list[j].Name) < 0 })
}
```

This snippet was compiled and run in a scratch module with `golang.org/x/text` v0.44.0 (it is not part of
`migrating_test.go` because Hearth's module cannot depend on `x/text`). It sorted Hebrew names alphabetically with
a Latin name first.

## 7. `renderTrend`: an SVG func

A func may return `template.HTML`, which is not escaped, so the rule is: build it from numbers, with fixed
attributes, never from a string a user wrote.

```go
func trend(points []float64) template.HTML { // {{trend .Data.History}}
	if len(points) < 2 {
		return ""
	}
	lo, hi := points[0], points[0]
	for _, p := range points {
		lo, hi = min(lo, p), max(hi, p)
	}
	span := hi - lo
	if span == 0 {
		span = 1
	}
	const w, h = 120.0, 32.0
	var b strings.Builder
	b.WriteString(`<svg class="trend" viewBox="0 0 120 32" width="120" height="32" role="img" aria-hidden="true"><polyline fill="none" stroke="currentColor" stroke-width="2" points="`)
	for i, p := range points {
		x := w * float64(i) / float64(len(points)-1)
		y := h - (p-lo)/span*h
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strconv.FormatFloat(x, 'f', 1, 64))
		b.WriteByte(',')
		b.WriteString(strconv.FormatFloat(y, 'f', 1, 64))
	}
	b.WriteString(`"/></svg>`)
	return template.HTML(b.String())
}
```

Every byte that comes from data goes through `strconv.FormatFloat`; labels or titles from the database do not
belong in this function (put them in the template, where `html/template` escapes them). A test pins the exact
`points` output.

## 8. The CLI versus your Go program

`hearth build`, `check` and `dev` do not run your Go code, and `html/template` cannot parse an unknown function.
So:

- List the function names in `hearth.config.json` (`"funcs": ["num", "dec", "trend"]`). The CLI registers a stub
  for each: templates parse, `hearth check` reports which pages call a func and need your program, and a stub
  that is actually called returns an error.
- The CLI's own `hearth build` therefore fails on a **static** page that calls a func (it prerenders that page).
  Pages that use funcs should be in `"ssr"` (the CLI does not render them; your program does), or be built only by
  your program. In the voters design all pages that use `num`/`dec`/`trend` are database pages, so they are `ssr`
  or `[param]` pages anyway.
- Flow: `hearth build` runs Vite and writes `dist/templates` (the bundle your Go program embeds or reads) and
  `dist/public` (assets, `public/`, static pages). Your program then renders what only it can, with the real funcs:

```go
// web build
site, err := hearth.New(hearth.Options{Bundle: os.DirFS("dist/templates"), CacheDir: cacheDir, BaseURL: baseURL,
	Funcs: funcs /* no SitemapURLs here, see below */})
// ... site.MustHandle(...) for every route, exactly as the server does
rep, err := hearth.Build(ctx, site, "dist/public")
```

  `hearth.Build` is the same routine the CLI calls (prerender, export, `404.html`, `sitemap.xml`, `robots.txt`). It
  skips `ssr` pages. It also prerenders every path `Options.SitemapURLs` returns, so give the build's `Site` no
  `SitemapURLs`; the running server (which has it) answers `/sitemap.xml` itself. Otherwise 10,000 database pages
  become static files that shadow your handlers and never refresh.
- Development: `hearth dev` renders from `*.mock.json` and fails loudly on a func stub. For real data use
  `hearth dev -live`, which proxies to your Go server (started with `Options.Dev`); a func that fails there fails
  the page, as in production.
- `hearth preview` runs the production flow locally: a file if it exists, otherwise your Go server.
- `hearth dev` renders from mock data, so a client that calls your backend needs `dev.proxy`
  (`{"dev":{"proxy":{"/api":"http://localhost:8080"}}}`): requests under `/api` go to that URL first, before page
  routes, with method, path, query, body and headers intact (longest prefix wins, whole path segments only, dev
  only; `hearth check` validates it). With `vite.devURL` and a separate `vite` process, Vite's `server.proxy`
  works too.
- After a deploy empties the cache, refill it with `go site.WarmSitemap(ctx, 4)`; for a targeted change use
  `site.Invalidate(...)`. A large warm costs real time and database load (see the README).

## 9. 404 and 410

Astro's `404.astro` becomes `pages/404.html` (and `410.html`). They are rendered inside your layout with `.Status`
set to 404 or 410, are never cached, are not routes and not in the sitemap. `404.he.html` overrides per language.
Without the files the body is plain text. `hearth build` writes `dist/public/404.html` so Caddy or nginx can serve
it for missing files (see [DEPLOY.md](DEPLOY.md)). Handlers choose the response: `hearth.ErrNotFound` -> 404,
`hearth.ErrGone` -> 410.

Move the `<style>` of `404.astro` to a CSS file as in section 3.

## 10. noindex

Return `hearth.Meta{NoIndex: true}` from the handler. Hearth sends `X-Robots-Tag: noindex` and sets `.NoIndex` in
the page data; the layout turns that into `<meta name="robots" content="noindex">`, which also survives when the
web server serves the cached file without Hearth. A static page declares it in its own template:
`{{define "robots"}}noindex{{end}}`. 404 and 410 pages are always noindex.

## 11. Sitemap with `lastmod`

```go
SitemapURLs: func(ctx context.Context) ([]hearth.SitemapEntry, error) {
	// the same query that lists candidates; UpdatedAt is the row's updated_at
	var out []hearth.SitemapEntry
	for _, c := range candidates {
		out = append(out, hearth.SitemapEntry{Path: "/candidates/" + c.ID, LastMod: c.UpdatedAt})
	}
	return out, nil
},
```

`<lastmod>` is written only when `LastMod` is set; Hearth never invents a date (static pages get none). Above
50,000 URLs or 50 MB `/sitemap.xml` becomes an index of `/sitemap-1.xml`, ... Compare the old Astro sitemap's URL
set with the new one before you switch.

## 12. Redirects from handlers

voters keeps renamed candidates in `url_redirects`. The `/candidates/{id}` handler looks the id up and answers
`hearth.Redirect(301, to)`:

```go
if to, ok := s.renamed[id]; ok && slugRe.MatchString(to) {
	return nil, hearth.Meta{}, hearth.Redirect(301, "/candidates/"+to) // a path we build, never a stored URL
}
```

Hearth sends the target as given, so validate it: build the path yourself from a validated slug and never pass a
URL string from the database straight through (`//attacker.example` is a valid-looking "path"). The test
checks that a stored `//attacker.example` target gets 404, not a redirect.

**Never prerender a database-backed route.** Do not put `/candidates/{id}` in `collections`, and do not return
those paths from `SitemapURLs` into a `hearth.Build` call: the prebuilt file is served by Caddy before Hearth
is asked, so it shadows the redirect and the 404/410. Handler-rendered pages are cached under `cache/`, which
`Invalidate` and the next deploy refresh; files in `dist/public` are never refreshed by the Go server. (Caddy checks `dist/public`
first, then the cache; see [DEPLOY.md](DEPLOY.md).)

## 13. `search.json`

voters has a `search.json` endpoint. It is not a page; serve it from your own mux in front of the Site (the
pattern in the test), or as `Options.Fallback`, which only sees paths that match no page:

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /search.json", searchJSON) // your handler: Content-Type, Cache-Control, encoding/json
mux.Handle("/", site)                          // everything else is Hearth's
http.ListenAndServe(":8080", mux)
```

If Caddy serves static files first, add a `handle /search.json` that goes to the Go server (it is not a file).
Set `Cache-Control` yourself; Hearth does not touch responses it does not render.

## 14. Hebrew and RTL

- `dir` comes from the language: Hearth sets `.Dir` to `rtl` for Hebrew (and Arabic, and so on) and `ltr`
  otherwise. Write `<html lang="{{.Lang}}" dir="{{.Dir}}">` and nothing else per language. With
  `"defaultLang": "he"` the root URLs are Hebrew; other languages get a prefix (`/en/...`).
- Use CSS logical properties; do not hardcode `left` and `right`.
- Mixed Hebrew and Latin or numbers in one line: wrap the Latin fragment in `<bdi>` or `<span dir="ltr">`.
  Numbers formatted by `num` are plain digits with commas, which render correctly inside RTL text.
- A Hebrew route parameter (a `{letter}` page) is allowed by the cache. The only open question is the
  web server: Caddy must match the percent-decoded request path to a UTF-8 file name. This is checked by
  `deploy/verify.sh` but has never been run against a real Caddy here.
- Hebrew in JSON data: all of the above is UTF-8 end to end; the `json` helper keeps Hebrew letters as they are.

## 15. Images

- **`public/` is copied as is** to the site root: no hashing, no processing. The 59 MB of candidate photos that
  `photos.ts` loads at runtime stay in `public/photos/...` and are served untouched at `/photos/...`. They are not
  in the manifest, so templates and scripts reference them by plain path.
- **`{{picture "name" "alt"}}` is for `src/images/**`** (brand logos, illustrations): the `@lok-tech/hearth/vite` plugin turns
  each `.jpg`/`.png` into AVIF, WebP and the original, content-hashed, with `width` and `height` set.
- **Responsive widths.** `hearthImages({ widths: [480, 960, 1600] })` writes every format at every width
  (default `[maxWidth]`, one size, no change for existing sites). Widths are positive integers, at most 8, none above
  `maxWidth`; a source narrower than a width is never enlarged. `{{picture "hero" "Alt" "(min-width: 60rem) 50vw, 100vw"}}`
  then emits `srcset` with `w` descriptors per format plus `sizes`; without a `sizes` argument it emits `sizes="100vw"`.
  Logos usually do not need widths. A trailing `"eager"` (`{{picture "hero" "Alt" "eager"}}`) drops `loading="lazy"`
  and `decoding="async"` for the image above the fold.
- **Art direction (a logo that changes in dark mode, a different hero on mobile) is `pictureSet`**, for images
  in `src/images/` (JPEG and PNG; the plugin does not process SVG). The last pair is the fallback `<img>`:

```html
  {{pictureSet "Example" "" "(prefers-color-scheme: dark)" "brand/logo-dark" "" "brand/logo-light"}}
```

  This emits, per format, `<source media="(prefers-color-scheme: dark)" type=... srcset=...>` for the dark logo, then
  the light logo's own format sources, then the `<img>` with `alt`, `width`, `height` (no layout shift). The first
  `<source>` whose `media` matches wins, so list the most specific pair first. For a viewport change use
  `"(max-width: 600px)"` with a cropped image, and add a `sizes` string. Every name must exist; `media` must not
  contain a double quote. `prefers-color-scheme` follows the operating system; if your site has its own theme
  switch, use two images toggled by CSS instead.
- **Files in `public/` are not processed, so use hand-written markup for them** (for example SVG logos):

```html
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="/brand/logo-dark.svg">
    <img src="/brand/logo-light.svg" alt="Example" width="160" height="40">
  </picture>
  ```

  They are copied as is and not hashed: no automatic cache-busting, so rename a file (`logo-light-v2.svg`) when its
  content changes.
- For the largest above-the-fold image use the `"eager"` argument, or write the `<img>` yourself with
  `fetchpriority="high"` and no lazy loading.

## 15b. Trailing slashes and cutover

Hearth and the shipped Caddy and nginx configs answer `/x/` with a 301 to `/x` (`/` excepted; `/he/` goes to
`/he`). If the Astro site served both forms, or was indexed with trailing slashes, check before cutover:

1. Export the indexed URLs from the search console and count those ending in `/`.
2. Confirm each one answers 301 to the non-slash form and the target answers 200.
3. Confirm internal links, the sitemap and the canonical tag use the non-slash form.
4. Run `VERIFY_SLASH="/about /candidate/1" deploy/verify.sh https://staging.example` (a static page and a
   parameter route).

The query string is kept by nginx and dropped by the Go server and the Caddy rule; see
[DEPLOY.md](DEPLOY.md#trailing-slashes-and-the-cutover-checklist). A route that shares a path with a
`public/` directory (`/photos` the page, `public/photos/` the images) needs `VERIFY_DIR_COLLISION=/photos`;
see "A route and a `public/` directory on the same path" in DEPLOY.md.

## 16. Parity check

Do this on a sample (50 candidates, every static page, a 404), before any traffic moves.

1. Keep the old site's built HTML (`dist/` of the Astro build) and Hearth's output, either `dist/public` or fetch
   pages from the running Go server.
2. For each URL extract the same things from both and diff them:
   - `<title>`, `meta[name=description]`, `link[rel=canonical]`, `link[rel=alternate]`, `meta[name=robots]`
   - the JSON-LD blocks (parse the JSON, compare as data, not as text)
   - the headings (`h1` to `h3`) as an ordered list
   - the visible text: strip `script` and `style`, collapse whitespace, then diff line by line
3. Differences you expect and accept: asset URLs, attribute order, whitespace, Astro's `data-astro-*` attributes,
   `<link>` hints. Differences you fix: a missing `<h1>`, a changed canonical, text formatted differently
   (`1,234` against `1234`, `4.6` against `4.57`), a missing `noindex`.
4. Compare the URL sets: the old `sitemap.xml` against the new one, and every old URL that is not in the new set
   must answer 301 or 410, not 404.

A starting point (needs `pip install beautifulsoup4`; it is a sketch, it has not been run against voters):

```python
import sys, re, json
from bs4 import BeautifulSoup

def facts(path):
    s = BeautifulSoup(open(path, encoding="utf-8"), "html.parser")
    def one(sel, attr):
        el = s.select_one(sel)
        return el.get(attr) if el else None
    for t in s(["script", "style"]):
        if t.get("type") != "application/ld+json":
            t.decompose()
    ld = [json.loads(t.string) for t in s.select('script[type="application/ld+json"]')]
    for t in s.select('script[type="application/ld+json"]'):
        t.decompose()
    return {
        "title": s.title.string if s.title else None,
        "description": one('meta[name=description]', "content"),
        "canonical": one('link[rel=canonical]', "href"),
        "robots": one('meta[name=robots]', "content"),
        "headings": [(h.name, h.get_text(" ", strip=True)) for h in s.select("h1,h2,h3")],
        "jsonld": ld,
        "text": re.sub(r"\s+", " ", s.get_text(" ", strip=True)),
    }

a, b = facts(sys.argv[1]), facts(sys.argv[2])
for k in a:
    if a[k] != b[k]:
        print("DIFF", k, "\n  old:", a[k], "\n  new:", b[k])
```

## What was and was not verified

Verified by tests in this repository: every Go snippet above (`pkg/hearth/migrating_test.go`: formatters, trend
output, handler outcomes 200/301/404/410, noindex meta and header, `{{entry}}`, the data block and attributes,
sitemap `lastmod`, `search.json` through a mux and through `Options.Fallback`, `hearth.Build` writing `404.html`
and no file for `ssr` pages or database rows); `json` in attributes and scripts (`internal/render/jsonattr_test.go`);
the `/?t=` script under Node; `hearth check`'s `envPrefix` warning, `dev.proxy` (in-process, in-memory backends) and `Site.WarmSitemap` (`internal/build/check_test.go`, `internal/dev/proxy_test.go`, `pkg/hearth/warm_test.go`).

Not verified: any of this against the real voters data or templates; the Astro features of voters that were
not given here (its exact `/?t=` map, `renderTrend` output, JSON-LD) are assumed to be as described in the plan;
the TypeScript was type-stripped and run, not type-checked with `tsc` and not run in a browser; the Python parity
sketch was not run; Hebrew paths through a real Caddy; a Vite build of the voters entries; a real Vite build
exposing a `PUBLIC_` variable; `dev.proxy` over real TCP and WebSockets.
