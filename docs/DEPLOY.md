# Deploying Hearth

Hearth bakes pages into files; a web server serves those files at static speed. This page covers what to
put in front of it (Caddy or nginx), where assets can live (disk, S3, CDN), and how to roll out a release.
Playground is out of scope.

Files referenced here live in `deploy/`:

| File | What |
|---|---|
| `deploy/caddy/Caddyfile` | Caddy: prebuilt files, render cache, Go server |
| `deploy/caddy/Caddyfile.static` | Caddy: static-only site |
| `deploy/nginx/hearth.conf` | nginx: the same three layers |
| `deploy/compose/` | Dockerfile (one build, two images) and `compose.yaml` |
| `deploy/verify.sh` | Checks any server against the contract below |

## 1. What gets served from where

```
request ──► web server ──► 1. dist/public/<path>             prebuilt pages + hashed assets
                       ├─► 2. <cache>/current/<path>         pages the Go server rendered earlier
                       └─► 3. Go server (hearth.Site)        renders, writes file 2, answers
```

- **Static-only site** (no Go server): serve `dist/public` and nothing else. Use `Caddyfile.static`, or
  the nginx `location /` block with `try_files $uri $uri/index.html =404`.
- **SSR site**: all three layers. Step 3 only sees misses, so after warm-up the Go server handles
  almost nothing. 404, 410 and redirects are never written to the cache, so they always reach step 3.
- **404 pages.** Put `pages/404.html` (and optionally `410.html`, and `404.he.html` per language) next to your
  pages. They render inside the layout with `.Status`, are `noindex`, are never cached, and the Go server answers
  with them itself, so a SSR site needs nothing extra in the web server. For servers without the Go server in
  the path (static-only, S3/CDN, a missing asset), `hearth build` also writes the rendered 404 as a file:
  `dist/public/404.html` (default language; in prefix mode also `dist/public/<lang>/404.html`), or
  `<host>/404.html` in each host directory in subdomain/domain modes. `Caddyfile.static` and the nginx example
  serve it with status 404.
- `hearth build` is not a second renderer: it runs a `hearth.Site` over your templates, prerenders every
  page (`Site.Prerender`) and copies the result (`Site.Export`) into `dist/public`. A prebuilt file and a page
  your server renders on a miss therefore come from the same code and are byte-identical (a test enforces it).
- `hearth build` writes `dist/templates`, which the Go server embeds (`embed.FS`) or reads from a path.
  Both the build and the server record a bundle format version; a mismatch fails at startup.
- **Pages that list `ssr` in `hearth.config.json`** (for example `/candidates`) are not prerendered, so no file
  exists in `dist/public` and the request falls through to the Go server, which renders them with their handler
  and caches the file. After the data changes, call `site.Invalidate("/candidates", "/")` (every language) or
  `SetVersion` for everything. Call `site.Validate()` at startup: an `ssr` page without a handler answers 404.
- **Sites with custom `Funcs`** cannot be built by the CLI (it does not run your Go code). Give your binary a
  `build` subcommand that calls `hearth.Build(ctx, site, "dist/public")`, the same prerender, export and
  sitemap steps the CLI runs.

Minimal Go server (`go get github.com/orelhadad/hearth`, then
`import "github.com/orelhadad/hearth/pkg/hearth"`):

```go
//go:embed all:templates          // dist/templates copied next to main.go at build time
var bundle embed.FS

func main() {
	sub, _ := fs.Sub(bundle, "templates")
	site, err := hearth.New(hearth.Options{
		Bundle:     sub,
		CacheDir:   os.Getenv("HEARTH_CACHE_DIR"), // e.g. /var/cache/hearth, shared with the web server
		BaseURL:    "https://example.com",
		// I18n defaults to path prefixes (/he/...); set I18n{Mode: "subdomain", Hosts: ...} for hosts.
	})
	if err != nil { log.Fatal(err) }
	site.MustHandle("post/{id}", loadPost) // a typo in the pattern panics at startup
	log.Fatal(http.ListenAndServe(":8080", site))
}
```

### The cache directory

```
<cache>/current  ->  versions/<bundle>.<n>      relative symlink, flipped atomically
<cache>/versions/<bundle>.<n>/post/42/index.html
<cache>/versions/<bundle>.<n>/he.example.com/...   (only for subdomain/domain i18n)
```

- Mount it **read-only** into the web server and point the web server at `<cache>/current`.
- Cache versions are `<bundle build id>.<data version>`. **A new bundle starts an empty cache**, so a
  redeploy can never serve pages rendered by the old templates or with the old asset hashes. A restart
  with the same bundle keeps its warm cache. `site.SetVersion("v42")` (or `Subscribe`) flips the data
  version; pages refill lazily. Old versions are pruned (`Options.KeepVersions`, default 2).
- The Go server writes with temp files named `.tmp-*` in the page's directory, then renames. They are
  only complete after the rename, which is why the web server must never serve dotfiles (rule 2).

## 2. The serving contract

Whatever server you use, it must do these seven things. `deploy/verify.sh` tests them.

1. **Files first, then the Go server.** Try `<path>` and `<path>/index.html` in `dist/public`, then in
   `<cache>/current`, then proxy. The query string never picks a file (the cache key is the path).
2. **Never serve dotfiles.** Hides the `.tmp-*` files, `.git`, `.env`.
3. **One URL per page.** `/x/` redirects (301) to `/x`, `/x/index.html` to `/x`, `/index.html` to `/`.
   Otherwise the same page exists at three URLs and the canonical tag disagrees with the server.
4. **Cache headers.** HTML: `public, max-age=0, must-revalidate` plus `ETag` (the same as the Go server's
   default `Options.CacheControl`, so file hits and renders behave alike). `/assets/*`:
   `public, max-age=31536000, immutable`; they are content-hashed.
5. **Compress** HTML, CSS, JS, JSON, SVG.
6. **`/healthz` is internal.** It reports version and counters. Allow private ranges only.
7. **Mount the cache read-only** and run the Go server as a non-root user that owns it.

Known differences between servers:

- Caddy attaches its HTML `Cache-Control` before proxying; the Go server's own headers win on proxied
  responses, but a proxied 404 can carry `max-age=0, must-revalidate` (harmless: it revalidates).
- nginx `add_header` is not inherited between locations, which is why the header appears in both `/` and `@cache`.
- Subdomain/domain i18n: one server block per host with an explicit `root .../current/<host>`. Never build the
  directory from the `Host` header.

## 3. Assets on S3 or a CDN

Hashed assets (`dist/public/assets/*`) are immutable, so they can live anywhere. Two shapes:

**A. Same domain, path-based routing (recommended).** CloudFront (or any CDN) routes `/assets/*` to the
bucket and everything else to the web server. Leave `assetBase` unset: URLs stay `/assets/...`, no CORS.

**B. Separate asset domain.** In `hearth.config.json`:

```json
{ "assetBase": "https://cdn.example.com" }
```

The build passes `--base` to Vite and prefixes every manifest URL, so `{{asset ...}}`, `{{picture ...}}` and
CSS `url()` all point at the CDN. Files from `public/` (favicon, robots) stay on your origin. Cross-origin
module scripts and fonts need CORS on the bucket or CDN:

```json
[{ "AllowedOrigins": ["https://example.com"], "AllowedMethods": ["GET", "HEAD"], "AllowedHeaders": ["*"], "MaxAgeSeconds": 86400 }]
```

Upload:

```sh
aws s3 sync dist/public/assets s3://BUCKET/assets \
  --cache-control "public, max-age=31536000, immutable" --only-show-errors
# Some tool versions guess no type for .avif; set it explicitly:
aws s3 cp dist/public/assets/img s3://BUCKET/assets/img --recursive --exclude "*" --include "*.avif" \
  --content-type image/avif --cache-control "public, max-age=31536000, immutable"
```

Rules for assets:

- **Never delete old assets on deploy, and never use a lifecycle rule by age.** Pages cached by CDNs, browsers
  and the render cache keep pointing at old hashes. `sync` only uploads changed files, so an age-based rule would
  also delete current files that did not change. To prune, delete only files referenced by none of the manifests of
  the releases you keep.
- Serve assets with `Content-Encoding` where possible: have the CDN compress, or upload pre-compressed
  files with the matching header. Do not rely on the origin.

### Pages on S3 as well (fully static sites)

```sh
aws s3 sync dist/public s3://BUCKET --exclude "assets/*" --delete \
  --cache-control "public, max-age=0, must-revalidate"
```

S3 does not resolve `/about` to `about/index.html`. With CloudFront, add a viewer-request function:

```js
function handler(event) {
  var r = event.request, u = r.uri;
  if (u === "/index.html") return redirect("/");
  if (u.endsWith("/index.html")) return redirect(u.slice(0, -"/index.html".length));
  if (u.length > 1 && u.endsWith("/")) return redirect(u.slice(0, -1));
  if (!/\.[A-Za-z0-9]+$/.test(u)) r.uri = (u === "/" ? "" : u) + "/index.html";
  return r;
  function redirect(to) {
    return { statusCode: 301, statusDescription: "Moved Permanently", headers: { location: { value: to } } };
  }
}
```

For SSR on top of S3, use an origin group: S3 first, the web server as failover on 403/404 (S3 answers 403
for a missing key when the role lacks `s3:ListBucket`). This is the same "file, else server" rule as step 1.
HTML on a CDN: keep `max-age=0, must-revalidate` (it revalidates via ETag), or set
`Options.CacheControl` to something like `public, max-age=0, s-maxage=60, stale-while-revalidate=300` and
invalidate `/*` on each release or `SetVersion`. Keep the web server's header in step with that option.

## 4. Releases and rollback

Order matters because assets are additive and pages are not:

1. `hearth build` (runs Vite, prerenders, writes `dist/`).
2. **Upload assets first** (S3/CDN, or the web server's `public/assets`). Old files stay.
3. Roll out the Go server with the new `dist/templates`. On start it sees a new bundle id and begins an empty cache.
4. Roll out `dist/public` (prebuilt pages) to the web server, or sync it to S3.
5. Invalidate the CDN's HTML (`/*`) if it caches HTML.

Rollback: redeploy the previous bundle. Its assets are still there (step 2 never deleted them), and its
cache version directory may still be on disk if it is within `KeepVersions`.

With the compose example both images come from one build and are replaced together; there is a window of a few
seconds between the two containers. To remove it, publish assets to S3/CDN (shape A or B), or copy the previous
release's `public/assets` into the new web image.

## 5. Verify

None of these configs have been run against a live Caddy or nginx by the author (Docker and local ports are
blocked in the development sandbox), so run the checks yourself:

```sh
nginx -t                       # or: caddy validate --config deploy/caddy/Caddyfile
deploy/verify.sh http://localhost:8080
VERIFY_DYNAMIC=/post/1 VERIFY_DYNAMIC_MISSING=/post/nope deploy/verify.sh https://example.com
VERIFY_404_TEXT="Page not found" deploy/verify.sh https://example.com   # the missing path shows pages/404.html
VERIFY_UNICODE=/letter/%D7%90 deploy/verify.sh https://example.com      # a percent-encoded Hebrew parameter
VERIFY_EXTERNAL=1 deploy/verify.sh https://example.com   # from outside: /healthz must be hidden
```

`verify.sh` itself is tested against a fake server (`deploy/test/verify-selftest.sh`): a correct server passes and twelve
deliberate violations (slash, `index.html`, dotfile, traversal, asset and HTML headers, ETag, query, dynamic
ETag, immutable 404, unbranded 404 body, failing Hebrew path) each fail. The compose file is validated by `docker-compose config`.

The Hebrew `{letter}` check (`VERIFY_UNICODE`) is **not yet verified against real Caddy or nginx**. The Go side
writes the cache file under the UTF-8 name (tested), and the open question is only whether the web server maps
the percent-encoded request path to that file name. If it fails on Caddy, that is where to look first.

The Caddy specifics most likely to need adjusting, if `verify.sh` fails there:

- `file_server { canonical_uris false; pass_thru }` is what stops `/about` becoming `/about/` and what falls
  through to the next layer on a miss. If your Caddy version treats directories differently, switch to
  `try_files {path} {path}/index.html` before `file_server`.
- The `{re.<name>.1}` placeholders in the `redir` lines.
