# Plan: support voters.co.il (blockers and gaps)

> **Historical.** This plan was written before the layers refactor ([LAYERS.md](LAYERS.md)). `Options.Dev`,
> `DevAssetURL`, `LenientAssets`, `Options.Fallback` and the `.Dev` template variable named below were removed:
> use `Options.Overrides` / the `HEARTH_*` variables, `Site.Matches` and `.HMR`.

Source: an agent's read-through of the Hearth docs and code against the voters.co.il Astro site
(2026-11-19). It found 2 blockers and 9 smaller gaps. Nothing was run against the site. This plan
turns each item into a design, the code it touches, and a test that proves it. Items 1 to 3 are the
only real Hearth work blocking a pilot; the rest are small or belong to the voters repo.

Ground rule from [ARCHITECTURE.md](ARCHITECTURE.md): one engine. Every change below goes into `Site`
(`pkg/hearth`) or `internal/render`, never into a second render path. Each phase keeps the parity test
(build output equals server output) green.

## Summary

| # | Item | Kind | Where | Phase |
|---|---|---|---|---|
| B1 | Static-route handlers (pages with no `[param]`) | Blocker | Hearth | 1 |
| B2 | Custom template functions (`Options.Funcs`) | Blocker | Hearth | 1 |
| G1 | 404 and 410 templates | Gap | Hearth | 2 |
| G2 | `<meta name="robots">` from page data | Gap | Hearth | 2 |
| G3 | Entry helper: all CSS, modulepreload, script tag | Gap | Hearth | 3 |
| G4 | Sitemap `lastmod` (and the 50,000 URL limit) | Gap | Hearth | 3 |
| G5 | `<style>` blocks, `define:vars`, `/?t=` script | Gap | docs and recipes | 4 |
| G6 | Candidate photos and logos | Gap | docs only | 4 |
| G7 | Go 1.27 module and Dockerfile for the web container | Gap | voters repo | 5 |
| G8 | Redirect store (`url_redirects`) and `search.json` | Gap | voters repo | 5 |
| G9 | Hebrew `{letter}` path through Caddy | Check | `verify.sh` | 2 |

## Phase 1: the two blockers

### B1. Static-route handlers (PLAN open item 3)

**Problem.** `Site.render` calls a handler only when `pg.IsDynamic()`. A page such as `/candidates` has
no parameter, so it cannot get data. `hearth build` also prerenders every static page, so a prebuilt
file would shadow any handler.

**Design.** Make "this page is server-rendered" an explicit property of the bundle, not an accident of
having a `[param]`.

1. Config: `"ssr": ["/", "/candidates", "/parties", "/elections", "/coalition"]` in
   `hearth.config.json` (`internal/config`). Validated against the discovered routes; an unknown
   entry fails `hearth check`. Parameterised pages need no entry (they are already SSR unless bound to a
   `collections` entry).
2. Bundle: the build records the list in `manifest.json` (`ssr`) and bumps `BundleFormat` to 2, so the
   server knows without reading the config. Old bundles fail loudly, as designed.
3. `planPages` skips `ssr` pages: `hearth build` does not prerender them and does not write a file
   that would shadow the handler. `hearth check` still trial-renders them from
   `pages/<name>.mock.json` (`Page.MockFile` already exists).
4. `Site.render`: look up a handler for every matched page, not only dynamic ones. Outcome handling
   (404, 410, redirect, 500, `Meta`) is unchanged.
5. `Site.Handle` rules for a page without parameters:
   - registered on a page that is **not** in `ssr`: error ("would be shadowed by the prebuilt file");
   - a page in `ssr` with no handler: logged at startup by a new `Site.Validate()` (called by `New`'s
     caller, and by `hearth check` against mocks) and answered 404 at request time, as dynamic pages
     already are.
6. Sitemap: `ssr` static pages stay in the sitemap (they are real, indexable URLs). No code change,
   since `sitemap()` lists every non-dynamic page.
7. Dev `mock` mode: reads `<page>.mock.json` as the page's data, same as dynamic pages today
   (`internal/dev/dev.go`).
8. Invalidation: works as is. After a dedup the voters server calls
   `site.Invalidate("/candidates", "/parties", "/")`, or `SetVersion` for a full flip. Add a doc note and
   a test that `Invalidate("/candidates")` drops all languages of a parameterless page.

**Tests.**
- Handler on an `ssr` page renders data and caches the file (`Hits` increments on the second request).
- Handler on a non-`ssr` static page fails `Handle`.
- `hearth build` output contains no `candidates/index.html`; the sitemap still lists `/candidates`.
- Parity test extended: an `ssr` page served by a `Site` with a handler equals what `Prerender` +
  `Export` would write for the same handler.
- 404, 410, 301 from a parameterless handler.

**Cost.** About 150 lines plus tests. No new dependency.

### B2. Custom template functions

**Problem.** Only `t`, `plural`, `asset`, `picture`, `json`, `data` exist. voters needs about 20
number formats (`toLocaleString('he-IL')`, `toFixed(2)`), Hebrew sorting, arithmetic and `renderTrend`
(SVG).

**Design.**
1. `Options.Funcs template.FuncMap` on `hearth.New`, passed to `render.Options.Funcs` and merged in
   `Renderer.funcs`.
2. Rules, all enforced in `New`: a name may not shadow a built-in helper or a Go template builtin;
   every value must be a function with one result, or a result plus `error`. Errors fail the render
   (never a partial page).
3. Functions must be pure and safe for concurrent use (renders run in parallel). Stated in the README.
4. Raw HTML: still no generic "unsafe" helper. A function may return `template.HTML`; the doc rule is
   that it must be built from trusted numbers only. `renderTrend` is the model: a Go func that takes
   `[]float64` and returns `template.HTML` built with `strconv`, never from strings a user wrote.
5. **The CLI problem.** `hearth build`, `check` and `dev` do not run your Go code, and
   `html/template.Parse` fails on an unknown function. So:
   - `hearth.config.json` gets `"funcs": ["num", "dec", "trend", ...]` (names only).
   - The CLI (`build`, `check`, `dev` mock) registers a stub for each name. A stub returns an error
     "function X is provided by your Go program; run this page through it" if a page actually calls
     it, so `check` still parses every template and reports precisely which pages need the real
     program.
   - Real builds for sites with funcs run through the site's own Go binary:
     `site.Prerender(ctx, paths, n)` then `site.Export(dir)` (both exist). Provide one tested helper,
     `hearth.Build(ctx, site, outDir)`, so the voters binary gets a `web build` subcommand without
     copying the CLI's logic. That keeps "one engine".
   - Dev for these sites uses `hearth dev -live` (proxy to the Go server started with `HEARTH_ENV=dev`).
6. Ship no formatting helpers in the core (no `x/text` dependency). voters writes `num`, `dec` and the
   Hebrew-collated sort in its own module. Sorting belongs in the handler, not the template.

**Tests.** A func is callable in a page; a name clash fails `New`; a func returning an error fails the
render; stubs let `hearth check` parse a template that uses `{{num .N}}` and report it as needing the Go
program; `template.HTML` from a func is not double-escaped; `-race` run with parallel `Prerender`.

**Cost.** About 120 lines plus tests, plus the `Build` helper (about 40).

## Phase 2: correctness gaps

**Status: implemented** (G1, G2, G9 Go side and `verify.sh`). Decisions are in the PLAN.md log. Deviations: the
static `robots` define is read by the renderer and folded into `NoIndex` instead of through a layout `block`;
`404.html` goes to the root in the default language plus `<lang>/404.html` in prefix mode, and one per host
directory in host modes. The Hebrew path through real Caddy is still unverified.

### G1. 404 and 410 templates (PLAN open item 7)

- Convention: `pages/404.html` and `pages/410.html`, rendered inside the shared layout. They are not
  routes (the route table skips them).
- `Site` renders them for an `ErrNotFound` or `ErrGone` outcome and for an unmatched path inside a known
  language, with the right status. Plain text stays as the fallback when the template is absent.
  A mux or `hearth dev` takes unmatched paths first (`Site.Matches`). 404 and 410 bodies are **never cached** (only 200s
  are, as designed).
- `hearth build` writes the rendered 404 as `dist/public/404.html` (and per-host copies in host i18n
  modes) so Caddy's `handle_errors` / `not_found: 404.html` points at a real file. Add the same to the
  DEPLOY.md Caddy and nginx examples.
- The template gets `.Status` in `PageData`, so a single file can serve both codes if wanted.
- Tests: status and body for 404 and 410 pages, no cache file written, `404.html` exists after build,
  `verify.sh` checks that a missing path returns the branded body.

### G2. Robots meta from page data

- Add `NoIndex bool` to `render.PageData`, filled from `Meta.NoIndex` in `pageData`.
- The starter layout gets `{{if .NoIndex}}<meta name="robots" content="noindex">{{end}}`. The header
  stays as well. The meta tag is what survives when Caddy serves the cached file without Hearth.
- Static pages that must be noindex (voters has `noindex` on a few pages) declare it in their template:
  `{{define "robots"}}noindex{{end}}`, read by the layout with a `block`. Both paths end in the same
  tag.
- Tests: noindex page from a handler carries both header and meta; the cached file contains the meta.

### G9. Hebrew path through Caddy

- Add a Hebrew `{letter}` case to `deploy/verify.sh` (and its fake-server self-test) and a Go test with
  a Hebrew parameter in cache and `Prerender`. The agent's reading is that the cache path check allows
  it; the open question is only Caddy matching the percent-decoded path to the UTF-8 file name.
  This needs a run against real Caddy, which is already listed under "Still unverified".

## Phase 3: assets and sitemap

**Status: implemented** (G3, G4). Decisions are in the PLAN.md log ("Vite entries", "Sitemap and robots").
Deviations: the bundle format is 3, not 2 (Phase 1 already took 2). Static pages get no `lastmod`, not the
bundle build time: that changes on every deploy and would claim every page changed, and the plan itself says not
to invent dates. `lastmod` is a W3C datetime in UTC, not date-only. Only entries with `isEntry` get an `entries`
record; a missing `imports` key fails the build. The `<head>` block idea in G3 item 6 needed no code: a page puts
`{{entry "src/game.ts"}}` in its `head` block next to the layout's own call (a CSS file shared by two calls is
emitted twice). The example layout has no Vite build, so it was left unchanged; the starter layout uses `{{entry}}`.
The 50,000 URL split is tested with a lowered limit (an unexported field), not with 50,000 real URLs.

### G3. Entry helper (CSS, modulepreload, script)

**Problem.** Only the first CSS file of an entry is mapped (`entry.ts.css`), shared chunks that import
CSS lose it, there are no `modulepreload` hints, and every page hand-writes
`<script type="module" src="{{asset ...}}">`.

**Design.**
1. `runVite` reads the whole Vite manifest and, for each entry, resolves `file`, `css` and the
   recursive `imports` (with their `css`).
2. `manifest.json` gains `entries`: `name -> {js, css[], preload[]}` (format 2, same bump as B1). The
   existing `assets` keys stay, so `{{asset}}` is unchanged.
3. New helper `{{entry "src/site.ts"}}` returns `template.HTML`: one `<link rel="stylesheet">` per CSS
   file, one `<link rel="modulepreload">` per imported chunk, one `<script type="module">`. All URLs come
   from the manifest and are escaped; `assetBase` is prefixed as for assets.
4. Vite assets (`HEARTH_ASSETS=vite:<url>`): emit the Vite client and the module script instead, as the layout does today.
5. The placeholder resolver (`hearth check`) resolves unknown entries to placeholders, as `asset` does.
6. The layout takes the entry through a block, so `site.css` on the layout and `game.css` on the game
   entry both work without a rule about one CSS file per entry.

Tests: manifest conversion with a shared chunk that imports CSS; helper output; `assetBase`; dev output.

### G4. Sitemap `lastmod`

- New `Options.SitemapURLs func(ctx) ([]SitemapEntry, error)` with `SitemapEntry{Path string; LastMod time.Time}`.
  `SitemapEntries` stays (deprecated, same behaviour, no `lastmod`).
- `internal/sitemap` writes `<lastmod>` (W3C date) when set. Static pages use the bundle build time unless
  the page sets one; do not invent dates for pages with none.
- Sitemap size guard: more than 50,000 URLs or 50 MB makes `sitemap()` return a `sitemapindex` with
  numbered files (`/sitemap-1.xml`), because voters multiplies URLs by languages later. Today's 10k
  Hebrew-only URLs do not need it, so this is a small, last item.
- The `lastmod` source on voters is the candidate row's `updated_at`, returned by the same query that
  already feeds `SitemapEntries`.

## Phase 4: docs and recipes (no engine change)

**Status: implemented** (G5, G6). The guide is [MIGRATING-FROM-ASTRO.md](MIGRATING-FROM-ASTRO.md); its Go snippets run in
`pkg/hearth/migrating_test.go`. Deviation from "no engine change": the escaping test found that `json` inside
`<script type="application/json">` was double-encoded (it returned `template.HTML`, which `html/template` quotes
again in a script context). The data-attribute form was fine. Fix: `json` now returns `template.JS`; tests in
`internal/render/jsonattr_test.go`. No `jsonAttr` helper was needed. Notes found while writing: `hearth.Build` also prerenders
every `SitemapURLs` path, so the build's `Site` must not set it for database routes; a static page that calls a func
fails the CLI's `hearth build` (stubs), so such pages belong in `ssr`. Unverified: the guide against real voters
templates, the TypeScript under `tsc`, the Python parity sketch, `x/text/collate` (compiled in a scratch module only).

### G5. Astro features that have no Hearth equivalent

Write `docs/MIGRATING-FROM-ASTRO.md` with a table of each voters case and its replacement:

| Astro | Hearth |
|---|---|
| `<style>` in `404.astro`, `Legal.astro`, `admin-upload-file.astro` | Move to a CSS file imported by the page's Vite entry. No scoped styles; use a page class. |
| `define:vars` and embedded data | `<script type="application/json" id="data">{{json .Data}}</script>` and read it in the entry script. |
| `/?t=` redirect script | A small entry script reading `data-*` set from the template; no inline script. |
| `toLocaleString`, `toFixed`, `localeCompare` | `Options.Funcs` (B2) and sorting in the handler. |
| `renderTrend` | A Go func returning `template.HTML` from numbers (B2, rule 4). |

Before documenting the data-attribute route, add a test that `json` output inside an attribute
(`data-x="{{json .X}}"`) is escaped correctly: `json` returns `template.HTML`, and the attribute context
must still quote it. If it does not, add a `jsonAttr` helper rather than documenting a trap.

### G6. Images

No code. Document that `public/` is copied as is (the 59 MB of candidate photos, loaded at runtime by
`photos.ts`), and that `picture` is for `src/images/**` (brand logos). Responsive widths (`srcset`) stay
as open item 7 and are not needed for this migration.

## Phase 5: work in the voters repo

These are not Hearth changes; listed so nothing is lost.

- **G7. Go version.** New `web/` module with `go 1.27.3` and its own Dockerfile on `golang:1.27-alpine`,
  separate from `backend/` (1.22, `golang:1.23`). The web container is a new service in
  `playground.yml`; use the `playground-yml` skill.
- **G8. Redirects and search.**
  - `url_redirects` lookup lives in the `/candidates/{id}` handler and calls `hearth.Redirect(301, to)`
    after validating the target. Never prerender a database-backed route (no `collections` entry for
    it), or the prebuilt file shadows the redirect.
  - `search.json`: serve from the voters mux in front of `Site`, or behind `Site.Matches`.
- Order of the port, after Phases 1 and 2: layout and `/candidates/{id}` first as the pilot, with a
  parity check against the current Astro HTML (diff of `<title>`, description, canonical, JSON-LD,
  headings and visible text), then the five `ssr` pages, then the rest.

## Order and acceptance

1. **Phase 1** (B1, B2). Done when a Go program with a handler on `/candidates` and a `num` func serves a
   page from a cold cache, caches it, and `Invalidate` refreshes it; parity test green.
2. **Phase 2** (G1, G2, G9). Done when a missing candidate returns the branded 404 with `404.html` on disk,
   and a noindex page carries the meta in the cached file.
3. **Phase 3** (G3, G4). Done when a layout using `{{entry}}` emits every CSS file and preload hint for a
   Vite build with a shared chunk, and the sitemap carries `lastmod`.
4. **Phase 4 and 5** alongside the pilot port.

Update [PLAN.md](PLAN.md) as phases land: remove open items 3 and 7, and the "handlers attach to
`[param]` routes only" limit, and add the new decisions (the `ssr` list, `Funcs`, bundle format 2) to the
decisions log. Every item above is unverified against real voters data until the pilot runs.

---

# Round 2: more general gaps (2026-11-19)

Scope: **Hearth only.** voters.co.il is the example that exposed these needs, not the target. Each item is
written as a feature any site would use, and anything that is only voters' own port work (its Go module,
its formatters, its redirect store, its `search.json`) is out of this plan. Phases 1 to 4 above are
implemented; this round continues the numbering.

| # | Feature | Kind | Phase |
|---|---|---|---|
| G10 | Public build-time env vars reach the client | Starter, `check` warning, docs | 6 |
| G11 | Dev proxy for backend paths in `hearth dev` | Config option, docs | 6 |
| G12 | Warm the cache from the sitemap after a flip | `Site.WarmSitemap`, docs | 6 |
| G13 | Hand-written `<picture>` for art direction (dark mode logos) | Docs | 7 |
| G14 | Trailing-slash behaviour and migration checklist | Docs, `verify.sh` | 7 |
| G15 | Which server needs which files; route vs `public/` path collision | Docs, `verify.sh` | 7 |

## Phase 6: code

### G10. Public build-time env vars

**Problem.** Frontends commonly read `import.meta.env.PUBLIC_*` (Astro, Next-style naming). Vite exposes
only `VITE_*` unless `envPrefix` is set, so a client silently loses its API URL. No Hearth doc mentions it.

**Design.**
1. The starter `vite.config.ts` sets `envPrefix: ['VITE_', 'PUBLIC_']`, with a comment that every prefixed
   variable is baked into the client bundle and is public.
2. Docs (README and the migration guide): set `envPrefix` in the site's Vite config; values are fixed at
   build time; never put a secret under a prefix.
3. Values that must change without a rebuild do not belong in the bundle. Document the pattern: the
   handler puts the value in page data, the template writes it with `json` into a
   `<script type="application/json">` or a `data-*` attribute, the entry script reads it. Changing it then
   needs `Invalidate`, not a JS rebuild.
4. `hearth check` warns when `src/**` mentions `import.meta.env.<NAME>` and the site's `vite.config.*` has
   no `envPrefix` that covers `<NAME>`. Text scan only, no Vite run. A warning, never an error; a miss is
   acceptable, a false failure is not.

**Tests.** Warns for `PUBLIC_X` without a matching prefix; silent for `VITE_X` and when the prefix is
configured; the starter still passes `hearth check`.

### G11. Dev proxy for backend paths

**Problem.** Most sites call a backend (`/api`, uploads, short links) in development. `hearth dev` in watch
mode proxies nothing, so those calls fail. With `vite.devURL` set, unmatched paths go to Vite, so Vite's
`server.proxy` works, but this is undocumented.

**Design.** `dev.proxy` in `hearth.config.json`: a map from path prefix to target URL, for example
`{"/api": "http://localhost:8080"}`.
1. Applies to `hearth dev` in both modes, checked before page routes and the Vite fallback, longest prefix
   first. Matches on a path-segment boundary (`/api` matches `/api` and `/api/x`, not `/apiary`).
2. Validation: absolute `http` or `https` targets, prefixes start with `/`, a bare `/` is refused (it would
   swallow the site), and `/healthz`, `/robots.txt`, `/sitemap.xml` cannot be proxied.
3. Dev only. `preview` and production ignore it (routing there is the web server's job). `hearth check`
   validates it.
4. Docs also show the `vite.devURL` + Vite `server.proxy` alternative.

**Tests.** A request under `/api` reaches a test backend with the original path and query; a page route is
not shadowed; `/apiary` is not proxied; bad config values fail with a clear message. In-process handler
tests only, since real TCP binding is blocked in the sandbox like the existing dev tests.

### G12. Warm the cache from the sitemap

**Problem.** `SetVersion` empties every cached page, so the first crawler burst re-renders them all
(thousands of pages on a large site). `Warm` and `WarmPaths` exist, but the docs do not say how to drive a
full warm.

**Design.**
1. `Site.WarmSitemap(ctx, concurrency)` takes the same merged path list the sitemap uses (static pages,
   `ssr` pages, `SitemapURLs`/`SitemapEntries`) and renders every path in every language through the
   existing warm path, returning a report like `Prerender` (OK count, failures by path). No second render
   path.
2. Honours `MaxConcurrent` and `RenderTimeout`, and stops when `ctx` is cancelled. Output is written under
   the version active when each render began, so a flip during a warm cannot write stale pages into the
   new version.
3. Order: `ssr` and static pages first, then the rest, so a partial warm still covers the heaviest pages.
4. Docs recipe: `Invalidate` for targeted changes; `SetVersion` for a full flip, then
   `go site.WarmSitemap(ctx, 4)`. State plainly that a large warm costs real time and database load, and
   that the cache is already useful while it runs.

**Tests.** After `SetVersion` and `WarmSitemap`, every sitemap path is a hit (`Hits` rises, `Renders` does
not); an `ssr` page is warmed through its handler; a failing handler is reported, not fatal; a cancelled
context stops early; a flip mid-warm does not write into the new version.

### Phase 6 status (implemented 2026-11-19)

G10, G11 and G12 are implemented and tested; see the matching rows in the [PLAN.md](PLAN.md) decisions log.
- **G10**: starter `envPrefix`, `build.Warnings` (printed by `hearth check`, never fails it), README and
  migration guide (section 4b). Tests: `internal/build/check_test.go`, `cmd/hearth/main_test.go`,
  `internal/starter/starter_test.go`.
- **G11**: `dev.proxy` (`internal/config.ValidateProxy`, `internal/dev/proxy.go`), docs incl. the Vite
  `server.proxy` alternative. Tests are in-process only: in-memory backends through the existing `memTransport`
  and `httptest.NewRecorder`; no sockets.
- **G12**: `Site.WarmSitemap`, README recipe, example warms at startup. Tests: `pkg/hearth/warm_test.go`.
- Deviations: a prefix with any trailing slash is refused (rather than normalised); `WarmSitemap` also returns an
  `error` (the entries callbacks can fail) next to the `PrerenderReport`.
- Unverified: a real Vite build exposing a `PUBLIC_` variable, `dev.proxy` over TCP/WebSockets, and a large warm.

## Phase 7: docs and checks

### G13. Art-directed `<picture>`

No code. `{{picture}}` emits AVIF/WebP sources and a fallback, with no `media` attribute. For logos or hero
images that change with `prefers-color-scheme` or viewport, write the `<picture>` by hand with
`<source media="...">` and keep the files in `public/` (copied as is, not hashed). Say so in the README
Images section and the migration guide: `picture` is for `src/images/**`, hand-written markup is for art
direction. A hashed, optimised variant set would be a separate `pictureSet` helper, only if a site needs it.

### G14. Trailing slashes

Hearth and the bundled Caddy/nginx configs 301 `/x/` to `/x`. Sites that used to serve both forms should
know. Docs: a short "before cutover" checklist (export indexed URLs from the search console, count those
ending in `/`, confirm each 301s to the canonical form, confirm internal links and the sitemap use the
non-slash form). `verify.sh` gets a `VERIFY_SLASH` case (the slash form answers 301 with `Location` equal to
the non-slash path, for a static page and a parameter route), added to the fake-curl self-test with one
broken mode.

### G15. Who needs which files; route vs `public/` collisions

- DEPLOY.md gets a table. **Web server (Caddy/nginx/CDN):** `dist/public` (static pages, hashed assets,
  `404.html`, `sitemap.xml`, `robots.txt`, everything copied from `public/`) plus the cache directory read
  only. **Go container:** `dist/templates` only, plus the cache directory it writes.
- A route and a `public/` directory can share a path (`/photos` the page, `public/photos/` the images).
  With `pass_thru` a directory without an `index.html` must fall through to Go. Add a `VERIFY_DIR_COLLISION`
  case to `verify.sh` (a directory path without an index returns the Go page, not a listing or a 404), with a
  fake-curl broken mode. Real Caddy confirmation stays under "Still unverified" in PLAN.md. Document the
  fallback if it fails: give the files a distinct prefix.

### Phase 7 status (implemented 2026-11-19)

Docs and `verify.sh` only; the one code change is a test pinning the documented redirect shape.
- **G13**: art-directed `<picture>` snippet and the "not hashed, rename the file" note in the README Images section
  and the migration guide (section 15); `pictureSet` recorded as an open item in PLAN.md, not built.
- **G14**: behaviour verified from `Site.resolve` (`TestTrailingSlashRedirectShape`) and the configs. Finding the plan did not
  have: the query string is dropped by the Go server and by Caddy, kept by nginx. DEPLOY.md and the migration guide
  (15b) carry the cutover checklist. `VERIFY_SLASH` added.
- **G15**: DEPLOY.md "Who needs which files" table (the only manifest the server reads is `templates/manifest.json`;
  `dist/manifest.json` is an unused copy) and the route vs `public/` section. Both shipped configs should fall through to
  Go by reading their rules, so no config change; fallback is a distinct prefix. `VERIFY_DIR_COLLISION` added.
- Self-test: slash-param, slash-loc, dir-404, dir-listing broken modes added (16 modes total).
- Unverified: all of it against a real Caddy or nginx.

## Not in this plan

Site-specific port work (a site's own Go module, formatters, data logic, redirect store, `search.json`,
Dockerfile and Go version). The migration guide already shows the patterns Hearth supports for them.

## Order and acceptance (round 2)

1. **Phase 6** (G10, G11, G12). Done when the starter's Vite build exposes a `PUBLIC_` variable, `hearth
   check` warns without `envPrefix`, `hearth dev` proxies a prefix to a test backend, and `WarmSitemap`
   leaves every sitemap path as a cache hit.
2. **Phase 7** (G13, G14, G15). Done when `verify-selftest.sh` catches broken slash and directory-collision
   modes, and the README, migration guide and DEPLOY.md carry the notes and the container table.

All unverified against a real Caddy, a real Vite build and a real site until one runs.

---

# Round 3: the remaining limits (2026-11-19)

Scope: **Hearth only**, general features. These four were listed as "smaller limits" in PLAN.md and are
now planned as features. Phases 1 to 4, 6, 7 and 9 are implemented; this round continues the numbering.

| # | Feature | Kind | Phase |
|---|---|---|---|
| G16 | Responsive images: `srcset` widths and `sizes` | `@lok-tech/hearth/vite` plugin, manifest, `picture` | 9 |
| G17 | `pictureSet`: hashed, art-directed `<picture>` (`media` sources) | Helper | 9 |
| G18 | Tag-based invalidation: `Meta.Tags`, `Site.InvalidateTags` | Cache + `Site` | 10 |
| G19 | Several processes on one cache directory | Cache | 11 |

Order matters: G18 and G19 both change `internal/cache`, so G18 lands first and G19 builds on its on-disk
index. Images (Phase 9) are independent and go first because they touch different files.

## Phase 9: images

### G16. Responsive `srcset`

**Today.** The plugin writes one size per format (`maxWidth`, default 1600). `images.json` is
`name -> [{url, type, width, height}]` with the fallback last, and `picture` emits one `<source>` per
non-fallback entry plus an `<img>`.

**Design.**
1. Plugin option `widths: [480, 960, 1600]` (default: `[maxWidth]`, so existing sites change nothing).
   For each source image and each width **smaller than** the source's own width, write a rendition in every
   format (AVIF, WebP, original format), content-hashed. Never upscale: a source narrower than a width
   gets only its own size. `maxWidth` stays as the cap and stays valid on its own.
2. `images.json` keeps its shape (one entry per file, each with `width`), so no bundle format bump is
   needed. `Manifest.Picture` groups entries by `type`, in the existing order, and emits
   `srcset="url1 480w, url2 960w, ..."` for each format when a type has more than one width, or the bare
   URL when it has one (output for old single-size bundles is byte-identical to today).
3. The `<img>` fallback: `src` is the largest original-format rendition, with its `width` and `height`
   (layout shift protection) and the same `srcset`; `sizes` defaults to nothing.
4. `picture` becomes variadic: `{{picture "hero" "Alt text"}}` and
   `{{picture "hero" "Alt text" "(min-width: 60rem) 50vw, 100vw"}}`. A wrong argument count or an empty
   `sizes` string is an error. When there are several widths and no `sizes`, emit `sizes="100vw"` (the
   correct browser default), and say so in the docs.
5. Lenient and dev modes behave as today (placeholder single file).
6. Plugin tests use the existing fake encoder: rendition list, no upscaling, hash stability, `widths`
   validation (positive integers, sorted, de-duplicated, at most 8).

**Tests.** `Picture` output for 1 width (unchanged bytes), several widths, mixed (a small source),
escaping of URLs, `sizes`, the argument-count errors; `check` passes with lenient mode.

### G17. `pictureSet` (art direction)

**Problem.** `picture` cannot switch image by `media` (dark and light logos, a cropped mobile hero). Writing
the markup by hand over `public/` works, but the files are not hashed or optimised.

**Design.**
1. `{{pictureSet "Alt text" "sizes" "(prefers-color-scheme: dark)" "logo-dark" "" "logo-light"}}`:
   alt, sizes (may be `""`), then pairs of `media`, `name`. The last pair has an empty media and is the
   fallback `<img>`. Between them every pair becomes one set of `<source media=... type=... srcset=...>`
   (one per format, using the G16 grouping), so formats and widths work inside each art direction.
2. Every `name` must exist in the manifest (an error otherwise, `check` placeholders in lenient mode).
   `media` is a plain string, escaped for the attribute, and must not contain `"` or control characters.
3. The fallback `<img>` carries the width, height, `loading="lazy"` and `decoding="async"` of the fallback
   image, as `picture` does. A way to turn lazy loading off for above-the-fold images is added to both
   helpers as a trailing literal `"eager"` argument (this is also a common request; document it).
4. No new plugin work: sets are built from images the plugin already produced.

**Tests.** Dark/light pair output (order of sources, media escaping), widths inside a set, missing name,
too few arguments, no fallback pair, `eager`.

**Docs.** README Images section and the migration guide move the dark/light logo example from hand-written
markup to `pictureSet`, keeping the hand-written form as the way to use `public/` files. PLAN.md: remove
"responsive widths" and "pictureSet" from the open items.

**Status (Phase 9): implemented** (G16, G17). Deviations from the design above, all small:
- `widths` above `maxWidth` is an error (the plan allowed a clamp); duplicates are removed and the list sorted silently.
- The no-upscaling rule is enforced in the plugin as well as in sharp: the encoder runs once per width (options now
  carry `width`, `maxWidth` equals it) and a rendition with the same type and width as an earlier one is dropped.
- `images.json` and the bundle format (3) are unchanged. `Manifest.Picture` now takes `(name, alt, sizes, lazy)`; the
  variadic template arguments are parsed by `PictureArgs` and `PictureSetArgs`.
- `sizes` is emitted only on a srcset that has `w` descriptors (so a one-width image renders exactly as before even
  when a `sizes` argument is given). In `pictureSet` the `eager` option is told apart from an image named `eager` by
  the parity of the pair list.
- Art sources emit every format including the original, so a browser without AVIF/WebP still gets the art-directed
  image. Docs, the skill and the starter README are updated. Unverified: real sharp encoding of several widths,
  browsers' `srcset` selection.

## Phase 10: tag-based invalidation (G18)

**Problem.** Invalidation is by path. When one record changes, the app must name every page that shows it
(its own page, lists, ranking pages), or flip the whole cache.

**Design.**
1. A handler declares tags with its data: `Meta.Tags []string` (for example `candidate:720`,
   `candidates`). Tags are strings of letters, digits and `-_.:/`, at most 64 bytes, at most 32 tags per
   page (more is an error that fails the render, not a silent drop). Pages without tags behave as today.
2. `Site.InvalidateTags(tags ...string) error` drops every cached page carrying any of the tags, in every
   language, using the existing per-path invalidation (so the generation check still stops an in-flight
   render from putting stale output back). It returns the number of pages dropped via a small result type,
   and joins errors as `Invalidate` does.
3. **On-disk index, no memory growth.** When a render writes a tagged page, it also writes marker files
   `<version>/.tags/<hash(tag)>/<hash(path-key)>` (the file content is the cache key). `InvalidateTags` lists
   the tag directory, invalidates each key, then removes the markers. Dot directories are already ignored by
   `Export` and by the web server configs. A version flip drops the index with the pages, and a restart
   with the same bundle keeps both, so tags survive restarts (an in-memory map would not).
4. Markers are written after the page file and only when the page write succeeded and was not skipped by
   the generation check. A crash between the two leaves an untagged page, so the order is: write marker
   first, then the page; a stray marker only causes an extra invalidation.
5. Large lists tag coarsely. Document the pattern: `ssr` list pages carry `candidates` (and maybe a group
   tag), detail pages carry `candidate:<id>`, and a change calls `InvalidateTags("candidate:720",
   "candidates")`. A page cannot carry thousands of tags by design.
6. `Source`/`Poll` stays version-based. A `TagSource` is not added; the app calls `InvalidateTags` itself
   (document it next to `Subscribe`).
7. `Prerender` and `Build` ignore tags (their handlers run at build time; the static output has no
   server to invalidate).

**Tests.** Tagged page dropped in all languages; untouched pages stay; tags survive a `Site` restart on the
same cache dir; a flip empties the index; the in-flight render race (a render that began before
`InvalidateTags` does not re-cache); tag validation and the 32 tag cap; marker cleanup; `-race`.

## Phase 11: several processes on one cache directory (G19)

**Problem.** The invalidation generation counters are in memory, so two processes sharing a cache directory
are not protected from the stale-write race (a render in process B that began before process A's
invalidation can write its stale page after it).

**Design.**
1. `Options.SharedCache bool` (default false, so single-process sites keep today's zero-cost path).
2. **Generation tokens on disk.** Each stripe gets a file `<dir>/gen/<stripe>` holding a random token.
   `Invalidate` writes a new token (atomic rename) **before** deleting the page. A render reads the token
   before it reads data; after renaming its page into place it reads the token again, and if it changed it
   deletes the file it just wrote. No cross-process locks (the library cannot use `x/sys`, and file locks
   differ on Windows).
3. Honest limit, written into the docs: between the rename and the compensating delete (microseconds) a
   reader can still be served the stale page. The in-process path keeps its exact guarantee; the shared
   path is "eventually consistent within milliseconds".
4. `SetVersion` is already an atomic symlink flip, so replicas that each call it for the same version
   converge. A replica with an older bundle writes under the version it started with, so it cannot pollute
   the new version; it may read pages the new bundle rendered (additive assets, as decided in PLAN.md).
   Document this for rolling deploys.
5. Prune: keep today's behaviour; a render started before a flip may recreate a pruned directory (known,
   harmless, listed under open items).
6. The tag index (G18) is file-based, so `InvalidateTags` already works across replicas.
7. Supported filesystems: a local disk or a shared volume with POSIX `rename` and `symlink`. NFS and EFS are
   not tested and the docs say so.

**Tests.** Two `Site` values on one directory in one process (the race, both orders); a test that spawns
helper processes (`os/exec` of the test binary) running renders and invalidations concurrently, requiring
that after all invalidations finished no page rendered from older data remains; token compare with a
renamed-over file; `SharedCache` off is byte-for-byte unchanged behaviour.

## Order and acceptance (round 3)

1. **Phase 9** (G16, G17). Done when a plugin build with `widths` yields hashed renditions per format,
   `{{picture}}` and `{{pictureSet}}` emit correct `srcset`/`media`, and a single-width bundle renders the
   same bytes as before.
2. **Phase 10** (G18). Done when `InvalidateTags` drops tagged pages in every language after a restart and
   the in-flight race test passes.
3. **Phase 11** (G19). Done when the two-process test passes with `SharedCache` on and docs state the
   millisecond window.

Update PLAN.md (decisions log, remove the matching limits and open item 2 (tags), keep
"Still unverified" honest) and the hearth skill as each phase lands. Unverified until run: real sharp
encoding of several widths, browsers' `srcset` selection, shared volumes other than a local disk.
