# Architecture

How the code is organised, how a page gets made, and the rules that keep static output and server
output identical. Read this before changing anything under `pkg/` or `internal/`. For product
decisions and status see [PLAN.md](PLAN.md); for running things see [DEVELOPMENT.md](DEVELOPMENT.md).

## The one idea

A page is rendered by **one engine**, `pkg/hearth`'s `Site`, no matter who asks:

- a visitor's request to a server on a cache miss,
- `hearth build` (prerender every page, export the files),
- `hearth check` (trial render), and
- `hearth dev` (a dev-mode `Site` over the source tree).

Nothing else renders. That is what makes a prebuilt file and a server-rendered page byte-identical.
Two tests protect it (see "Guard tests"). If you find yourself writing a second code path that builds a
`render.PageData` or calls the renderer, stop: add the behaviour to `Site` instead.

## Packages

Dependency direction: `cmd/hearth -> internal/{build,dev,starter,config} -> pkg/hearth -> internal core`.

| Package | Responsibility | May import |
|---|---|---|
| `cmd/hearth` | argument parsing, exit codes, `serve` loop | build, config, dev, starter |
| `internal/build` | `Run` (Vite, bundle copy, prerender via Site, sitemap/robots), `Check`, `planPages` | config, data, fsutil, manifest, route, i18n, `pkg/hearth` |
| `internal/dev` | `NewMock` (dev-mode Site per request), `NewLive`, `NewPreview` (proxies) | config, route, cache (path mapping only), `pkg/hearth` |
| `internal/starter` | embedded starter (`template/`), `Init` | fsutil |
| `internal/config` | `hearth.config.json` schema and validation | cache (`ValidHost`) |
| **`pkg/hearth`** | **the only public package**: `Site`, `Options`, `Handler`, `Prerender`, `Export`, `Warm`, `Poll` | every core package below |
| `internal/render` | layout + templates + helpers, `PageData` | route, manifest, i18n, data |
| `internal/route` | `pages/` file path to route, matching, `Page.MockFile` | none |
| `internal/i18n` | catalogs, plurals, `IsRTL`, **`URLs`** (language to URL mapping) | none |
| `internal/manifest` | `manifest.json`, asset and image lookup, `Picture` | none |
| `internal/cache` | versioned dirs, atomic `current` symlink, `RelPath`, `ValidHost`, `ValidVersion` | none |
| `internal/data` | read `data/*.json` safely | none |
| `internal/fsutil` | `WriteFileAtomic` | none |
| `internal/sitemap` | sitemap XML | none |
| `internal/testutil` | shared test helpers (`WriteSite`, `BaseSite`, `Read`, `Get`) | none |
| `internal/archtest` | tests about code shape | none (parses imports) |

`example/` is a separate Go module that imports only `pkg/hearth`.

## Guard tests (`internal/archtest`, `internal/build/parity_test.go`)

1. **Only `pkg/hearth` imports `internal/render`.** Anything else that imports it fails the test. Reading
   `data/*.json` is not rendering, which is why it lives in `internal/data`.
2. **`pkg/hearth` must not import** `build`, `dev`, `starter`, `config` or `cmd`.
3. **Parity.** Build a site, serve the built `dist/templates` from an independently constructed `Site` (its own
   data handler) and require every built `index.html`, `sitemap.xml` and `robots.txt` to equal the server's answer.
   Runs in prefix and subdomain modes.

Both rules were verified by breaking them on purpose. When you add a guard, do the same.

## A request through `Site` (`pkg/hearth/site.go`)

`ServeHTTP` answers `/healthz`, `/robots.txt`, `/sitemap.xml`, rejects non-GET/HEAD with 405, then calls
`resolve(r)`, which is also what `Prerender` calls:

1. `urls.Resolve(r.Host, r.URL.Path)` gives `(lang, neutralPath)`. Prefix mode strips `/<lang>`; host modes map
   the `Host` to a language (case-insensitive, port ignored). Unknown host: `unmatched` 404.
2. `cache.RelPath("", neutralPath)` rejects anything that cannot be a safe file name (`..`, `.`, backslash, NUL).
3. A trailing slash (except `/`) is a 301 to the same path without it.
4. Unless `Dev`: read `cache.Path(host, r.URL.Path)`. A file is a hit (the cache key is the *requested* path, so
   prefix mode includes `/he/...`; host modes use the configured host directory and the neutral path).
5. Otherwise `singleflight` on `version|host|path`, then `render`: match route, take a semaphore slot
   (`MaxConcurrent`, with `RenderTimeout`), call the handler for a `[param]` page, build `PageData` with
   `pageData(...)`, render, and write the file **under the version that was active when the render began**
   (a flip during a render cannot leak stale content into the new version). Only 200s are written.
6. Handler outcomes: `ErrNotFound` 404 (message kept in `note`, shown only in Dev), `ErrGone` 410,
   `*RedirectError` (301, 302, 307, 308 only), anything else is a 500 and is not cached.
7. `rendered.unmatched` (no page, unknown host, unsafe path) goes to `Options.Fallback` when set; a matched
   page that is not found does not.

`pageData(lang, neutral, params, data, meta)` is the **single place** that decides what a template sees:
`Lang`, `Dir` (`i18n.IsRTL`), `Params`, `Data`, `Canonical` (`meta.Canonical`, else `urls.Abs` when there is an
origin), `Alternates` (`urls.Alternates`, nil for one language or no `BaseURL`), `Prefix`, `Dev`.

### Language and URL rules (`internal/i18n/urls.go`)

`URLs{Mode, Hosts, Default, BaseURL, Langs}` is shared by the server and the build.

- `Location(lang)` returns `(host dir, prefix)`: host modes give `(Hosts[lang], "")`; prefix mode gives
  `("", "")` for the default language and `("", "/<lang>")` otherwise.
- `Path(lang, "/")` is `/he` (no trailing slash), `Path("he", "/a")` is `/he/a`.
- `Abs` = `Origin(lang) + Path`. `Origin` is `BaseURL`, or `scheme://Hosts[lang]` in host modes.
- `Alternates` is nil when there is one language, or no `BaseURL` outside host modes (hreflang must be absolute).

### Cache (`internal/cache`)

```
<CacheDir>/current -> versions/<bundleID>.<n>          relative symlink, replaced atomically
<CacheDir>/versions/<bundleID>.<n>/<path>/index.html
<CacheDir>/versions/<bundleID>.<n>/<host>/<path>/index.html     host modes only
```

- `New` makes the root and a `v0` version if none exists. `Site.New` then moves to `<manifest.Version>.0` unless
  the current version already starts with `<manifest.Version>.`: **a new bundle starts an empty cache**.
- `Site.SetVersion(v)` flips to `<bundleID>.<v>` and prunes to `KeepVersions` (default 2). Versions and hosts are
  validated (`ValidVersion`: letters, digits, `-_.`; `ValidHost`: lowercase letters, digits, `-.:`).
- Writes go through a `.tmp-*` file in the page's directory and a rename. `Export` and the web server configs
  both ignore dotfiles because of this.
- Known minor issue: a render that began before a flip and finishes after the prune can recreate its version
  directory (see PLAN open items).

## A build (`internal/build/build.go`, `Run`)

In order: resolve and sanity-check the output dir (refuses the site dir, its parent, `/`, or any ancestor of the
site) -> wipe `dist/public` and `dist/templates` -> copy `public/` -> run Vite if configured (`runVite`) ->
stamp `manifest.Version` (hash of the site's file names, sizes and mtimes) and write `dist/manifest.json` ->
copy `layouts partials pages(*.html only) locales data` to `dist/templates` plus `manifest.json` ->
`planPages` -> `newSite` over `dist/templates` with a throwaway cache -> `Prerender` -> fail on any failure
(first error plus a count) -> `Export` into `dist/public` -> if `baseURL` is set, save the `Site`'s own answers
for `/sitemap.xml` and `/robots.txt` once per origin (a root, or one directory per language host; an existing
`robots.txt` from `public/` wins).

- `planPages` lists static pages and each entry of every configured collection. Collection values must be
  non-empty, unique, free of `/ \ ? # % NUL`, and not `.` or `..`. Each collection becomes a `hearth.Handler`
  over the JSON entries (the build's stand-in for a database).
- `Check` uses the same `planPages` and `newSite` (with `LenientAssets`) over the source tree, and adds checks
  on catalogs, data JSON and `*.mock.json` shapes. It collects every problem instead of stopping at the first.

### Vite contract

`runVite` runs `vite.command` (an argv, no shell) in the site dir with `--outDir <dist/public> --emptyOutDir
--manifest` (+ `--base <assetBase>/` when `assetBase` is set). It reads `public/.vite/manifest.json`:

- `assets["src/entry.ts"] = "/assets/entry-<hash>.js"`; an entry's first CSS file is `assets["src/entry.ts.css"]`.
- the image plugin leaves `public/.hearth/images.json`: `name -> [{url,type,width,height}]`, fallback **last**.
- `assetBase` is prefixed onto every URL. Then `.vite/` and `.hearth/` are deleted from `public/`.

### `manifest.json` and bundle format

`{ "format": 1, "assets": {...}, "images": {...}, "version": "<build id>" }`. `manifest.BundleFormat` must match
or `Parse` fails loudly. Bump it when the bundle layout or manifest meaning changes. `Manifest.DevBase` (Vite dev
URL) and `Manifest.Lenient` (placeholder for unknown assets) are runtime-only (`json:"-"`).

## Templates (`internal/render`)

- Root template `layouts/base.html` (`render.LayoutName`) defines blocks `title description head body`. A page
  file overrides them with `{{define}}`. All `partials/*.html` are parsed into every page's set. A page is parsed
  once per `(file, lang)` and cached; `about.he.html` replaces `about.html` for `he`.
- `missingkey=error`: a missing data key or translation fails the render.
- Helpers: `t`, `plural`, `asset`, `picture(name, alt)`, `json`, `data`. `PageData.Link(path)` is a method.
- `{{if .Dev}}` is how the layout switches between the Vite client (dev) and the hashed CSS link (build).
- `html/template` escapes by context; there is deliberately no helper that returns raw HTML from user data.

## `hearth dev`

- `mock`: `dev.NewMock` builds a **new** `Site` on every request (`Dev: true`, bundle = source tree) so edits show
  immediately. Dynamic pages get a handler reading `pages/<path>.mock.json` (`{ "<param value>": data, "*": data }`).
  `Fallback` is the Vite proxy; `DevAssetURL` is the Vite URL.
- `live`: reverse proxy to `dev.server`. `preview`: a file from `dist/public` (mapped with `cache.RelPath`), else
  the same proxy.
- Vite is **not** started by the CLI; run it separately (`npm run vite`).

## Files that look odd on purpose

- `internal/starter/template/dot_gitignore`: stored with a `dot_` prefix and renamed on write, because package
  managers rewrite or drop `.gitignore` files. `//go:embed all:template` is needed for dotfiles.
- `internal/starter/template/` is data, not Go: the archtest skips directories named `template`.
- `pkg/hearth/doc.go` is the public documentation; keep its example in step with the API.
- `{{name}}` in the starter's `package.json` is replaced on `init`; other `{{...}}` text in starter files is
  template syntax for the user's site and must not be touched.
