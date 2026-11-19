# Hearth

Build and maintain websites with a focus on SEO and performance. Static pages and server-rendered
pages share **one template location and one render logic**. Server-rendered output is baked into files,
and a web server serves those files at static speed.

- **One template system.** Go `html/template`, one shared layout, file path = route.
- **Two ways in, one engine.** The `hearth` CLI builds static sites; the Go package `pkg/hearth` renders
  pages in your own server. `hearth build` is that same engine run ahead of time, so a prebuilt page and a
  page your server renders on a cache miss are byte-identical (a test enforces it).
- **SEO by default.** Canonical and hreflang tags, one URL per page, sitemap and robots, 301/410/404 done
  properly, hashed immutable assets, images in AVIF + WebP + fallback with width and height.

## Quick start

```sh
npm i -D @lok-tech/hearth vite sharp      # the CLI is a Go binary in a per-platform package
npx hearth init mysite && cd mysite && npm install
npm run check                   # validate routes, data, locales; trial-render every page
npm run build                   # -> dist/public (deploy anywhere), dist/templates (for a Go server)
```

The npm packages are not published yet. From a checkout of this repository:
`go run ./cmd/hearth init ../mysite`, then `go run ./cmd/hearth build -site ../mysite`.

## How it works

```
 BUILD TIME                                    RUN TIME

 site/  pages layouts partials locales data    browser
        src (ts, css, images)                     |
          |                                       v
     hearth build                          web server (Caddy, nginx) or CDN
       1. vite build  -> hashed assets         |
       2. hearth.Site -> Prerender             |-- 1. file in dist/public?          serve it
       3. Export      -> files                 |-- 2. file in cache/current?        serve it
          |                                    |
          +--> dist/public     (static)        '-- 3. miss
          '--> dist/templates  (bundle) --.            |
                                          |            v
                                          |    Go server  (hearth.Site)
                                          |      one render at a time per URL, bounded concurrency
                                          |      your handler  -> your database
                                          '--->  same templates -> render -> write the file
                                                   (200 only, atomically, into cache/versions/<n>)
                                                     |
                      next request for that URL: step 2, no Go server involved
```

- **Routes.** `pages/about.html` is `/about`; `pages/post/[slug].html` is `post/{slug}`; `about.he.html`
  overrides one language. A `[param]` page with a collection bound in `hearth.config.json` is prerendered for
  every entry; without one it is rendered by your server.
- **Cache.** `cache/versions/<bundle>.<n>/<path>/index.html`, with an atomic `current` symlink. A new bundle
  starts an empty cache, so a redeploy never serves pages from the old templates. `SetVersion` flips it when
  your data changes; pages refill lazily.
- **No database driver in the library.** Your server decides what changed and tells Hearth
  (`SetVersion`, or `Subscribe` with a `Source`; `hearth.Poll` is a stdlib-only one).

## A site

```
site/
  pages/      index.html  about.html  about.he.html  post/[slug].html  post/[slug].mock.json
  layouts/    base.html          blocks: title, description, head, body
  partials/   header.html        {{define "header"}} ... {{end}}
  locales/    en.json  he.json   {{t "key"}}; plurals use CLDR suffixes (items.one, items.other)
  data/       posts.json         local JSON, {{data "posts"}}
  src/        entry.ts  styles/  images/
  public/                        copied to the site root as-is
  hearth.config.json
```

Template helpers: `t`, `plural`, `asset`, `entry`, `picture`, `pictureSet`, `json` (safe for `<script type="application/json">` and for an attribute such as `data-x="{{json .X}}"`),
`data`, and `.Link "/about"` for links that stay in the current language. A missing translation key
fails the build.

`{{entry "src/site.ts"}}` in the layout's `<head>` loads a Vite entry: a stylesheet link for every CSS file
(including CSS of shared chunks), a `modulepreload` hint per imported chunk and the module script, all with
hashed URLs (and `assetBase`). Under a Vite dev server it emits the Vite client and the script. Use `{{asset}}`
for single files such as a favicon.

`hearth.config.json` covers `baseURL`, `defaultLang`, `i18n` (path prefix, subdomain or domain),
`assetBase` (S3/CDN), `vite`, `collections`, `ssr`, `funcs` and `dev` (`addr`, `server`, `proxy`). Paths are
identical in every language.

Two settings are for sites that serve data from Go:

- `"ssr": ["/candidates", "/"]` marks pages without a `[param]` as server-rendered. `hearth build` does not
  write a file for them (it would shadow your handler); `hearth check` and `hearth dev` render them from
  `pages/candidates.mock.json`; they stay in the sitemap. An entry that matches no page fails `check`.
- `"funcs": ["num", "trend"]` names the template functions your Go program adds with `Options.Funcs`. The CLI
  cannot run them, so it registers stubs: templates still parse and `hearth check` lists the pages that need
  your program, but `hearth build` fails on a page that calls one. Build those sites with `hearth.Build`
  (below).

## Frontend rules

- Shared CSS is one separate hashed file (every `{{entry}}` links it); never inline shared code into a page that gets cached.
- Page-specific JS or CSS is its own entry in `vite.config.ts`, loaded only on that page with `{{entry "src/page.ts"}}` in the `head` block.
- Tiny page-unique data goes in the HTML as JSON (`<script type="application/json">{{json .X}}</script>`), read by an entry script. No inline code.
- Fonts: see the note at the top of the starter's `src/styles/main.css`.

## Images

- `public/` is copied to the site root as is: no hashing, no processing. Files that scripts load at runtime
  (candidate photos, say) pass through untouched and are referenced by plain path.
- `{{picture "name" "alt"}}` is for `src/images/**` (logos, illustrations): AVIF + WebP + the original, hashed,
  with `width` and `height`. It takes optional arguments: `{{picture "hero" "Alt" "(min-width: 60rem) 50vw, 100vw"}}`
  adds a `sizes` string (never empty), and a trailing literal `"eager"` turns off `loading="lazy"` and
  `decoding="async"` for the image above the fold (`{{picture "hero" "Alt" "eager"}}`, or with `sizes` before it).
- **Responsive widths.** Set `widths` on the plugin: `hearthImages({ widths: [480, 960, 1600] })` (default
  `[maxWidth]`, one size, so existing output does not change). Every format is written at every width, content-hashed.
  `widths` must be positive integers, at most 8, none above `maxWidth` (an error, not a clamp); duplicates are
  removed and the list is sorted. A source narrower than a width is never enlarged: it gets only its own size.
  `picture` then emits `srcset="/a-480.avif 480w, /a-960.avif 960w"` per format, and the `<img>` fallback has the
  largest original-format rendition as `src` (with its `width`/`height`) plus the same `srcset`. With several widths
  and no `sizes` argument it emits `sizes="100vw"`, the browser's own default for a full-width image; pass `sizes`
  whenever the image is narrower than the viewport, or the browser downloads more than it needs. A single-width
  `srcset` is a bare URL and `sizes` is not emitted for it. `images.json` keeps its shape (one entry per file),
  so older bundles render the same bytes.
- **Art direction: `pictureSet`.** `{{pictureSet "Alt" "sizes" "(media)" "name" ... "" "fallback" ["eager"]}}` takes
  the alt text, a `sizes` string (may be `""`), then `media`, `name` pairs; the last pair has an empty media and is
  the fallback `<img>`. Each earlier pair becomes one `<source media type srcset>` per format (widths work inside
  each), in the order written, so put the most specific media first. The fallback's own AVIF/WebP sources come
  after them. Dark and light logos from `src/images/`:

```html
  {{pictureSet "Example" "" "(prefers-color-scheme: dark)" "brand/logo-dark" "" "brand/logo-light"}}
  ```

  Every name must exist (an error; `hearth check` uses placeholders). `media` is escaped and must not contain a
  double quote or control characters. Any wrong argument count is an error. `prefers-color-scheme` follows the
  operating system; if your site has its own theme switch, use two images toggled by CSS instead.
- **Files in `public/` are not processed.** To use one, write the `<picture>` yourself; the files are copied as is,
  **not hashed**, so give a changed image a new name (`logo-light-v2.svg`) because CDNs and browsers may keep the old bytes:

```html
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="/brand/logo-dark.svg">
    <img src="/brand/logo-light.svg" alt="Example" width="160" height="40">
  </picture>
  ```

Coming from Astro? See **[docs/MIGRATING-FROM-ASTRO.md](docs/MIGRATING-FROM-ASTRO.md)** (voters.co.il as the worked example).

## Environment variables and build-time values

Vite bakes `import.meta.env.<NAME>` into the client bundle **at build time**, and by default only for names that
start with `VITE_`. The starter sets `envPrefix: ['VITE_', 'PUBLIC_']` in `vite.config.ts`, so code written for
`PUBLIC_API_URL` (Astro-style naming) works. Rules:

- **Everything under a prefix is public.** It ends up in JavaScript anyone can read. Never put a secret
  (a token, a database URL, a private key) under `VITE_` or `PUBLIC_`; secrets stay in your Go server's
  environment.
- The value is fixed when `hearth build` runs Vite. Changing it means a rebuild and a redeploy of the assets.
- `hearth check` warns (it never fails) when `src/**` reads `import.meta.env.NAME` and the site's
  `vite.config.*` has no `envPrefix` covering `NAME`: without it the value is `undefined` in the browser. It is
  a text scan, so a prefix computed in code is not understood and silences the warning. `MODE`, `DEV`,
  `PROD`, `BASE_URL` and `SSR` are Vite's own and never warn.
- **A value that must change without a rebuild does not belong in the bundle.** Let the handler put it in the
  page data and let the template hand it to the script:

  ```html
  <script type="application/json" id="config">{{json .Data.Config}}</script>
  ```

  ```ts
  const config = JSON.parse(document.getElementById("config")!.textContent!);
  ```

  Changing the value then needs `site.Invalidate(...)` (or a version flip), not a JavaScript rebuild.

## Serve it from your own Go server

Requires Go 1.27 or newer.

```go
import "github.com/lok-tech/hearth/pkg/hearth"

site, err := hearth.New(hearth.Options{
	Bundle:   os.DirFS("dist/templates"), // or an embed.FS
	CacheDir: "/var/cache/hearth",
	BaseURL:  "https://example.com",
})
site.MustHandle("post/{slug}", loadPost) // a typo in the pattern fails at startup
site.MustHandle("/candidates", loadList) // a page without a parameter must be in "ssr" in the config
if err := site.Validate(); err != nil { log.Print(err) } // ssr pages with no handler answer 404
go site.Subscribe(ctx, hearth.Poll(currentVersion, 5*time.Second))
http.ListenAndServe(":8080", site)
```

A handler returns `(data, meta, err)`; the error decides the response:

| Return | Response | Cached |
|---|---|---|
| data | 200 | yes, as a file |
| `hearth.ErrNotFound` | 404 | no |
| `hearth.ErrGone` | 410 | no |
| `hearth.Redirect(301, to)` | 301 | no |
| any other error | 500 | no |

A 404 or 410 renders `pages/404.html` / `pages/410.html` inside your layout when they exist (`.Status` is 404 or
410; `404.he.html` overrides per language), `noindex` and never cached; otherwise the body is plain text. The
same goes for a path that matches no page in a known language (`Options.Fallback` still wins there). They are
templates, not routes: no sitemap entry, no prerender. `hearth build` writes the rendered 404 as
`dist/public/404.html` (plus `<lang>/404.html` in prefix mode, `<host>/404.html` in host modes) for the web server.

`Meta.NoIndex` sets the `X-Robots-Tag` header and `PageData.NoIndex`, which the layout turns into
`<meta name="robots" content="noindex">` (the tag survives when the web server serves the cached file without
Hearth). A static page declares the same with `{{define "robots"}}noindex{{end}}`.

`Options.Funcs` adds template functions (`template.FuncMap`) next to the helpers; `New` rejects a name that
shadows a helper or Go builtin and any value that is not a func with one result (or a result and an error).
A returned error fails the render, so no partial page is cached. A binary that registers funcs builds its
static pages with the same engine as the CLI: `hearth.Build(ctx, site, "dist/public")` (prerender, export,
sitemap and robots). Pages in `ssr` are skipped; after data changes call `site.Invalidate("/candidates")`, which
drops the page in every language.

`Options.SitemapURLs` lists pages that only the server knows, each with an optional `LastMod` (a row's
`updated_at`); `<lastmod>` is written only when you set it. Above 50,000 URLs or 50 MB `/sitemap.xml` becomes a
sitemap index of `/sitemap-1.xml`, ... and `hearth.Build` writes the same files. `SitemapEntries` (paths only) is
deprecated.

**After a full flip, warm the cache.** `site.Invalidate("/post/42", "/candidates")` drops only the pages you
name (all languages); use it for targeted changes. `site.SetVersion(v)` empties the whole cache, so the first
crawler burst would re-render everything. Refill it from the sitemap in the background:

```go
site.SetVersion("v43")
go func() {
	rep, err := site.WarmSitemap(ctx, 4) // 4 pages at a time, on top of MaxConcurrent
	log.Printf("warm: %d ok, %d failed, err %v", rep.OK, len(rep.Failures), err)
}()
```

`WarmSitemap` renders every page the sitemap lists (static pages, `ssr` pages, `SitemapURLs`) in every language
through the normal request path, so `MaxConcurrent`, `RenderTimeout`, singleflight and "only 200s are cached"
all apply. `ssr` and static pages come first. A failing page is listed in `rep.Failures`, not fatal; cancelling
`ctx` stops it. Each page is written into the cache version that was active when its render began, so a flip
during the warm cannot put old pages into the new version. A large site costs real time and database load
(one handler call per page per language); the site already serves, and caches on demand, while it runs. Lower
the concurrency to protect the database. `Site.Warm(ctx, urls, n)` warms literal URLs instead.

A complete Postgres-backed server with HTTP-level tests is in [`example/`](example/).

## CLI

| Command | What |
|---|---|
| `hearth init [-name N] [DIR]` | write the Vite starter |
| `hearth check` | validate and trial-render every page in every language, listing all problems |
| `hearth build` | Vite, prerender, `dist/public` + `dist/templates` + `dist/manifest.json` |
| `hearth dev` | pages rendered from source with `*.mock.json` data; `dev.proxy` forwards backend paths; `-live` proxies your Go server |
| `hearth preview` | the production flow locally: file if it exists, otherwise your Go server |

### Calling a backend from `hearth dev`

`hearth dev` renders pages from mock data, so calls to your real backend (`/api`, uploads, short links) have
nowhere to go. List them in `hearth.config.json`:

```json
{ "dev": { "proxy": { "/api": "http://localhost:8080", "/uploads": "http://localhost:8080/files" } } }
```

- Works in both modes (the default watch mode, and `vite.devURL`). It is checked first: before page routes, the
  Vite fallback and live reload. The longest prefix wins, and a prefix matches whole path segments (`/api`
  takes `/api` and `/api/x`, not `/apiary`).
- Method, path, query, body and headers pass through (including cookies and `Authorization`, and upgrade
  requests such as WebSockets). The backend receives its own `Host`; `X-Forwarded-For`, `-Host` and `-Proto`
  describe the original request. Redirects and `Set-Cookie` from the backend are not rewritten.
- A prefix starts with `/`, has no trailing slash, is not `/` (that would swallow the site) and is not
  `/healthz`, `/robots.txt` or `/sitemap.xml`; a target is an absolute `http` or `https` URL. `hearth check`
  validates all of this; a backend that is down answers 502 with its address.
- Development only: `hearth preview`, `hearth dev -live` and production ignore it (routing there is your web
  server's or Go server's job; see [docs/DEPLOY.md](docs/DEPLOY.md)).
- Alternative: with `vite.devURL` set and `vite` running in a second terminal, unmatched paths already go to the
  Vite dev server, so Vite's own `server.proxy` does the same job:

  ```ts
  server: { proxy: { "/api": "http://localhost:8080" } }
  ```

  Use one or the other for a given prefix; `dev.proxy` runs first.

## Deploying

Caddy and nginx configs, S3/CDN for assets, compose, release order and rollback, and a script that checks a
server against the contract: **[docs/DEPLOY.md](docs/DEPLOY.md)**.

## Security rules

Hearth writes whatever a handler returns to a file that everyone is then served. That makes a few
things your responsibility.

1. **Treat route parameters as hostile.** Validate them before the database and before anything else
   (the example accepts only `^[a-z0-9]+(-[a-z0-9]+)*$`, max 80). Hearth refuses paths that cannot be a safe
   file name, but it cannot know what a valid slug is. Use parameterised SQL only.
2. **Pages must be the same for everyone.** The cache key is the URL path. Never vary a page by cookie,
   `Authorization`, `Accept-Language`, or the query string, and never put anything user-specific or secret in
   a template. Handlers receive the full request; do not read those from it. If a page is private, do not
   serve it through Hearth.
3. **Escape, always.** `html/template` escapes by context. Never convert user content with `template.HTML`
   or friends. Put data in scripts only through the `json` helper.
4. **Validate redirect targets.** `hearth.Redirect` sends the target to the browser as given. If it comes from
   a database, check it is a plain on-site path (the example builds it as `"/post/" + validatedSlug`).
5. **Return errors, not partial pages.** Only 200 responses are written. A handler that swallows a failed
   lookup and returns empty data gets an empty page cached until the next version flip.
6. **`BaseURL` is configuration, not input.** Canonical, hreflang and sitemap URLs never come from the
   `Host` header. In subdomain/domain mode an unknown `Host` is a 404, and each language is cached under the host
   *you configured*, never the request's.
7. **Keep `/healthz` internal.** It reports counters and the cache version. The shipped web server configs
   restrict it to private ranges.
8. **Never serve dotfiles from the cache.** The cache writes through `.tmp-*` files; the web server configs
   deny all dotfiles.
9. **Mount the cache read-only in the web server**, run the Go server as a non-root user that owns it, and
   put rate limiting in front. Misses are bounded (`MaxConcurrent`, `RenderTimeout`, one render per URL at a
   time) so a 404 flood cannot exhaust your database, but unrouted URLs still reach the Go server.
10. **Custom `Funcs` must be pure and safe for concurrent use.** Pages render in parallel and the output is
    cached for everyone, so a function may not read the clock, a request, a cookie or shared mutable state. It
    may return `template.HTML` only when it builds the markup itself from trusted numbers (`strconv`, a sparkline
    from `[]float64`), never from a string a user wrote.
11. **Dependencies:** the library uses the standard library and `golang.org/x/sync`. Nothing else.

## When not to use Hearth

- **Pages that differ per user or per request:** dashboards, carts, anything behind a login, search results
  keyed by query string. The cache is per URL path.
- **Data that changes constantly.** `SetVersion` empties the whole cache and `Site.Invalidate("/post/42")` drops
  named pages, but there is no tag or dependency tracking: you name every affected page (lists, related
  pages). A site where rows change every few seconds would re-render continuously. It suits content that
  changes a few times an hour or less.
- **Content pipelines.** There is no Markdown, no CMS integration, and local data is JSON only.
- **Component-heavy front ends.** Hearth gives you Go templates plus Vite for assets, not islands or a
  component framework. Use Astro, Next or similar for that.
- **Millions of URLs.** Every page is a file; mind inodes and disk. Sitemaps split automatically at the protocol limits.
- **A site that is not Go on the server** and needs SSR. Fully static sites work from any host, but SSR
  needs the Go package.

## Status and limits

Verified here: the Go packages (`go test -race`), the Node packages, a real Vite + sharp build through the
npm wrapper, the parity between built files and a server, and the example's HTTP-level tests.

Not verified: a run against real Postgres, Docker builds and compose runs, live Caddy and nginx (use
`deploy/verify.sh`) and `hearth dev` over real TCP with Vite's HMR socket. The
sandbox this was built in blocks Docker and local ports. Multi-width image encoding was tested with a fake
encoder, not real sharp, and browsers' `srcset` selection was not observed.

## Repository

```
cmd/hearth/       the CLI
pkg/hearth/       the public Go package (the only importable one)
internal/         route, render, i18n, manifest, cache, config, build, dev, starter, ...
npm/hearth/       npm wrapper and the @lok-tech/hearth/vite image plugin; scripts/build-npm.mjs builds the platform packages
example/          Postgres example, its own Go module
deploy/           Caddy, nginx, compose, verify.sh
docs/             README.md (index), PLAN.md, ARCHITECTURE.md, DEVELOPMENT.md, DEPLOY.md, MIGRATING-FROM-ASTRO.md
```

Dependency direction: `cmd -> internal tools (build, dev) -> pkg/hearth -> internal core`. Only `pkg/hearth`
may import the renderer; a test fails if anything else does.
