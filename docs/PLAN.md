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
| Install | CLI: `npm i -D hearth` (wrapper + per-platform binaries, esbuild pattern; six platforms, static Linux binaries). Go: `go get github.com/orelhadad/hearth`, import `.../pkg/hearth`. Not published yet. |
| Routing | File path = route. `pages/about.html` -> `/about`. `pages/post/[id].html` -> pattern `post/{id}`. No `mode:` setting. |
| SSR routes | Registered in Go by pattern, e.g. `site.Handle("post/{id}", loadPost)`. Checked against the discovered template; a typo fails at startup (`Handle` returns an error, `MustHandle` panics). Handlers attach to `[param]` routes only (see limits). |
| Static data | Local files only, JSON only in v1 (`data/` folder, read with the `data` template helper; names are validated against traversal). YAML maybe v2. No Markdown. |
| Enumerated params | A `[param]` page with a bound local collection (`collections` in `hearth.config.json`) is prerendered for every entry at build. Without one it is SSR only. Entries with empty, duplicate or path-like parameter values fail the build. |
| i18n URLs | Per project: path prefix, subdomain, or domain (`Options.I18n{Mode, Hosts}`). Paths are identical in every language (no localised slugs). The server resolves the language from the path prefix or the `Host`; an unknown host is a 404. One module (`internal/i18n.URLs`) answers language, location, absolute URL and alternates for both the server and the build. |
| i18n texts | JSON catalogs `locales/<lang>.json`, `{{ t "key" }}`, plurals as CLDR suffix keys (en, he, ar, ru and a few more implemented). Optional per-language template override (`about.he.html`). Missing key fails the build and `hearth check`. |
| hreflang and canonical | Both come from the one `pageData` function, so static and SSR pages carry the same tags. hreflang only for sites with more than one language and a `BaseURL`; canonical omitted without a `BaseURL` (a relative one is invalid). |
| Cache | Path = sanitised URL path (`/x` -> `x/index.html`), versioned dirs + atomic relative `current` symlink, atomic writes through `.tmp-*`, only 200s cached. Host directory for subdomain/domain i18n, taken from configuration, never from the request. |
| Cache versions | `<bundle build id>.<data version>`. A new bundle starts an empty cache (a redeploy never serves pages from old templates or old asset hashes); a restart with the same bundle keeps its cache. Old versions are pruned (`KeepVersions`, default 2). |
| Invalidation | Event/subscribe API in the package (`SetVersion`, `Subscribe`, stdlib-only `Poll`). The server decides what triggers a change. No database driver in the library. All-or-nothing: a flip empties the whole cache. |
| Go version | `go 1.27.3` in both modules (the library and the example), `golang:1.27-alpine` in the Dockerfiles. A low floor was considered and dropped: library users need Go 1.27 or newer. |
| Dependencies | Library: stdlib + `golang.org/x/sync` (singleflight, semaphore). Nothing else. The example module adds `pgx`. |
| One engine | `hearth build`, `check` and `dev` do not render pages themselves; they drive a `hearth.Site`. `build` = `Site.Prerender` + `Site.Export`. A test builds a site and requires every built file, `sitemap.xml` and `robots.txt` to equal what a separate server answers, byte for byte (prefix and subdomain modes). Another test fails if any package other than `pkg/hearth` imports the renderer. |
| Dependency direction | `cmd -> internal tools (build, dev) -> pkg/hearth -> internal core`. `pkg/hearth` is the only public Go package and never imports the tool side. One repo for now; splitting later is a mechanical move (publish `pkg/hearth` as its own module). |
| Dev modes | `mock` (a dev-mode `Site` rebuilt per request over the source tree, `*.mock.json` data, templates reload without a restart), `live` (reverse proxy to the project's Go server), `preview` (prod flow locally: file in `dist/public`, else proxy to the server). In `mock`, `{{asset}}` points at the Vite dev server and unmatched paths are proxied to it. |
| Vite | Vite for assets and HMR. `hearth build` runs the configured command with `--outDir --emptyOutDir --manifest` (and `--base` when `assetBase` is set), converts Vite's manifest, and removes build-internal files from `public/`. Verified with a real Vite 6 build. HMR socket and `vite preview` over real TCP not verified. |
| Images | `hearth/vite` plugin (sharp): `src/images/**/*.{jpg,png}` -> AVIF + WebP + original, content-hashed, `width`/`height` in `{{picture}}`. One size per format (`maxWidth`, default 1600); no `srcset` widths yet. |
| Assets on a CDN | `assetBase` in the config prefixes every manifest URL and is passed to Vite as `--base`. Same-domain path routing (no `assetBase`) is the recommended shape. Assets are additive: never delete old ones on deploy. |
| Sitemap and robots | Answered by the `Site` (all languages in prefix mode, the host's own language in host modes); `build` saves those answers once per host, so static files match the server's. Server-rendered pages are added through `Options.SitemapEntries`. |
| Starter | Embedded in the binary, written by `hearth init`; refuses to overwrite and checks every target first. |
| Security stance | Hearth makes unsafe cache paths impossible and escapes by context, but some safety is the caller's: validate route parameters, never vary a page by cookie, header or query, validate redirect targets, return errors instead of partial pages. Stated as rules in the README and enforced in the example's tests. |

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
pkg/hearth/       the public Go package: Site, Options, Handler, Prerender, Export, Warm, Poll
internal/         cache route i18n manifest render data config build dev starter fsutil sitemap testutil archtest
npm/hearth/       wrapper, bin, hearth/vite; scripts/build-npm.mjs makes the six platform packages
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
	// MaxConcurrent, RenderTimeout, CacheControl, KeepVersions, Logger, SitemapEntries,
	// Dev, DevAssetURL, LenientAssets, Fallback
})
site.MustHandle("post/{id}", loadPost)       // (data any, meta hearth.Meta, err error)
                                             // err: ErrNotFound 404, ErrGone 410, Redirect(301, to), other 500
site.SetVersion("v42")                       // invalidate: flip the symlink, pages refill lazily
site.Subscribe(ctx, src)                     // src implements Watch(ctx, notify func(version string)) error
hearth.Poll(fn, interval)                    // stdlib-only polling Source
site.Warm(ctx, urls, n)                      // render and cache literal URLs
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

- A run against real Postgres (the store is compiled and vetted, its SQL has not been executed).
- Docker image builds and compose runs (Docker is blocked in the development sandbox).
- Caddy and nginx behaviour: `file_server { canonical_uris false; pass_thru }`, the `{re.name.1}` redirects, the
  nginx config under `nginx -t`. Run `deploy/verify.sh` against them.
- S3/CloudFront: the sync commands, the viewer-request function, origin-group failover.
- `hearth dev` and `hearth preview` over real TCP, `vite preview`, and the HMR websocket. These need local port
  binding enabled (`sandbox.network.allowLocalBinding: true`) or a run on your side. The handlers are tested in
  process.
- Publishing to npm: the `hearth` name and the `@hearth` scope may not be available.
- The five platform binaries other than darwin-arm64.

## Known limits and open items

Design limits (documented in the README):

- **Invalidation is all-or-nothing.** `SetVersion` empties the whole cache; there is no per-page or per-tag
  invalidation. Suits content that changes a few times an hour or less.
- **Handlers attach to `[param]` routes only.** A page without a parameter cannot get per-request data. Allowing it
  would need the build to skip prebuilding such pages, otherwise the prebuilt file shadows the handler.
- No 404/410 templates yet (plain text bodies). No Markdown. One image size per format.

Open items, roughly in order of value:

1. **Handlers see the whole request.** Reading cookies, `Authorization` or the query would cache personalised output.
   The README forbids it; stripping them from the request handed to handlers would make it impossible.
2. **Per-page invalidation** (see limits).
3. **Static-route handlers** (see limits).
4. **`Site.Warm` does not set a `Host`**, so it cannot warm host-mode languages; `Prerender` does. Document or fix.
5. **Pruning race.** A render that began before a flip can recreate its (pruned) version directory when it writes.
   Harmless but leaks a directory until the next prune.
6. **`hearth dev` does not start Vite**; run `npm run vite` in a second terminal. The per-request `Site` rebuild in
   `mock` mode has not been measured on a large site.
7. **404/410 templates** and **responsive image widths** (`srcset`).
8. **Publish**: check the npm names, then the platform packages first and `hearth` last (`scripts/build-npm.mjs`).
9. **Repository hygiene**: the folder is not a git repo yet. Splitting `pkg/hearth` into its own module can wait
    until a release cadence difference appears.
10. **The brief.** Rules from the original brief were not available when the starter was written; reconcile them
    with the starter's README rules.
