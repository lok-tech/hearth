# Architecture

How the code is organised, how a page gets made, and the rules that keep static output and server
output identical. Read this before changing anything under `pkg/` or `internal/`. For product
decisions and status see [PLAN.md](PLAN.md); for running things see [DEVELOPMENT.md](DEVELOPMENT.md).

## The one idea

A page is rendered by **one engine**, `pkg/hearth`'s `Site`, no matter who asks:

- a visitor's request to a server on a cache miss,
- `hearth build` (prerender every page, export the files),
- `hearth check` (trial render), and
- `hearth dev` (a `Site` over the source tree, built with the dev overrides).

Nothing else renders. That is what makes a prebuilt file and a server-rendered page byte-identical.
Two tests protect it (see "Guard tests"). If you find yourself writing a second code path that builds a
`render.PageData` or calls the renderer, stop: add the behaviour to `Site` instead.

## Packages

Dependency direction: `cmd/hearth -> internal/{build,dev,starter,config} -> pkg/hearth -> internal core`.

| Package | Responsibility | May import |
|---|---|---|
| `cmd/hearth` | argument parsing, exit codes, `serve` loop | build, config, dev, starter |
| `internal/build` | `Run` (Vite, bundle copy, prerender via Site, sitemap/robots), `Check`, `planPages` | config, data, fsutil, manifest, route, i18n, `pkg/hearth` |
| `internal/dev` | `NewMock` (a Site per request with the dev overrides), `NewWatch`, `NewLive`, `NewPreview` (proxies) | config, route, cache (path mapping only), `pkg/hearth` |
| `internal/starter` | embedded starter (`template/`), `Init` | fsutil |
| `internal/config` | `hearth.config.json` schema and validation | cache (`ValidHost`) |
| **`pkg/hearth`** | **the only public package**: `Site`, `Options`, `Handler`, `Prerender`, `WarmSitemap`, `Export`, `Warm` | every core package below |
| `internal/render` | layout + templates + helpers, `PageData`, the `Templates` and `Assets` seams | route, manifest, i18n, data |
| `internal/cache` | the `Store` seam: `*Cache` (files) and `Nop` | none |
| `internal/manifest` | `manifest.json` and the three asset resolvers: `*Manifest`, `Placeholder`, `Vite` | none |
| `internal/route` | `pages/` file path to route, matching, `Page.MockFile` | none |
| `internal/i18n` | catalogs, plurals, `IsRTL`, **`URLs`** (language to URL mapping) | none |
| `internal/manifest` | `manifest.json`, asset and image lookup, `Picture`, `PictureSet` | none |
| `internal/cache` | flat page files, atomic writes through an optional temp dir, per-path invalidation, `RelPath`, `ValidHost` | none |
| `internal/data` | read `data/*.json` safely | none |
| `internal/fsutil` | `WriteFileAtomic` | none |
| `internal/sitemap` | sitemap XML with `lastmod`, `Split` and `Index` for the 50,000 URL / 50 MB limits | none |
| `internal/testutil` | shared test helpers (`WriteSite`, `BaseSite`, `Read`, `Get`) | none |
| `internal/archtest` | tests about code shape | none (parses imports) |

`example/` is a separate Go module that imports only `pkg/hearth`.

## Guard tests (`internal/archtest`, `internal/build/parity_test.go`)

1. **Only `pkg/hearth` imports `internal/render`.** Anything else that imports it fails the test. Reading
   `data/*.json` is not rendering, which is why it lives in `internal/data`.
2. **`pkg/hearth` must not import** `build`, `dev`, `starter`, `config` or `cmd`.
3. **Parity.** Build a site, serve the built `dist/templates` from an independently constructed `Site` (its own
   data handler) and require every built `index.html`, `404.html`, `sitemap.xml` and `robots.txt` to equal the server's answer.
   Runs in prefix and subdomain modes. A second test bakes an `ssr` page (with a custom func) through `Prerender` +
   `Export` and requires it to equal what a `Site` with the same handler serves.

Both rules were verified by breaking them on purpose. When you add a guard, do the same.

## A request through `Site` (`pkg/hearth/site.go`)

`ServeHTTP` answers `/healthz`, `/robots.txt`, `/sitemap.xml` (and `/sitemap-N.xml` parts), rejects non-GET/HEAD with 405, then calls
`resolve(r)`, which is also what `Prerender` calls:

1. `urls.Resolve(r.Host, r.URL.Path)` gives `(lang, neutralPath)`. Prefix mode strips `/<lang>`; host modes map
   the `Host` to a language (case-insensitive, port ignored). Unknown host: `unmatched` 404.
2. `cache.RelPath("", neutralPath)` rejects anything that cannot be a safe file name (`..`, `.`, backslash, NUL).
3. A trailing slash (except `/`) is a 301 to the same path without it.
4. `store.Get(host, r.URL.Path)`; with the page cache off (`HEARTH_CACHE=off`, `cache.Nop`) it always misses. A file is a hit (the cache key is the *requested* path, so
   prefix mode includes `/he/...`; host modes use the configured host directory and the neutral path).
5. Otherwise `singleflight` on `host|path`, then `render`: match route, take a semaphore slot
   (`MaxConcurrent`, with `RenderTimeout`), call the page's handler (every `[param]` page, and every `ssr` page; a parameterless page that is not in `ssr` has none), build `PageData` with
   `pageData(...)`, render, and write the file, unless the page was invalidated after the render began
   (`Store.Gen` / `Put` returning `cache.ErrStale`: a render that read old data cannot put it back). Only 200s are written.
6. Handler outcomes: `ErrNotFound` 404 (message kept in `note`, shown only with `HEARTH_DIAGNOSTICS=1`), `ErrGone` 410,
   `*RedirectError` (301, 302, 307, 308 only), anything else is a 500 and is not cached.
7. A path no page claims (no page, unknown host, unsafe path) is a 404 like any other. `Site` never serves or proxies
   it elsewhere; `Site.Matches(r)` tells a mux, or `hearth dev`, which requests are not Site's, so they can send
   those on (`search.json`, the Vite dev server).
8. Any 404 or 410 that is not a redirect goes through `writeError`: `errorBody(lang, status)`
   renders `pages/404.html` / `410.html` (not routes: `route.Table.ErrorPage`) with `Status` and `NoIndex` set,
   `no-store`, never cached; plain text when the template is missing, the host is unknown or the render fails.
   `hearth.Build` writes `errorBody(lang, 404)` as `404.html` through the same function.

`pageData(lang, neutral, params, data, meta)` is the **single place** that decides what a template sees:
`Lang`, `Dir` (`i18n.IsRTL`), `Params`, `Data`, `Canonical` (`meta.Canonical`, else `urls.Abs` when there is an
origin), `Alternates` (`urls.Alternates`, nil for one language or no `BaseURL`), `Prefix`, `Status` (200), `NoIndex` (`meta.NoIndex`), `HMR` (assets come from a Vite dev server).
The renderer folds a page's `{{define "robots"}}noindex{{end}}` into `NoIndex`, so the layout emits one tag either way.

### Language and URL rules (`internal/i18n/urls.go`)

`URLs{Mode, Hosts, Default, BaseURL, Langs}` is shared by the server and the build.

- `Location(lang)` returns `(host dir, prefix)`: host modes give `(Hosts[lang], "")`; prefix mode gives
  `("", "")` for the default language and `("", "/<lang>")` otherwise.
- `Path(lang, "/")` is `/he` (no trailing slash), `Path("he", "/a")` is `/he/a`.
- `Abs` = `Origin(lang) + Path`. `Origin` is `BaseURL`, or `scheme://Hosts[lang]` in host modes.
- `Alternates` is nil when there is one language, or no `BaseURL` outside host modes (hreflang must be absolute).

### Cache (`internal/cache`)

```
<CacheDir>/<path>/index.html
<CacheDir>/<host>/<path>/index.html     host modes only
```

- The cache is flat and has no versions. A full invalidation is a deploy that replaces `CacheDir` (the reference
  deploy runs `rsync --delete` into it); `Site.Invalidate` drops single pages. Nothing in the Go server decides
  that a new bundle should empty the cache.
- `Invalidate` removes `<path>/index.html` only, so it cannot touch other files in a directory shared with
  prebuilt output (`404.html`, `sitemap.xml`, `assets/`). It bumps the page's generation first, under the lock
  `WriteGen` takes, so a render that began earlier skips its write. Generations live in 256 hashed slots, so
  memory does not grow with the number of pages; a shared slot can only skip a write, never keep a stale page.
- Writes go through a temp file and a rename. With `Options.TmpDir` the temp file is created there (it must be on
  the same mount as `CacheDir`); `New` proves that with a probe rename and fails at startup if it cannot, and
  removes temp files older than ten minutes left by a crash. Without `TmpDir` the temp file is a `.tmp-*`
  dotfile next to the page, which `Export` and the web server configs ignore.
- Hosts are validated (`ValidHost`: lowercase letters, digits, `-.:`).

## A build (`internal/build/build.go`, `Run`)

In order: resolve and sanity-check the output dir (refuses the site dir, its parent, `/`, or any ancestor of the
site) -> wipe `dist/public` and `dist/templates` -> copy `public/` -> run Vite if configured (`runVite`) ->
stamp `manifest.Version` (hash of the site's file names, sizes and mtimes) and write `dist/manifest.json` ->
copy `layouts partials pages(*.html only) locales data` to `dist/templates` plus `manifest.json` ->
`planPages` -> `newSite` over `dist/templates` with a throwaway cache -> `hearth.Build` (`pkg/hearth/build.go`):
`Prerender` of every parameterless page that is not in `ssr` plus the `SitemapURLs`/`SitemapEntries` (the collection pages) -> fail
on any failure (first error plus a count) -> `Export` into `dist/public` -> if `baseURL` is set, save the `Site`'s own
answers for `/sitemap.xml` and `/robots.txt` once per origin (a root, or one directory per language host; an existing
`robots.txt` from `public/` wins). The `ssr` list is validated against the routes before anything is written.

- `planPages` lists static pages (not `ssr` pages: their handlers answer, so no file may exist) and each entry of every configured collection. Collection values must be
  non-empty, unique, free of `/ \ ? # % NUL`, and not `.` or `..`. Each collection becomes a `hearth.Handler`
  over the JSON entries (the build's stand-in for a database).
- `Check` uses the same `planPages` and `newSite` (with the placeholder asset resolver) over the source tree, and adds checks
  on catalogs, data JSON and `*.mock.json` shapes. It collects every problem instead of stopping at the first.

### Vite contract

`runVite` runs `vite.command` (an argv, no shell) in the site dir with `--outDir <dist/public> --emptyOutDir
--manifest` (+ `--base <assetBase>/` when `assetBase` is set). It reads `public/.vite/manifest.json`:

- `assets["src/entry.ts"] = "/assets/entry-<hash>.js"`; an entry's first CSS file is `assets["src/entry.ts.css"]`
  (kept so `{{asset}}` is unchanged; it is not the whole story, use `entries`).
- `entries["src/entry.ts"] = {js, css[], preload[]}` for every manifest key with `isEntry`: `js` is the script
  (empty for a CSS-only entry), `css` is every stylesheet in cascade order (statically imported chunks first,
  depth first, then the entry's own), `preload` the statically imported chunks. Resolution walks the whole Vite
  manifest, is cycle-safe, lists each file once and is stable (sorted keys, first-seen order). Dynamic imports are
  not followed. An `imports` key missing from the manifest fails the build.
- the image plugin leaves `public/.hearth/images.json`: `name -> [{url,type,width,height}]`, fallback **last**.
- `assetBase` is prefixed onto every URL. Then `.vite/` and `.hearth/` are deleted from `public/`.

### `manifest.json` and bundle format

`{ "format": 3, "assets": {...}, "entries": {...}, "images": {...}, "version": "<build id>", "ssr": ["/candidates"] }` (`ssr` lists the parameterless pages served by handlers; format 2 added it, format 3 added `entries` for `{{entry}}`). `manifest.BundleFormat` must match
or `Parse` fails loudly. Bump it when the bundle layout or manifest meaning changes. The Vite
dev URL and placeholder behaviour are not manifest data: they are the `Vite` and `Placeholder` resolvers in
`internal/manifest/resolvers.go`, chosen by `HEARTH_ASSETS`.

## Templates (`internal/render`)

- Root template `layouts/base.html` (`render.LayoutName`) defines blocks `title description head body`. A page
  file overrides them with `{{define}}`. All `partials/*.html` are parsed into every page's set. A page is parsed
  once per `(file, lang)` and cached (`HEARTH_TEMPLATES=request` parses on every render instead); `about.he.html` replaces `about.html` for `he`.
- `missingkey=error`: a missing data key or translation fails the render.
- Helpers: `t`, `plural`, `asset`, `entry`, `picture(name, alt [, sizes] [, "eager"])`, `pictureSet(alt, sizes, media, name, ..., "", fallback [, "eager"])`, `json`, `data`, plus `Options.Funcs`. `PageData.Link(path)` is a method.
- `Options.Funcs` is validated in `render.New` (`checkFuncs`): no shadowing of a helper or Go builtin, valid name, a
  func with one result or result plus error. The CLI cannot run your Go code, so config `funcs` names become stubs
  (`internal/mock.Stubs`) that parse but fail when called; `Check` reports such pages with `build.NeedsProgram`.
- `{{entry "src/site.ts"}}` (`Manifest.Entry`, returns `template.HTML`) emits a `<link rel="stylesheet">` per CSS file,
  a `<link rel="modulepreload">` per imported chunk and the `<script type="module">`, all from `entries`, escaped and
  with `assetBase` already prefixed. With the `Vite` resolver it emits the Vite client and the module
  script instead, so a layout needs no branch. The `Placeholder` resolver (`check`) resolves an unknown entry to a bare
  script placeholder; anywhere else an unknown entry fails the render. Several entries can be used on one page
  (a layout entry plus a page entry from a block): a CSS file shared by two calls is not de-duplicated across calls.
- `{{if .HMR}}` is available for layouts that still want to branch on the dev server (`.Dev` was removed).
- `html/template` escapes by context; there is deliberately no helper that returns raw HTML from user data.

## `hearth dev`

- Who it is for: a site with **no Go server of its own** (templates and data, built to static files). A project
  with a Go server runs that server and uses the `hearthDev()` Vite plugin instead (see "Layers and overrides").
- Every mode builds its `Site` with `Overrides` (cache off, diagnostics on, placeholder or Vite assets) and
  `IgnoreEnv: true`, so a `HEARTH_*` variable in a shell cannot change it. What no page claims goes to the mode's
  fallback, decided by `Site.Matches`.
- `mock`: `dev.NewMock` builds a **new** `Site` on every request (bundle = source tree) so edits show
  immediately. Dynamic pages get a handler reading `pages/<path>.mock.json` (`{ "<param value>": data, "*": data }`); `ssr` pages read the whole file as their data (`internal/mock`, shared with `check`).
  The fallback is the Vite proxy; `HEARTH_ASSETS=vite:<url>` semantics (the `Vite` resolver) point `{{asset}}` at it.
- `live`: reverse proxy to `dev.server`. `preview`: a file from `dist/public` (mapped with `cache.RelPath`), else
  the same proxy.
- `hearth dev` starts Vite itself in **watch mode** (`dev.NewWatch`): `vite build --watch` into `node_modules/.cache/hearth-dev`, one address for pages and assets, the latest Vite manifest overlaid onto the source bundle per request (last good one kept while a rebuild empties the directory), `.vite`/`.hearth` never served. No HMR, but **live reload** (`internal/dev/reload.go`): a poller fingerprints `pages layouts partials locales data public` plus the Vite manifest's mtime (`src/` is left out so the reload follows the rebuild, never precedes it), and after a 150 ms settle it signals browsers over server-sent events at `/__hearth/reload`; a small script is injected before `</body>` of HTML responses only. Setting `vite.devURL` selects the older proxy mode instead (run `vite` yourself; `{{asset}}` points at it and templates see `.HMR`).
- `dev.proxy` (`internal/dev/proxy.go`, validated in `config.ValidateProxy`, so `check` and `config.Load` reject bad entries): `withProxy` wraps the whole dev handler in both modes, outside live reload, so a prefix is checked before page routes, the Vite fallback and reload injection. Longest prefix first, whole path segments, `httputil.ReverseProxy` with `Rewrite` (`SetURL` sets the backend `Host`, `SetXForwarded`). `preview`, `-live` and production never read it.

## Layers and overrides

Design and decisions: [LAYERS.md](LAYERS.md). The short version, for someone changing `pkg/hearth`:

- A `Site` is four seams with production defaults: **templates** (`render.Templates`: parse once, or on every
  request), **render** (`Site.resolve`/`render`, no mode), **store** (`cache.Store`: files, or `Nop`) and **assets**
  (`render.Assets`: `*Manifest`, `Placeholder`, `Vite`). `Site` itself is a thin `http.Handler` over them.
- There is no "dev mode". `Options.Overrides` and the `HEARTH_*` variables (`pkg/hearth/overrides.go`) pick another
  implementation per seam, and the environment wins over code. `New` logs one `WARN` when the environment changed
  anything. Tools set `IgnoreEnv`.
- `HEARTH_BUNDLE` replaces `Options.Bundle` with `os.DirFS(dir)` in `New`, before anything reads the bundle. With no
  `manifest.json` (a source tree, which needs `placeholder` or `vite:` assets) `configSSR` reads the `ssr` list from
  `hearth.config.json` at the bundle root, so the server does not have to; a bundle that has a manifest ignores it.
- Do not add a check like `if devMode` to `Site`. Add or select an implementation of a seam.
- Serving and proxying in development are not Go package concerns: the `hearthDev()` Vite plugin
  (`npm/hearth/vite.js`) reloads on template edits and proxies to the importer's server; `hearth dev` does the same
  job for sites without one.

## Files that look odd on purpose

- `internal/starter/template/dot_gitignore`: stored with a `dot_` prefix and renamed on write, because package
  managers rewrite or drop `.gitignore` files. `//go:embed all:template` is needed for dotfiles.
- `internal/starter/template/` is data, not Go: the archtest skips directories named `template`.
- `pkg/hearth/doc.go` is the public documentation; keep its example in step with the API.
- `{{name}}` in the starter's `package.json` is replaced on `init`; other `{{...}}` text in starter files is
  template syntax for the user's site and must not be touched.
