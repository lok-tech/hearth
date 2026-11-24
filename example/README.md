# Example: a Postgres-backed blog

Posts live in Postgres. A post page is rendered from its row the first time it is visited, written to
disk as a plain file, and served from that file afterwards by Caddy. Change a row and the cache flips
within seconds; the next visit renders it again.

This is its own Go module and imports only the public package, `github.com/lok-tech/hearth/pkg/hearth`.

## Run it

**With Docker (Postgres + server + Caddy), one command from the repository root:**

```sh
docker compose -f example/compose.yaml up --build
```

**Without Docker or a database** (an in-memory store with the same demo posts), from `example/`:

```sh
make run          # builds the site with this checkout's CLI, then serves on http://localhost:8080
make test         # the HTTP-level tests
```

Then try:

| URL | Result |
|---|---|
| `/post/hello-world` | 200, rendered from the database, then a file |
| `/he/post/hello-world` | 200, Hebrew, right to left, canonical and hreflang |
| `/post/hello` | 301 to `/post/hello-world` (a renamed post; `/he/post/hello` keeps `/he`) |
| `/post/retired-post` | 410 (deleted on purpose) |
| `/post/nope`, `/post/Bad_Slug` | 404, nothing written |
| `/sitemap.xml` | the static pages plus every published post, in both languages |

With Docker, watch the first request render and the second come from a file, then change a row:

```sh
docker compose -f example/compose.yaml exec db psql -U hearth blog \
  -c "update posts set title = 'Hello again' where slug = 'hello-world'"
# within POLL_SECONDS the server sees a new version, invalidates the pages, and the next visit shows the new title
```

## Where things are

| File | |
|---|---|
| `site/` | the Hearth site: layout, pages, locales (no JavaScript, so no Node needed) |
| `handler.go` | `post/{slug}`: validates the slug, maps the store's answer to 200, 301, 404 or 410 |
| `store.go` | the `Store` interface: `Post`, `Slugs`, `Version` |
| `postgres.go` | the real store: `database/sql`, parameterised queries only |
| `memory.go` | the demo and test store |
| `main.go` | wiring: bundle, cache, `watchPosts` (polls `store.Version`, calls `Invalidate`), sitemap entries, timeouts, shutdown |
| `server_test.go` | HTTP-level tests, see below |
| `schema.sql` | tables, `updated_at` trigger, seed rows |

## What the tests prove

Real requests through the real `Site` and handler, with a fake database:

- render, then a file is written; the second request is served from it (the database is asked once)
- `Invalidate` drops only the named page and it refills from the new data (also via `watchPosts`)
- 404, 410 and 301 write nothing; a failing database is a 500 and is not cached
- untrusted slugs never reach the database; hostile redirect targets from the database never become redirects
- post content is HTML-escaped; Hebrew pages are right to left and link within their language
- the sitemap lists database posts in every language and omits deleted and renamed slugs

Not covered here: a real Postgres (the store is compiled and vetted, but its SQL has not been run) and the
Docker build.
