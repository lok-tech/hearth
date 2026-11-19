# Plan: support voters.co.il (blockers and gaps)

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
   - Dev for these sites uses `hearth dev -live` (proxy to the Go server with `Options.Dev`).
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
  `Options.Fallback` still wins for unmatched paths. 404 and 410 bodies are **never cached** (only 200s
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
4. `Dev` mode: emit the Vite client and the module script instead, as the layout does today.
5. Lenient mode (`hearth check`) resolves unknown entries to placeholders, as `asset` does.
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
  - `search.json`: serve from the voters mux in front of `Site`, or as `Options.Fallback`.
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
