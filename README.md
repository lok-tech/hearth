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
npm i -D hearth vite sharp      # the CLI is a Go binary in a per-platform package
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

Template helpers: `t`, `plural`, `asset`, `picture`, `json` (safe for `<script type="application/json">`),
`data`, and `.Link "/about"` for links that stay in the current language. A missing translation key
fails the build.

`hearth.config.json` covers `baseURL`, `defaultLang`, `i18n` (path prefix, subdomain or domain),
`assetBase` (S3/CDN), `vite`, `collections` and `dev`. Paths are identical in every language.

## Serve it from your own Go server

Requires Go 1.27 or newer.

```go
import "github.com/orelhadad/hearth/pkg/hearth"

site, err := hearth.New(hearth.Options{
	Bundle:   os.DirFS("dist/templates"), // or an embed.FS
	CacheDir: "/var/cache/hearth",
	BaseURL:  "https://example.com",
})
site.MustHandle("post/{slug}", loadPost) // a typo in the pattern fails at startup
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

A complete Postgres-backed server with HTTP-level tests is in [`example/`](example/).

## CLI

| Command | What |
|---|---|
| `hearth init [-name N] [DIR]` | write the Vite starter |
| `hearth check` | validate and trial-render every page in every language, listing all problems |
| `hearth build` | Vite, prerender, `dist/public` + `dist/templates` + `dist/manifest.json` |
| `hearth dev` | pages rendered from source with `*.mock.json` data; `-live` proxies your Go server |
| `hearth preview` | the production flow locally: file if it exists, otherwise your Go server |

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
10. **Dependencies:** the library uses the standard library and `golang.org/x/sync`. Nothing else.

## When not to use Hearth

- **Pages that differ per user or per request:** dashboards, carts, anything behind a login, search results
  keyed by query string. The cache is per URL path.
- **Data that changes constantly.** `SetVersion` empties the whole cache and `Site.Invalidate("/post/42")` drops
  named pages, but there is no tag or dependency tracking: you name every affected page (lists, related
  pages). A site where rows change every few seconds would re-render continuously. It suits content that
  changes a few times an hour or less.
- **Dynamic data on a page without a route parameter.** Handlers attach to `[param]` routes. A listing page
  such as `/posts` needs its own parameter (`/posts/{page}`) or to be built from local JSON.
- **Content pipelines.** There is no Markdown, no CMS integration, and local data is JSON only.
- **Component-heavy front ends.** Hearth gives you Go templates plus Vite for assets, not islands or a
  component framework. Use Astro, Next or similar for that.
- **Millions of URLs.** Every page is a file; mind inodes, disk, and sitemap limits.
- **A site that is not Go on the server** and needs SSR. Fully static sites work from any host, but SSR
  needs the Go package.

## Status and limits

Verified here: the Go packages (`go test -race`), the Node packages, a real Vite + sharp build through the
npm wrapper, the parity between built files and a server, and the example's HTTP-level tests.

Not verified: a run against real Postgres, Docker builds and compose runs, live Caddy and nginx (use
`deploy/verify.sh`), `hearth dev` over real TCP with Vite's HMR socket, and publishing to npm. The
sandbox this was built in blocks Docker and local ports. Images are emitted at one size per format (no
`srcset` widths yet).

## Repository

```
cmd/hearth/       the CLI
pkg/hearth/       the public Go package (the only importable one)
internal/         route, render, i18n, manifest, cache, config, build, dev, starter, ...
npm/hearth/       npm wrapper and the hearth/vite image plugin; scripts/build-npm.mjs builds the platform packages
example/          Postgres example, its own Go module
deploy/           Caddy, nginx, compose, verify.sh
docs/             README.md (index), PLAN.md, ARCHITECTURE.md, DEVELOPMENT.md, DEPLOY.md
```

Dependency direction: `cmd -> internal tools (build, dev) -> pkg/hearth -> internal core`. Only `pkg/hearth`
may import the renderer; a test fails if anything else does.
