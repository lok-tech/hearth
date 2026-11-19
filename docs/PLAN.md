# Hearth: plan and status

Hearth builds and maintains websites with a focus on SEO and performance. It replaces tools like Astro:
static pages and SSR pages share **one template location and one render logic**. SSR output is baked
into files, and a web server serves those files at static speed.

Out of scope: Playground changes (its own agent will handle them). Static files may be served from the
web server's disk, S3, or a CDN.

**Status (2026-11-18): D2 to D6 are implemented.** What is verified and what is not is listed at the end
of this page. See [DEPLOY.md](DEPLOY.md) for serving and the repository README for usage.

## Decisions log

| Topic | Decision |
|---|---|
| Template system | Go `html/template` only. One shared layout with blocks: title, description, canonical, head, body. Canonical and hreflang are emitted by the layout from `.Canonical` and `.Alternates`. |
| Interfaces | (1) npm package `hearth` = CLI for static sites and pages. (2) Go package `pkg/hearth` = SSR in your own server. **Both run the same engine** (see "One engine"). |
| Install | CLI: `npm i -D @lok-tech/hearth` (wrapper + per-platform binaries, esbuild pattern; six platforms, static Linux binaries). Go: `go get github.com/lok-tech/hearth`, import `.../pkg/hearth`. Published as 0.1.0 (npm) and tagged v0.1.0 (Go). |
| Routing | File path = route. `pages/about.html` -> `/about`. `pages/post/[id].html` -> pattern `post/{id}`. No `mode:` setting. |
| SSR routes | Registered in Go by pattern, e.g. `site.Handle("post/{id}", loadPost)`. Checked against the discovered template; a typo fails at startup (`Handle` returns an error, `MustHandle` panics). A parameterless page takes a handler only when it is listed in `ssr` (next row). |
| Static-route handlers (`ssr`) | `"ssr": ["/candidates"]` in `hearth.config.json` marks parameterless pages whose data comes from a handler. Validated against the routes (an unknown entry, or one with a `[param]`, fails `check` and `build`), recorded in `manifest.json` (`ssr`) with `BundleFormat` 2 (now 3, see Vite entries), so the server needs no config. `hearth build` does not prerender them (a file would shadow the handler); `Site.Handle` on a parameterless page outside `ssr` is an error; `Site.Validate()` lists `ssr` pages with no handler (they answer 404). `check` and `dev` render them from `pages/<name>.mock.json`; the sitemap still lists them. `Options.SSR` adds to the manifest list for tools that run a `Site` over a source tree. |
| Custom template functions | `Options.Funcs template.FuncMap`, checked in `New`: no shadowing of the helpers or Go builtins, each value a func with one result (or result plus error). Must be pure and concurrency-safe; `template.HTML` only from trusted numbers. Config `funcs` lists the names; the CLI registers stubs that fail when called, so `check` parses every template and reports pages that need the Go program (as notes, not failures) while `build` fails on them. A site with funcs builds with `hearth.Build(ctx, site, outDir)` (Prerender + Export + sitemap/robots, which the CLI build now calls too), from its own binary. No formatting helpers in the core. |
| 404 and 410 pages | `pages/404.html` and `pages/410.html` (top level only; `404.he.html` overrides per language) render inside the shared layout with `.Status` set. They are not routes: the route table keeps them apart (`Table.ErrorPage`), so no sitemap, `Prerender`, `Build`, collection or `ssr` code sees them; `pages/docs/404.html` is an ordinary page. `Site` uses them for `ErrNotFound` / `ErrGone`, and for unmatched paths in a known language (an unknown host stays plain text). `Options.Fallback` still wins for unmatched paths. They are `noindex` (meta and `X-Robots-Tag`), `Cache-Control: no-store`, never written to the cache. Absent template or a failing render at request time: plain text with the same status (the failure is logged). `hearth check` trial-renders them in every language and `build` fails on a broken one. `hearth.Build` / `hearth build` write the rendered 404 as `404.html`: at the output root for the default language in prefix mode plus `<lang>/404.html` for each other language, and `<host>/404.html` per host in subdomain/domain modes. 410 is not written to a file (web servers have no use for it). Parity test compares each `404.html` with the server's 404 body. |
| Robots meta | `PageData.NoIndex` is filled from `Meta.NoIndex` and the layouts emit `<meta name="robots" content="noindex">` (the `X-Robots-Tag` header stays for handler pages). A static page declares it with `{{define "robots"}}noindex{{end}}`; the renderer executes that template and folds it into `NoIndex` (any other output is an error), so both ways give one tag in the same place and the cached file contains it. Deviation from the first sketch: the layout does not read a `block` (a block would print the word into the head); the engine reads the define instead. |
| Static data | Local files only, JSON only in v1 (`data/` folder, read with the `data` template helper; names are validated against traversal). YAML maybe v2. No Markdown. |
| Enumerated params | A `[param]` page with a bound local collection (`collections` in `hearth.config.json`) is prerendered for every entry at build. Without one it is SSR only. Entries with empty, duplicate or path-like parameter values fail the build. |
| i18n URLs | Per project: path prefix, subdomain, or domain (`Options.I18n{Mode, Hosts}`). Paths are identical in every language (no localised slugs). The server resolves the language from the path prefix or the `Host`; an unknown host is a 404. One module (`internal/i18n.URLs`) answers language, location, absolute URL and alternates for both the server and the build. |
| i18n texts | JSON catalogs `locales/<lang>.json`, `{{ t "key" }}`, plurals as CLDR suffix keys (en, he, ar, ru and a few more implemented). Optional per-language template override (`about.he.html`). Missing key fails the build and `hearth check`. |
| hreflang and canonical | Both come from the one `pageData` function, so static and SSR pages carry the same tags. hreflang only for sites with more than one language and a `BaseURL`; canonical omitted without a `BaseURL` (a relative one is invalid). |
| Cache | Path = sanitised URL path (`/x` -> `x/index.html`), versioned dirs + atomic relative `current` symlink, atomic writes through `.tmp-*`, only 200s cached. Host directory for subdomain/domain i18n, taken from configuration, never from the request. |
| Cache versions | `<bundle build id>.<data version>`. A new bundle starts an empty cache (a redeploy never serves pages from old templates or old asset hashes); a restart with the same bundle keeps its cache. Old versions are pruned (`KeepVersions`, default 2). |
| Invalidation | Event/subscribe API in the package (`SetVersion`, `Subscribe`, stdlib-only `Poll`). The server decides what triggers a change. No database driver in the library. `SetVersion` flips the whole cache; `Invalidate(paths...)` drops named pages (all languages) with a per-path generation check so an in-flight render cannot re-cache stale output. No tags yet. |
| Go version | `go 1.27.3` in both modules (the library and the example), `golang:1.27-alpine` in the Dockerfiles. A low floor was considered and dropped: library users need Go 1.27 or newer. |
| Dependencies | Library: stdlib + `golang.org/x/sync` (singleflight, semaphore). Nothing else. The example module adds `pgx`. |
| One engine | `hearth build`, `check` and `dev` do not render pages themselves; they drive a `hearth.Site`. `build` = `hearth.Build` (`Site.Prerender` + `Site.Export` + sitemap and robots), which a Go program calls with its own handlers and `Funcs`. A test builds a site and requires every built file, `sitemap.xml` and `robots.txt` to equal what a separate server answers, byte for byte (prefix and subdomain modes); a second test requires an `ssr` page baked with `Prerender` + `Export` to equal what a `Site` serves. Another test fails if any package other than `pkg/hearth` imports the renderer. |
| Dependency direction | `cmd -> internal tools (build, dev) -> pkg/hearth -> internal core`. `pkg/hearth` is the only public Go package and never imports the tool side. One repo for now; splitting later is a mechanical move (publish `pkg/hearth` as its own module). |
| Dev modes | `mock` (a dev-mode `Site` rebuilt per request over the source tree, `*.mock.json` data, templates reload without a restart), `live` (reverse proxy to the project's Go server), `preview` (prod flow locally: file in `dist/public`, else proxy to the server). In `mock`, `{{asset}}` points at the Vite dev server and unmatched paths are proxied to it. |
| Vite | Vite for assets and HMR. `hearth build` runs the configured command with `--outDir --emptyOutDir --manifest` (and `--base` when `assetBase` is set), converts Vite's manifest, and removes build-internal files from `public/`. Verified with a real Vite 6 build. HMR socket and `vite preview` over real TCP not verified. |
| Images | `@lok-tech/hearth/vite` plugin (sharp): `src/images/**/*.{jpg,png}` -> AVIF + WebP + original, content-hashed, `width`/`height` in `{{picture}}`. Plugin option `widths` (default `[maxWidth]`) writes every format at every width; `picture` and `pictureSet` emit `srcset`/`sizes`. |
| Responsive images and art direction (G16, G17) | `hearthImages({ widths })`: default `[maxWidth]` (output unchanged); validated as positive integers, at most 8 after de-duplication, none above `maxWidth` (an error, not a clamp, because a width the cap would drop is a typo); sorted. The encoder runs once per width and a duplicate (same type and width, a narrow source returning its own size) is dropped, so the no-upscaling rule is enforced by the plugin, not only by sharp. `images.json` keeps its shape and the bundle format stays 3: `Manifest.Picture` groups entries by type in first-appearance order and sorts each group by width; a one-width group is a bare URL (byte-identical to before, proven against the old implementation in a test), several become `url Ww, ...` with `sizes` (default `100vw`, emitted only when a srcset has `w` descriptors). The `<img>` `src` is the largest original-format rendition. `picture` is variadic (`name alt [sizes] ["eager"]`; empty sizes and wrong counts are errors); `pictureSet alt sizes (media name)... "" fallback ["eager"]` emits every format as a `<source media>` per art pair then the fallback's own sources and `<img>`; a trailing `eager` is told apart from a name by pair parity. `media` rejects `"` and control characters. Both return `template.HTML`. Lenient and dev modes keep placeholders. |
| Assets on a CDN | `assetBase` in the config prefixes every manifest URL and is passed to Vite as `--base`. Same-domain path routing (no `assetBase`) is the recommended shape. Assets are additive: never delete old ones on deploy. |
| Sitemap and robots | Answered by the `Site` (all languages in prefix mode, the host's own language in host modes); `build` saves those answers once per host, so static files match the server's. Server-rendered pages are added through `Options.SitemapURLs` (`SitemapEntry{Path, LastMod}`); `SitemapEntries` still works (deprecated, no date) and both are merged. `<lastmod>` is a W3C datetime in UTC and appears only when the caller supplied it: static pages have none (no trustworthy source exists: bundle build time changes on every deploy and would tell crawlers everything changed), and Hearth never invents dates. The same path twice keeps the latest date; output is sorted by URL. Above 50,000 URLs or 50 MB, `/sitemap.xml` is a `sitemapindex` of `/sitemap-1.xml`, `/sitemap-2.xml`, ... (consecutive slices of the same sorted list); `hearth.Build` writes the same files, and `robots.txt` still names `/sitemap.xml`. A request for a part that does not exist, or any part while one file suffices, is a 404, so `/sitemap-N.xml` is reserved. The parts are cut from a fresh list per request, so a list that changes between reading the index and a part can disagree briefly. |
| Vite entries | `manifest.json` carries `entries` (`name -> {js, css[], preload[]}`) resolved from the whole Vite manifest (static imports, recursive, cycle-safe, de-duplicated; CSS of imported chunks first). `{{entry "src/site.ts"}}` emits the stylesheet links, `modulepreload` hints and the module script; in Vite dev-server mode the client and module script; unknown entries are placeholders in lenient mode and an error otherwise. `assets` keys are unchanged, so `{{asset}}` still works. This changed the manifest meaning, so `BundleFormat` is 3. Dynamic imports are not preloaded. |
| `json` helper | Returns `template.JS`: inserted verbatim in `<script type="application/json">` and attribute-escaped in `data-x="{{json .X}}"`, both round-trip (tests). It used to return `template.HTML`, which `html/template` double-encoded inside a script. No `jsonAttr` helper. |
| Starter | Embedded in the binary, written by `hearth init`; refuses to overwrite and checks every target first. |
| Public env vars (round 2, G10) | The starter's `vite.config.ts` sets `envPrefix: ['VITE_', 'PUBLIC_']` with a comment that prefixed values are baked into the client and public. `hearth check` prints a warning (`build.Warnings`, never an error, exit code unchanged) when `src/**` reads `import.meta.env.NAME` and the site's `vite.config.*` has no `envPrefix` covering `NAME`. Text scan only: `VITE_` always counts, string or array `envPrefix` is read, comments are stripped, Vite built-ins (`MODE`, `DEV`, `PROD`, `BASE_URL`, `SSR`) are ignored, and an `envPrefix` that is not a literal (a variable) silences the warning, because a miss is acceptable and a false warning is not. A missing vite config counts as "no prefix". Docs carry the secrets rule and the page-data + `json` pattern for values that change without a rebuild. |
| `dev.proxy` (G11) | `dev.proxy` in `hearth.config.json` maps a path prefix to a backend URL for `hearth dev` in both watch mode and `vite.devURL` mode (`dev.NewMock` / `dev.NewWatch` wrap their handler). Checked before page routes, the Vite fallback and live reload (the proxy sits outside the reload middleware so backend HTML is never injected), longest prefix first, whole path segments. `httputil.ReverseProxy` with `Rewrite`: the backend gets its own `Host`, `X-Forwarded-For/Host/Proto` describe the original request, path/query/body/headers/upgrades pass through; redirects and cookies are not rewritten. Validation (`config.ValidateProxy`, so `config.Load` and `check` both enforce it): prefix starts with `/`, **no trailing slash at all** (stricter than the plan: this removes the `/api` vs `/api/` duplicate class instead of normalising it), plain segments only (no `.`, `..`, empty, query, fragment, space, braces, `%`), never `/`, never `/healthz`, `/robots.txt`, `/sitemap.xml` (or beneath them), never the live reload path; targets absolute http(s) without fragment. `/sitemap-N.xml` is not specially refused. `preview`, `-live` and production ignore the key. |
| `Site.WarmSitemap` (G12) | `WarmSitemap(ctx, concurrency) (PrerenderReport, error)` = the sitemap's merged path list (static and `ssr` pages first, then `SitemapURLs`/`SitemapEntries`, de-duplicated) handed to `Prerender`, so it is the one engine: every language, host-mode aware (it sets the host, unlike `Warm`), `MaxConcurrent`, `RenderTimeout`, singleflight, only 200s cached. A cancelled `ctx` reports the remaining pages as failures with the context error. A render writes under the version active when it began (existing per-render version capture), so a flip mid-warm cannot write into the new version. An error from the entries callbacks is returned. The report type is `PrerenderReport` (not a new type). The example warms in the background at startup; `Invalidate` is for targeted changes. |
| Security stance | Hearth makes unsafe cache paths impossible and escapes by context, but some safety is the caller's: validate route parameters, never vary a page by cookie, header or query, validate redirect targets, return errors instead of partial pages. Stated as rules in the README and enforced in the example's tests. |
| Trailing slash, deploy file split, route vs `public/` (G13 to G15) | Docs and `verify.sh` only. Verified from code (with a test, `TestTrailingSlashRedirectShape`): `/x/` is a 301 to `/x`, `/` is excepted, repeated slashes collapse in one hop, languages follow the same rule, and **the Go server drops the query string** on that redirect; the Caddy `redir` drops it too, nginx keeps it (documented, configs unchanged). The Go container needs only `dist/templates` (it holds the only manifest it reads; `dist/manifest.json` is an unused copy) and the web server needs `dist/public`. Reading both shipped configs, a route path that is a `public/` directory without an index falls through to Go (nginx `try_files $uri` is a file test; Caddy `canonical_uris false` + `pass_thru`), so no config change was made; the fallback is a distinct prefix for the files. `{{picture}}` stays without `media` (art direction arrived later as `pictureSet`, see the responsive images row); hand-written `<picture>` remains the way to use `public/` files. `verify.sh` gained `VERIFY_SLASH` (space or comma separated paths) and `VERIFY_DIR_COLLISION` (+ `_TEXT`), each with fake-curl passing and broken modes. |

## Layout

A site:

```
site/
  pages/        index.html, about.html, about.he.html, post/[id].html, post/[id].mock.json
  layouts/ partials/
  data/         *.json (local data sources)
  locales/      en.json, he.json
  src/          entry.ts, *.css, images/
  public/       copied to the site root as-is
  hearth.config.json
```

Build output:

```
dist/public/       static pages + hashed assets + sitemap.xml + robots.txt (deploy anywhere, or sync assets to S3)
dist/templates/    bundle for the Go server (embed.FS or a path), includes manifest.json
dist/manifest.json
```

The build and the server both record a bundle format version; a mismatch fails loudly.

The repository:

```
cmd/hearth/       the CLI
pkg/hearth/       the public Go package: Site, Options, Handler, Prerender, WarmSitemap, Export, Warm, Poll
internal/         cache route i18n manifest render data config build dev starter fsutil sitemap testutil archtest
npm/hearth/       wrapper, bin, @lok-tech/hearth/vite; scripts/build-npm.mjs makes the six platform packages
example/          Postgres example, its own Go module (imports only pkg/hearth)
deploy/           Caddy, nginx, compose, verify.sh (+ self-test)
docs/             PLAN.md, DEPLOY.md
```

No Go files in the repository root.

## Go package API

```go
site, err := hearth.New(hearth.Options{
	Bundle:   bundleFS,                      // dist/templates (embed.FS or os.DirFS)
	CacheDir: "/var/cache/hearth",
	BaseURL:  "https://example.com",
	I18n:     hearth.I18n{Mode: "prefix"},   // or "subdomain" / "domain" with Hosts
	// MaxConcurrent, RenderTimeout, CacheControl, KeepVersions, Logger, SitemapURLs (SitemapEntries is deprecated),
	// Dev, DevAssetURL, LenientAssets, Fallback
})
site.MustHandle("post/{id}", loadPost)       // (data any, meta hearth.Meta, err error)
                                             // err: ErrNotFound 404, ErrGone 410, Redirect(301, to), other 500
site.SetVersion("v42")                       // invalidate: flip the symlink, pages refill lazily
site.Subscribe(ctx, src)                     // src implements Watch(ctx, notify func(version string)) error
hearth.Poll(fn, interval)                    // stdlib-only polling Source
site.Warm(ctx, urls, n)                      // render and cache literal URLs
site.WarmSitemap(ctx, n)                     // every sitemap path in every language, after SetVersion
site.Prerender(ctx, paths, n)                // every language-neutral path in every language; errors reported per page
site.Export(dir)                             // copy the rendered pages out (this is `hearth build`)
mux.Handle("/", site)                        // plain http.Handler
```

## CLI

`hearth init`, `hearth build`, `hearth check`, `hearth dev` (mock, `-live`), `hearth preview`, `hearth version`.
`hearth build` runs the Vite command from the config first, then renders the pages through `hearth.Site`.
`hearth check` needs no Vite build and reports all problems at once.

## Deliverables

| | Deliverable | Status |
|---|---|---|
| D2 | **Library core**: routes, templates and layouts, i18n, manifest and asset helpers, cache, invalidation API, singleflight and concurrency limit, HTTP behaviour (404, 410, 301 resolver, Cache-Control, ETag, canonical, hreflang, robots, sitemap), warmer, logging, `/healthz`, counters | Done. Tested: cache path safety, atomic write, version flip and bundle invalidation, pruning, singleflight, manifest parsing, redirects, both i18n modes, HTTP-level tests with a fake data source |
| D3 | **CLI**: `build`, `dev` (mock, live), `preview`, `check`, plus `init`, `version` | Done. Built on `hearth.Site`; parity and architecture tests |
| D4 | **Vite starter and npm distribution**: multi-entry, TypeScript, CSS, hashed assets, image step, fonts note | Done and run end to end through the npm wrapper with real Vite and sharp (from local tarballs). Not published. The brief's rules were never seen; the starter's rules are my own |
| D5 | **Deployment docs**: Caddy and nginx, S3/CDN, compose, release order and rollback, `verify.sh` | Done, **not run against a live server**. `verify.sh` is tested against a fake server (correct passes, ten violations fail); compose is validated with `docker-compose config` |
| D6 | **Example and README**: Postgres example in its own module, one-command run, HTTP-level tests, architecture diagram, security rules, "when not to use Hearth" | Done. Postgres SQL and the Docker stack not run |

Beyond the original list: package restructure (`pkg/` + `internal/`), the single-engine inversion, bundle-versioned
cache, `assetBase`, and subdomain/domain i18n in the runtime `Site`.

## Verified

- Go 1.27.3: `go vet` and `go test -race` on the root module (cache, route, i18n, manifest, render, data, config, build,
  dev, starter, sitemap, fsutil, `pkg/hearth`, CLI, architecture) and on the example module.
- Both guard tests were checked by breaking the rule on purpose and watching them fail.
- Node: wrapper (argument and exit-code passthrough, missing platform package) and the image step (with a fake encoder).
- A real `hearth init` + `npm install` from packed tarballs + `hearth build` with Vite 6 and sharp, and `tsc --noEmit`
  on the starter.
- The six platform binaries cross-compile; only darwin-arm64 was executed.
- `docker-compose config` on both compose files.

## Still unverified

- Real sharp encoding of several widths (tests use a fake encoder), and how browsers choose from `srcset`/`sizes`.
- A run against real Postgres (the store is compiled and vetted, its SQL has not been executed).
- Docker image builds and compose runs (Docker is blocked in the development sandbox).
- Caddy and nginx behaviour: `file_server { canonical_uris false; pass_thru }`, the `{re.name.1}` redirects, the
  nginx config under `nginx -t`. Run `deploy/verify.sh` against them.
- The new 404 pieces on real servers: Caddy `handle_errors` with `rewrite * /404.html` and `file_server { status 404 }`
  in `Caddyfile.static`, and nginx `error_page 404 /404.html` with an `internal` location. `verify.sh` checks them
  with `VERIFY_404_TEXT`.
- A Hebrew (percent-encoded, non-ASCII) path through real Caddy or nginx: the Go side is tested (cache file named
  with the UTF-8 letter, `Prerender`, `Export`, `Invalidate`), and `verify.sh` has the check (`VERIFY_UNICODE`) and
  a fake-server self-test, but whether Caddy matches the decoded path to that file name has not been run.
- S3/CloudFront: the sync commands, the viewer-request function, origin-group failover.
- `hearth dev` and `hearth preview` over real TCP, `vite preview`, and the HMR websocket. These need local port
  binding enabled (`sandbox.network.allowLocalBinding: true`) or a run on your side. The handlers are tested in
  process.
- Publishing to npm: unscoped `hearth` is taken, so the packages are `@lok-tech/hearth` and `@lok-tech/cli-*` (org `lok-tech`).
- The five platform binaries other than darwin-arm64.
- Round 2, Phase 7: `VERIFY_SLASH` and `VERIFY_DIR_COLLISION` against real Caddy or nginx (only the fake-server
  self-test ran), in particular Caddy's `pass_thru` on a directory without an index file and nginx's `try_files` on
  a directory path; the claim that the Caddy `redir` drops the query string and that nginx keeps it is from the
  directives' documented behaviour, not a run.
- Round 2, Phase 6: a real Vite build exposing a `PUBLIC_` variable through the starter's `envPrefix` (the
  starter config and the `check` warning are tested as text); `dev.proxy` over real TCP (tests use in-memory
  backends through `httptest.NewRecorder`, so WebSocket upgrades and streaming bodies are not exercised) and
  `dev.NewWatch` with the proxy (the ordering is tested by composing `withProxy` with the reload middleware, not by
  running Vite); `WarmSitemap` timing and database load on a large site (tests use a few pages).

## Known limits and open items

Design limits (documented in the README):

- **Invalidation is by path, not by tag.** `SetVersion` empties the whole cache; `Invalidate` drops the pages you
  name. Dependents (lists, sitemap entries) must be named too. One process per cache dir: the generation
  counters are in memory, so replicas sharing a cache directory are not protected from the stale-write race.
- No Markdown.

Open items, roughly in order of value:

1. **Handlers see the whole request.** Reading cookies, `Authorization` or the query would cache personalised output.
   The README forbids it; stripping them from the request handed to handlers would make it impossible.
2. **Tag-based invalidation** (routes declare tags; `InvalidateTags`), and a file-based generation scheme if a cache dir is ever shared.
4. **`Site.Warm` does not set a `Host`**, so it cannot warm host-mode languages; `Prerender` and `WarmSitemap` do. Documented.
5. **Pruning race.** A render that began before a flip can recreate its (pruned) version directory when it writes.
   Harmless but leaks a directory until the next prune.
6. **`hearth dev` runs Vite in watch mode itself** (one port, live reload, no HMR) unless `vite.devURL` is set, which keeps the proxy mode where you run `vite` in a second terminal. The per-request `Site` rebuild in
   `mock` mode has not been measured on a large site.
8. **Publish**: check the npm names, then the platform packages first and `hearth` last (`scripts/build-npm.mjs`).
9. **Repository hygiene**: Splitting `pkg/hearth` into its own module can wait until a release cadence difference appears.
10. **The brief.** Rules from the original brief were not available when the starter was written; reconcile them
    with the starter's README rules.
