# Design: layers and `HEARTH_*` overrides (no dev mode in the Go package)

Status: **implemented** (phases 1 to 5 below, all done 2026-11-24). Written after a site built on Hearth ended up with two server
binaries (`main.go` for production, `dev.go` for development) because `pkg/hearth` only offered a single
`Options.Dev` switch and `hearth dev` ran its own mock server instead of the importer's real one.

## Goals

1. An importer writes **one** server. It calls `hearth.New` the same way in every environment.
2. The defaults are the production setup. Nothing has to be configured to get it.
3. A developer changes behaviour with `HEARTH_*` environment variables, set by whatever launches the server
   (an npm script, a Makefile, `air`). The server's code does not change.
4. The Go package serves pages and nothing else. Static serving in production is the web server's job (Caddy,
   nginx). Serving assets and proxying in development is Vite's job.
5. Out of scope: how the importer's Go server is started or reloaded, and what `npm run` wraps.

## What is wrong today

`Options.Dev` is one boolean that switches on five unrelated behaviours (`pkg/hearth/site.go`):

| Behaviour | Code |
|---|---|
| skip the disk cache | `if !s.opt.Dev` around cache read and write |
| tolerate a missing `manifest.json` and unknown assets | `man.Lenient` |
| resolve `{{asset}}` to the Vite dev server | `DevAssetURL`, `.Dev` in templates |
| `Cache-Control: no-store` | `CacheControl` default |
| explain a 404 in the response | `res.note` |

`Options.Fallback` and `internal/dev` (`NewMock`, `withProxy`, the Vite proxy) put serving and proxying inside
the Go side. Because `NewMock` stubs the funcs (`mock.Stubs`) and builds its own `Site` per request, the importer's
real handlers, database and `WatchData` never run in development. The importer re-creates them in a second
entry point. That is the duplication to remove.

## The layers

Each layer has one job, one seam (an interface) and a production default. A layer never reads another layer's
mode.

| # | Layer | Job | Seam | Default | Override |
|---|---|---|---|---|---|
| 1 | Templates | load and parse the bundle | `Templates` | parse once at `New` | `HEARTH_TEMPLATES=request` re-reads and re-parses on every request |
| 2 | Render | route, data, i18n, funcs → status, headers, body | `Site.Render` | n/a | none: same output in every environment |
| 3 | Store | keep rendered pages | `Store` | disk files under `CacheDir` | `HEARTH_CACHE=off` |
| seam | Assets | `{{asset}}`, `{{entry}}`, images | `AssetResolver` | the bundle's `manifest.json` | `HEARTH_ASSETS=vite:<url>` or `placeholder` |

`Site` stays an `http.Handler`, because a cache miss needs something that turns a request into `Render`, then
`Store.Put`, then a response. It is a thin adapter: hit → serve from the store; miss → render, store, respond.
It has no `Fallback`, no proxy and no mode.

### 1. Templates

```go
type Templates interface {
    // Get returns the parsed templates. Cached implementations return the same value every call;
    // the request implementation parses the bundle again and returns a fresh value.
    Get() (*Parsed, error)
}
```

- `once` (default): parsed in `New`; a parse error fails `New`, as today.
- `request`: no file watching and no change detection. The bundle is read again on each call, so an edit shows
  on the next request. This works when `Options.Bundle` is an `os.DirFS` on the source tree. With an `embed.FS`
  the bytes are frozen into the binary, so `request` re-parses the same files; `New` logs one warning when
  `HEARTH_TEMPLATES=request` is set with a bundle that is not an `os.DirFS`.
- A parse error in `request` mode fails that request with a 500 and the parse message in the log and the body
  (only under `HEARTH_DIAGNOSTICS=1`), and the next request tries again.

### 2. Render

`Render(ctx, r) (Page, error)` is the existing render path (route lookup, handler, i18n, template execution,
head tags, sitemap data) with the mode checks removed. It takes the template set from layer 1, asset helpers from
the `AssetResolver`, and does not know about the store. `hearth build` and `hearth check` call the same function
(the single-engine rule in [ARCHITECTURE.md](ARCHITECTURE.md) stays).

### 3. Store

```go
type Store interface {
    Get(key Key) ([]byte, bool)
    Put(key Key, gen uint64, body []byte) (stored bool, err error) // stored=false: invalidated mid-render
    Invalidate(keys ...Key)
    Generation(key Key) uint64                                      // taken before rendering
}
```

- `DiskStore` is today's flat cache (`<CacheDir>/<host>/<path>/index.html`, temp files in `TmpDir`, rename into
  place, generation counter that skips a stale write). It stays the default and the only one the web server reads.
- `NopStore` (`HEARTH_CACHE=off`): `Get` misses, `Put` reports `stored=false`, `Invalidate` does nothing. The
  response header becomes `Cache-Control: no-store` because nothing is cached, not because of a mode.
- `hearth build` uses a store that writes into `dist/public`, so building and serving share one render path.
- `Invalidate`, `WatchData` and the sitemap warm-up belong to this layer. With `NopStore` they are no-ops, so
  the importer's `WatchData` call can stay in its `main.go` unchanged.

### Seam: assets

```go
type AssetResolver interface {
    Asset(name string) (string, error)
    Entry(name string) (template.HTML, error) // CSS links, modulepreload, script tag, or the Vite client
    HMR() bool                                // true when assets come from a Vite dev server
}
```

| Resolver | Selected by | Behaviour |
|---|---|---|
| `Manifest` | default | reads `manifest.json`; an unknown asset is an error |
| `Vite(url)` | `HEARTH_ASSETS=vite:http://localhost:5173` | `Asset` returns `url + "/" + name`; `Entry` emits `<url>/@vite/client` and the module script |
| `Placeholder` | `HEARTH_ASSETS=placeholder` (also used by `hearth check`) | unknown assets and images resolve to placeholders, so a render never fails for lack of a Vite build |

The template variable `.Dev` goes away. A layout that needs to branch asks the resolver: `{{if .HMR}}`.

## Configuration

`hearth.New(Options{...})` builds the production setup from `Options`. Then `HEARTH_*` variables override it.
**An environment variable always wins over a value in code.** That is what lets a developer change behaviour
without touching the server; the cost is that code cannot pin a setting. The fields that have an override
(`Templates`, `Store`, `Assets`) are therefore documented as defaults.

```
HEARTH_TEMPLATES   once | request
HEARTH_CACHE       disk | off
HEARTH_ASSETS      manifest | placeholder | vite:<url>
HEARTH_DIAGNOSTICS 0 | 1     404 reasons and template errors in responses; default 0
HEARTH_BUNDLE      <dir>     read the templates from this directory instead of Options.Bundle (phase 6)
HEARTH_ENV         dev       preset: request + off + (assets unchanged) + diagnostics
```

Rules:

- Parsing is strict about the layer variables: an invalid value for `HEARTH_TEMPLATES`, `HEARTH_CACHE`,
  `HEARTH_ASSETS`, `HEARTH_DIAGNOSTICS` or `HEARTH_ENV` makes `New` return an error naming it, so a typo cannot
  silently leave production behaviour on. Other `HEARTH_*` names are **not** rejected: deployments already use
  `HEARTH_CACHE_DIR`, `HEARTH_TMP_DIR` and `HEARTH_BINARY_PATH` for their own settings (`docs/DEPLOY.md`).
- When any override is active, `New` logs one `WARN` line listing them (`hearth: overrides active:
  cache=off templates=request`). A stray variable on a production host is then visible in the first log line.
- `HEARTH_ENV=dev` is a preset for convenience; individual variables are applied after it and win.
- The variables are read in one place (`hearth.FromEnv`, called by `New`). `Options` gains no `Dev`,
  `DevAssetURL`, `LenientAssets` or `Fallback` field.

## What this removes and replaces

| Today | After |
|---|---|
| `Options.Dev` | `Templates` + `Store` + `AssetResolver` + `HEARTH_DIAGNOSTICS` |
| `Options.DevAssetURL`, `Manifest.DevBase` | `Vite(url)` resolver |
| `Options.LenientAssets`, `Manifest.Lenient` | `Placeholder` resolver |
| `Options.Fallback` | removed; Vite proxies, not the Go package |
| `.Dev` in templates | `.HMR` |
| `res.note` shown "in Dev" | `HEARTH_DIAGNOSTICS=1` |
| `CacheControl` `no-store` when `Dev` | `no-store` when the store is `NopStore` |
| `internal/dev` `NewMock`, `withProxy`, Vite proxy | stays, as the CLI's own dev server for static-only sites; it builds its `Site` from the layers (see "`hearth dev` for static-only sites") and leaves `pkg/hearth` |

`pkg/hearth` keeps its import rule: it imports none of `build`, `dev`, `starter`, `config` or `cmd`.

## `hearth dev` for static-only sites

Not every project has a Go server: a site can be templates and data files only, built to static files. Such
a project has no server of its own to run, so `hearth dev` stays as its development server (decided
2026-11-24). It keeps its modes: `mock` (templates over `pages/*.mock.json`), `watch` (Vite in watch mode,
live reload), `live` (proxy to a running server) and `preview` (serve `dist/public`).

What changes is how it is built. It lives in `internal/dev` and is a **client** of the layers, not a special
case inside them:

- It configures the layers the way a developer would with `HEARTH_*`: `Templates=request`, `Store=off`,
  `Assets=vite:<url>` (or the watch-mode bundle). `hearth.Overrides` is the struct `FromEnv` fills; the CLI
  fills the same struct directly, so there is one code path and the env semantics are not duplicated.
- `Options.Fallback` goes away. In its place `Site.Matches(r *http.Request) bool` reports whether a page,
  handler or built-in route (`/healthz`, `/robots.txt`, sitemaps) answers the request. `internal/dev` decides:
  `Matches` → `Site`, otherwise its Vite or static fallback. Serving and proxying stay out of `pkg/hearth`.
- `dev.proxy` and live reload stay in `internal/dev` for this mode. For a project with a Go server, the
  `hearthDev()` Vite plugin below does the same job and `hearth dev` is not used.

Which one a project uses is a property of the project: **a Go server → `hearthDev()` plugin and your own
process; no Go server → `hearth dev`.** Both run the same render path.

## The Vite plugin: `hearth/vite` gets a dev half

The existing `hearthImages()` plugin (build only) stays. A new `hearthDev()` plugin covers the npm side of the
loop and is the only place that serves, proxies or reloads in development. It does **not** start or restart the
Go server; whoever launches it owns that.

```js
// vite.config.js
import { hearthDev, hearthImages } from "@lok-tech/hearth/vite";
export default { plugins: [hearthDev(), hearthImages()] };
```

```ts
interface HearthDevOptions {
  /** The importer's Go server. Default: dev.server from hearth.config.json, else http://127.0.0.1:8080. */
  server?: string;
  /** Folders watched for template edits, relative to the Vite root. Default: pages layouts partials locales data public. */
  watch?: string[];
  /** Path prefixes forwarded to a backend instead of the Go server. Default: dev.proxy from hearth.config.json. */
  proxy?: Record<string, string>;
}
```

What it does (`apply: "serve"`):

1. **Template reload.** Adds `watch` folders to `server.watcher` (they are outside Vite's module graph) and sends
   `{ type: "full-reload" }` over Vite's websocket when an `.html`, `.json` or locale file changes. The page
   already has the websocket because `{{entry}}` injects `/@vite/client` under `HEARTH_ASSETS=vite:<url>`. Chain:
   save → Vite watcher → reload → browser requests the page → Go server (`HEARTH_TEMPLATES=request`) renders the
   new template.
2. **Backend proxy.** Turns `proxy` (default `dev.proxy`) into Vite's `server.proxy`, so `/api` and `/s` reach
   their backend through Vite.
3. **Page proxy.** Returns a post-middleware from `configureServer`, so it runs after Vite's own handlers. A request
   Vite did not answer (not a module, not a file, not an asset) is forwarded to the Go server with `Host` and
   `X-Forwarded-*` set. Pages therefore load from the Vite origin, and the importer's server never serves assets.
4. **Config in one place.** It reads `hearth.config.json` (found by walking up from the Vite root), so a project
   does not repeat paths in `vite.config.js`. Options override the file.

It sets no environment variables for the Go server (it does not start it). The importer's script sets
`HEARTH_ENV=dev HEARTH_ASSETS=vite:http://localhost:5173` where it starts the server; `hearth/vite` documents the
pair.

## Phases

1. **Layers without behaviour change. Done 2026-11-24.** `Options.Dev` now only selects implementations, and
   every test, the parity test included, stays green:
   - `cache.Store` (`internal/cache`): `*Cache` is the disk store, `cache.Nop` keeps nothing and is what `Dev`
     selects. `Put` reports a skipped write as `cache.ErrStale`, which `Site` counts as `StaleSkips`.
   - `render.Templates`: the parse-once-and-keep logic is `onceTemplates`; `Renderer.parse` reads the set.
     The `request` implementation arrives in phase 2.
   - `render.Assets` (the design's `AssetResolver`): `*manifest.Manifest` implements it and gains `HMR()`.
     Splitting it into `Manifest`, `Vite` and `Placeholder` resolvers arrives with phase 3, when `Lenient`
     and `DevBase` go away.
   The interfaces are internal for now; the public names are fixed when `FromEnv` exposes them in phase 2.
2. **Env overrides. Done 2026-11-24.** `pkg/hearth/overrides.go` and `overrides_test.go`:
   - `Overrides` (the public struct), `OverridesFromEnv`, and `Options.Overrides` for code that sets it; the
     environment is applied on top and wins. An explicit override also beats the deprecated `Options.Dev`.
   - `HEARTH_TEMPLATES=request` (`render.Options.Reparse`, no change detection), `HEARTH_CACHE=off` (`cache.Nop`,
     `Cache-Control: no-store`), `HEARTH_ASSETS=manifest|placeholder|vite:<url>`, `HEARTH_DIAGNOSTICS=1` (404
     reasons and render errors in the response; a template parse error is a 500 that the next request retries)
     and the `HEARTH_ENV=dev` preset.
   - `.HMR` in templates (`.Dev` stays until phase 3), one `WARN` line when any override is active, and a
     warning when `request` is set on an `embed.FS` bundle.
   - Tests: every value, every invalid value, env beating code, `request` showing an edit (and `once` not),
     recovery after a parse error, opaque errors without diagnostics, the vite and manifest assets modes, the
     warning once and only when active, and an override beating `Dev`.
   Not done in this phase: the `Manifest`, `Vite` and `Placeholder` resolvers are still one `*manifest.Manifest`
   with `Lenient` and `DevBase` set (phase 3 splits it).
3. **Remove `Dev`. Done 2026-11-24.** A breaking change to `pkg/hearth` (v0.2.x → next minor):
   - Removed: `Options.Dev`, `DevAssetURL`, `LenientAssets`, `Fallback` and the `.Dev` template variable
     (`.HMR` replaces it). Added: `Options.IgnoreEnv` and `Site.Matches(r)`.
   - `internal/manifest` now has three resolvers: `*Manifest` (strict; `Lenient` and `DevBase` are gone),
     `Placeholder` and `Vite`, all satisfying `render.Assets`. `hearth check` uses `Placeholder`.
   - `hearth build`, `check` and every `hearth dev` mode build their `Site` with `Overrides` and
     `IgnoreEnv: true`, so a `HEARTH_*` variable in a developer's shell cannot change a build. `internal/dev`
     asks `Site.Matches` and sends what no page claims to its Vite proxy or static handler.
   - `cache.Nop` has no directory now, so `Export` (and therefore `Build`) returns an error when the page
     cache is off instead of writing nothing.
   - The startup warning lists only overrides that came from the environment; code that sets
     `Options.Overrides` is explicit already, and tools would otherwise print it on every run.
   - Tests moved off the removed fields; added: `Matches`, `IgnoreEnv`, `Export` with the cache off, and the
     three resolvers.
   - A site that pins hearth v0.2.0 (go.mod) is unaffected until it upgrades. On upgrade it replaces
     `Dev: true, DevAssetURL: u` with `Overrides` (or just the `HEARTH_*` variables) and any
     `Fallback` with a mux that calls `Matches`.
4. **`hearthDev()` plugin. Done 2026-11-24.** `npm/hearth/vite.js` (plugin), `lib/dev.js` (helpers),
   `test/dev.test.js`, `vite.d.ts`, `README.md`:
   - Template reload, backend proxy and page proxy as designed. `appType: "custom"` keeps Vite from serving its own
     `index.html` fallback or 404, so a request Vite did not answer reaches the page proxy (a post-hook middleware).
   - Backend prefixes are validated with the same rules as `config.ValidateProxy` and become Vite `server.proxy`
     keys that match whole path segments (`^/api(?:[/?]|$)`); the backend sees its own `Host`.
   - Loop guard: forwarded requests carry `x-hearth-via-vite`; a request that arrives with it is not proxied again.
     This matters for static-only sites, where `hearth dev` is the page server and falls back to Vite.
   - A hint line at startup prints the `HEARTH_ENV=dev HEARTH_ASSETS=vite:<url>` pair for the Go server.
   - 13 node tests: helpers, the plugin against a fake server, and (when `vite` is installed, now a devDependency)
     a real Vite for modules, backend, pages, the loop guard and a real websocket `full-reload`. The network tests
     need loopback ports.
   - Not done here: the website's hand-written `server.proxy` in `vite.config.ts` (the pattern this replaces)
     moves to `hearthDev()` when that site upgrades.
5. **Docs. Done 2026-11-24.** ARCHITECTURE.md (packages, request flow, the layers section, `hearth dev` for sites with
   no Go server), README.md ("Developing a site that has a Go server" and the variable table), DEPLOY.md,
   MIGRATING-FROM-ASTRO.md (dev loop, `search.json` behind `Site.Matches`), PLAN.md, DEVELOPMENT.md, `pkg/hearth/doc.go`,
   `npm/hearth/README.md` and `skills/hearth/SKILL.md`. FEATURE-PLAN.md is a historical plan: it got a note naming the
   removed API instead of a rewrite.

6. **`HEARTH_BUNDLE` and `ssr` from the config. Done 2026-11-25.** Found by reading the skill after phase 5: the layers
   alone did not make "one server" true, because `HEARTH_TEMPLATES=request` re-reads whatever `Bundle` is and
   `dist/templates` is the built copy, and a source tree has no manifest, so its `ssr` pages were unknown. The
   website's `dev.go` existed for exactly those two things.
   - `HEARTH_BUNDLE=<dir>` / `Overrides.Bundle`: `New` replaces `Options.Bundle` with `os.DirFS(dir)` (an error naming
     `HEARTH_BUNDLE` when it is missing or not a directory; `Options.Bundle` may then be nil). The environment wins.
   - With no `manifest.json` (only allowed with `placeholder` or `vite:` assets), `configSSR` reads `ssr` from
     `hearth.config.json` at the bundle root; a broken file is an error naming it. A bundle with a manifest ignores the
     config: the build's manifest stays authoritative.
   - The `hearthDev()` startup hint now prints `HEARTH_ENV=dev HEARTH_BUNDLE=<site dir> HEARTH_ASSETS=vite:<url>`.
   - Tests: the override (env beats code, code works with no `Options.Bundle`, bad paths), `ssr` from the config
     (works, refused without it, broken file, manifest wins) and `TestOneServerRunsInProductionAndDevelopment`: one
     server function, a built bundle in production (cached, hashed assets) and a source tree in development (Vite
     assets, nothing cached, a template edit shows on the next request).
   - The website can now delete `cmd/server/dev.go` and the `Dev`, `DevAssetURL` and SSR handling in
     `internal/site/app.go`: run `HEARTH_ENV=dev HEARTH_BUNDLE=. HEARTH_ASSETS=vite:http://localhost:5173` on the one
     server (after upgrading hearth). Its own database-optional-in-dev behaviour (`cfg.Dev`) is the importer's.

7. **Warming is a store concern. Done 2026-11-25.** The design said `Invalidate`, `WatchData` and the sitemap warm-up are
   no-ops with the store off; phase 1 to 6 only did that for `Invalidate`. Found when the website's server, now the same
   code in development, rendered its whole sitemap at start with the cache off and kept nothing.
   - `Warm` and `WarmSitemap` return an empty report (and no error) with `HEARTH_CACHE=off`: nothing renders and
     `SitemapURLs` is not called. `Build` and `Export` fail at once, before rendering, with an error naming
     `HEARTH_CACHE=off` (`Site.keepsPages`).
   - Tests: warming does nothing with the cache off (control: it renders with the cache on) and `Build` fails before
     rendering a page.
   - A site that guards warming with its own switch (the website's `WARM=0`) can drop it after upgrading.

## Open items

- **Mock funcs (not built).** `mock.Stubs` still backs `hearth dev`'s `mock` mode and `hearth check`. The designed
  `HEARTH_FUNCS=real | mock` switch for a project with a Go server was not needed by any phase and is not
  implemented; build it only if someone wants stubbed funcs inside their own server.
- **Parse cost in `request` mode.** One parse per request is fine for a development tree. If a large site makes
  it slow, `Templates` can later cache by directory mtime without changing the interface.
- **Resolved:** the Vite client's websocket and `/@vite/*` are answered by Vite before the page proxy runs
  (`npm/hearth/test/dev.test.js`, real Vite).
- **Not verified here:** a Go server and Vite together in a browser on a real site (the website). The pieces are
  tested separately: the Go layers in `pkg/hearth` (including one server function in both environments), the plugin
  against stub servers and a real Vite.
