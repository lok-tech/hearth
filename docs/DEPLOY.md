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
                       ├─► 2. <cache>/<path>                 pages the Go server rendered earlier
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
  and caches the file. After the data changes, call `site.Invalidate("/candidates", "/")` (every language).
  Call `site.Validate()` at startup: an `ssr` page without a handler answers 404.
- **Sites with custom `Funcs`** cannot be built by the CLI (it does not run your Go code). Give your binary a
  `build` subcommand that calls `hearth.Build(ctx, site, "dist/public")`, the same prerender, export and
  sitemap steps the CLI runs.

Minimal Go server (`go get github.com/lok-tech/hearth`, then
`import "github.com/lok-tech/hearth/pkg/hearth"`):

```go
//go:embed all:templates          // dist/templates copied next to main.go at build time
var bundle embed.FS

func main() {
	sub, _ := fs.Sub(bundle, "templates")
	site, err := hearth.New(hearth.Options{
		Bundle:     sub,
		CacheDir:   os.Getenv("HEARTH_CACHE_DIR"), // e.g. /var/www/mysite/public, shared with the web server
		TmpDir:     os.Getenv("HEARTH_TMP_DIR"),   // e.g. /var/www/mysite/hearth-tmp, same mount, never served
		BaseURL:    "https://example.com",
		// I18n defaults to path prefixes (/he/...); set I18n{Mode: "subdomain", Hosts: ...} for hosts.
	})
	if err != nil { log.Fatal(err) }
	site.MustHandle("post/{id}", loadPost) // a typo in the pattern panics at startup
	log.Fatal(http.ListenAndServe(":8080", site))
}
```

### Who needs which files

| | Needs | Does not need |
|---|---|---|
| **Web server** (Caddy, nginx, CDN) | `dist/public`: static pages, hashed `assets/`, `404.html`, `sitemap.xml` and `sitemap-N.xml`, `robots.txt`, and everything copied from `public/`. Plus the cache directory, **read-only**, (SSR sites) | `dist/templates`, `dist/manifest.json` |
| **Go container** (`hearth.Site`) | `dist/templates` (embedded or read from a path) and the cache directory, **read-write** | `dist/public`, `dist/manifest.json` |

What the code says, so you do not have to guess:

- The Go server reads exactly one manifest, `templates/manifest.json` inside the bundle (asset URLs, image
  variants, the `ssr` list, build id). `hearth build` also writes a copy at `dist/manifest.json`; nothing
  loads that copy at runtime. A bundle without a manifest starts only in `Options.Dev`.
- The Go server needs no `dist/public`. It answers `/sitemap.xml`, `sitemap-N.xml` and `/robots.txt` itself;
  `hearth build` writes them into `dist/public` so a web server in front can serve them as files. If you serve
  the files, redeploy them with each release so they match the bundle.
- A static-only site needs `dist/public` and nothing else. Files in `public/` (favicon, `robots.txt`, images
  that scripts load) only exist in `dist/public`: a Go container alone cannot serve them.

### A route and a `public/` directory on the same path

A route `/photos` (a page that lists them) and `public/photos/` (the image files) can coexist: the build puts
the page at `dist/public/photos/index.html` and the images beside it. The case to check is the SSR one, where
the page is not prebuilt and `public/photos/` has **no** `index.html`. A request for `/photos` must not be
answered by the directory (a listing, a 404, or a redirect to `/photos/`): it has to fall through to the Go
server. What the shipped configs do, read from their rules and **not run against a live server**:

- **nginx** (`hearth.conf`): `try_files $uri $uri/index.html @cache` tests `$uri` as a *file*, so the
  directory does not match; `$uri/index.html` does not exist; the request falls through to `@cache` and then
  `@app`. No `autoindex` is set. No config change needed.
- **Caddy** (`Caddyfile`): `file_server { canonical_uris false; pass_thru }`. With `canonical_uris false` there is
  no redirect to `/photos/`, no `browse` means no listing, and `pass_thru` hands a directory with no index file to
  the next handler (the cache layer, then `reverse_proxy`). No config change needed. If a Caddy version
  answers 404 or a listing instead, the fallback below is the safe route.

`VERIFY_DIR_COLLISION=/photos deploy/verify.sh <url>` checks it (200 HTML, no listing; add
`VERIFY_DIR_COLLISION_TEXT` for text that only the Go page contains). **Fallback if it fails on your server:**
give the files a distinct prefix (`public/photo-files/...`, served at `/photo-files/...`) so no route shares a
path with a directory. Do not rely on server-specific tricks for it.

### The cache directory

```
<cache>/post/42/index.html
<cache>/he.example.com/...   (only for subdomain/domain i18n)
```

The cache is flat: plain files, no versions, no symlink. There are two ways to lay it out.

**One site directory, one deploy target** (what playground-server sets up):

```
/var/www/mysite/              one mount: rw in the Go container, ro in the web server
  public/                     rsync --delete target = web root = HEARTH_CACHE_DIR
  hearth-tmp/                 HEARTH_TMP_DIR: temp files, never served, never touched by rsync
```

The prebuilt files and the rendered pages live in the same tree. A deploy is
`rsync -a --delete dist/public/ /var/www/mysite/public/` (trailing slashes, and never the parent, or
`--delete` removes `hearth-tmp/`): it replaces the prebuilt files and drops every cached page in one step,
so **a deploy is the full invalidation**. Deploy the Go server first, then the files. Between the two, the
new server can cache a page that names an asset the sync has not copied yet; the sync then deletes that
page (it is not in `dist/public`) and it renders again, so the window is short and self-healing.
`--keep-cache` style deploys (no `--delete`) keep every cached page and every old prebuilt file: use them
only when the templates and asset hashes did not change.

**Separate directories**: the web server tries `dist/public`, then `<cache>`, then the Go server (the
reference Caddy and nginx configs). Mount `<cache>` read-only into the web server.

- `Site.Invalidate("/post/42")` drops that page in every language; it is the only invalidation the Go
  server does. It removes `<path>/index.html` only, so a prerendered page that is invalidated is simply
  rendered again from current data, and `404.html`, `sitemap.xml` and `assets/` can never be hit.
- **`HEARTH_TMP_DIR`**: the Go server writes each page to a temp file there and renames it into the cache.
  `rename` cannot cross mounts, so the directory must be inside the same mount as the cache (one volume,
  not two bind mounts). `New` checks this at startup with a probe rename and refuses to start otherwise,
  and removes temp files older than ten minutes left by a crash. Without it, temp files are `.tmp-*`
  dotfiles in the page's directory, which is why the web server must never serve dotfiles (rule 2).
- **Ownership**: the Go server creates directories and files as its own user, and `rsync --delete` has to
  remove them later. Run rsync as the same user as the Go server (or as root, or share a group with
  group-writable directories), or the sync fails on a directory the server created.

## 2. The serving contract

Whatever server you use, it must do these seven things. `deploy/verify.sh` tests them.

1. **Files first, then the Go server.** Try `<path>` and `<path>/index.html` in `dist/public`, then in
   `<cache>`, then proxy. The query string never picks a file (the cache key is the path).
2. **Never serve dotfiles.** Hides `.tmp-*` files (when `HEARTH_TMP_DIR` is not set), `.git`, `.env`.
3. **One URL per page.** `/x/` redirects (301) to `/x`, `/x/index.html` to `/x`, `/index.html` to `/`.
   Otherwise the same page exists at three URLs and the canonical tag disagrees with the server. Details
   and a migration checklist: [Trailing slashes](#trailing-slashes-and-the-cutover-checklist).
4. **Cache headers.** HTML: `public, max-age=0, must-revalidate` plus `ETag` (the same as the Go server's
   default `Options.CacheControl`, so file hits and renders behave alike). `/assets/*`:
   `public, max-age=31536000, immutable`; they are content-hashed.
5. **Compress** HTML, CSS, JS, JSON, SVG.
6. **`/healthz` is internal.** It reports counters. Allow private ranges only.
7. **Mount the cache read-only** and run the Go server as a non-root user that owns it.

### Trailing slashes and the cutover checklist

Behaviour, checked against the code (`Site.resolve`, with a test) and read from the shipped configs:

- `/x/` answers **301** to `/x`, for static pages, parameter routes and `ssr` pages alike. `/` is the only
  path with a slash that is canonical and is never redirected. More than one trailing slash (`/x//`) goes
  to `/x` in one hop from the Go server; the Caddy rule redirects to `/x/` first, so it takes two hops.
- Language prefixes follow the same rule: `/he/` goes to `/he`, and `/he` is the Hebrew home page. In
  subdomain/domain mode the language is the host, and `/` stays `/`.
- **The query string is kept by nginx and dropped by the Go server and by the Caddy `redir`**: `/x/?a=1` goes
  to `/x?a=1` on nginx (a `rewrite ... permanent` keeps the arguments) and to `/x` on the other two. Pages
  never depend on the query (the cache key is the path), so this only matters for tracking parameters.
- `/x/index.html` goes to `/x`, `/index.html` to `/`.
- Hearth emits non-slash links itself (`.Link`, the sitemap, the canonical tag, hreflang). Hand-written
  links, and data that carries URLs, are yours to keep slash-free.

A site that used to serve both `/x` and `/x/` (many generators and older servers do) changes behaviour at
cutover. **Before cutover:**

1. Export the indexed URLs from the search console (Pages report, or the sitemap coverage export).
2. Count those ending in `/`. Those are the URLs whose redirect you are about to depend on.
3. Confirm each one answers 301 to its non-slash form, and that the target answers 200 (a script over the
   export, or `VERIFY_SLASH` below for one of each kind).
4. Confirm internal links, the sitemap and the `<link rel="canonical">` all use the non-slash form, so a
   crawler is never sent through a redirect from your own pages.
5. Keep the old URLs' redirects in place for as long as they were indexed; do not "tidy" them away.

```sh
# a static page and a parameter route: "<path>/" must be a 301 to "<path>"
VERIFY_SLASH="/about /post/1" deploy/verify.sh https://example.com
```

`VERIFY_SLASH` takes paths separated by spaces or commas, written without the trailing slash.

Known differences between servers:

- Caddy attaches its HTML `Cache-Control` before proxying; the Go server's own headers win on proxied
  responses, but a proxied 404 can carry `max-age=0, must-revalidate` (harmless: it revalidates).
- nginx `add_header` is not inherited between locations, which is why the header appears in both `/` and `@cache`.
- Subdomain/domain i18n: one server block per host with an explicit `root .../<cache>/<host>`. Never build the
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
invalidate `/*` on each release. Keep the web server's header in step with that option.

## 4. Releases and rollback

Order matters because assets are additive and pages are not:

1. `hearth build` (runs Vite, prerenders, writes `dist/`).
2. **Upload assets first** (S3/CDN, or the web server's `public/assets`). Old files stay.
3. Roll out the Go server with the new `dist/templates`.
4. Roll out `dist/public` (prebuilt pages) to the web server, or sync it to S3. If the cache directory is the
   web root, the `rsync --delete` is what empties the cache; otherwise empty the cache directory in this step.
5. Invalidate the CDN's HTML (`/*`) if it caches HTML.

Rollback: redeploy the previous bundle and its `dist/public`. Its assets are still there (step 2 never
deleted them) and the cache starts empty again.

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
VERIFY_SLASH="/about /post/1" deploy/verify.sh https://example.com       # "<path>/" is a 301 to "<path>"
VERIFY_DIR_COLLISION=/photos VERIFY_DIR_COLLISION_TEXT=Photos deploy/verify.sh https://example.com  # route vs public/ directory
VERIFY_EXTERNAL=1 deploy/verify.sh https://example.com   # from outside: /healthz must be hidden
```

`verify.sh` itself is tested against a fake server (`deploy/test/verify-selftest.sh`): a correct server passes and sixteen
deliberate violations (slash, `index.html`, dotfile, traversal, asset and HTML headers, ETag, query, dynamic
ETag, immutable 404, unbranded 404 body, failing Hebrew path, a parameter-route slash form that answers 200 or
redirects to the wrong place, a directory collision that answers 404 or a listing) each fail. The compose file is validated by `docker-compose config`.

The Hebrew `{letter}` check (`VERIFY_UNICODE`) is **not yet verified against real Caddy or nginx**. The Go side
writes the cache file under the UTF-8 name (tested), and the open question is only whether the web server maps
the percent-encoded request path to that file name. If it fails on Caddy, that is where to look first.

`VERIFY_SLASH` and `VERIFY_DIR_COLLISION` are likewise **not yet verified against real Caddy or nginx**; the
directory-collision reading of both configs above comes from their rules, not from a run.

The Caddy specifics most likely to need adjusting, if `verify.sh` fails there:

- `file_server { canonical_uris false; pass_thru }` is what stops `/about` becoming `/about/` and what falls
  through to the next layer on a miss. If your Caddy version treats directories differently, switch to
  `try_files {path} {path}/index.html` before `file_server`.
- The `{re.<name>.1}` placeholders in the `redir` lines.
