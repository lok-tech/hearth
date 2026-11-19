# Development

How to build, test and verify Hearth, how to extend it, and what to know about the environment this was built
in. Structure and rules are in [ARCHITECTURE.md](ARCHITECTURE.md); status and open work in [PLAN.md](PLAN.md).

## Toolchain

Go 1.27.3 (both `go.mod` files say `go 1.27.3`), Node 24, npm 11. Dependencies: the library uses the standard
library and `golang.org/x/sync` only; `example/` adds `pgx`; `npm/hearth` has no runtime dependencies (`sharp` and
`vite` are optional peers). Do not add dependencies to the library without a strong reason.

The folder is not a git repository yet. When it becomes one, the user's global rule is to commit and push with the
`/git-add`, `/git-ac`, `/git-commit` and `/git-push` skills, not raw `git`.

## Run everything

From the repository root:

```sh
gofmt -l . example                       # must print nothing
go vet ./... && go test -race -count=1 ./...
(cd example && go vet ./... && go test -race -count=1 ./...)   # separate module: ./... at the root skips it
(cd npm/hearth && npm test)              # node --test test/*.test.js
deploy/test/verify-selftest.sh           # tests deploy/verify.sh against a fake server
docker-compose -f deploy/compose/compose.yaml config -q && docker-compose -f example/compose.yaml config -q
```

`go test ./...` at the root also runs the guard tests (`internal/archtest`, `internal/build/parity_test.go`).

## Environment notes (the development sandbox)

These cost time to discover. They apply to a sandboxed Bash; on a normal machine most do not matter.

- **Go build cache** is outside the writable area: `export GOCACHE=$TMPDIR/gocache` before any `go` command.
- **npm cache** (`~/.npm`) is not writable: pass `--cache <writable dir>` to `npm install` and `npm pack`, and allow
  the host `registry.npmjs.org`.
- **Fetching Go modules** (`go get`, `go mod tidy` for a new dependency) fails inside the sandbox: the proxy host is
  blocked and TLS verification of the sandboxed proxy fails. These were run once, outside the sandbox, for
  `golang.org/x/sync` and `pgx`. Everything else works offline from the module cache; Go toolchains 1.23 to 1.27
  are cached there.
- **Local ports cannot be bound**: no `httptest.NewServer`, no `http.ListenAndServe`. Tests use `httptest.NewRecorder`
  and in-process handlers; the proxy transport is replaceable (`dev.proxyTransport`) so proxy tests use an in-memory
  upstream. This is why `hearth dev`/`preview` over real TCP and the Vite HMR socket are unverified.
- **Docker** builds and containers are blocked. `docker-compose config -q` (the standalone binary) validates compose
  files without a daemon. Do not override `DOCKER_CONFIG`; it hides the `docker compose` plugin.
- **No nginx or Caddy** is installed, so those configs are untested. `deploy/verify.sh` is the way to test them.
- **macOS tooling**: BSD `sed` has no `\b` (use `perl -pi -e` or Python); `sed -i ''` needs the empty argument;
  `make` prints `xcrun` cache errors in the sandbox (harmless); the shell is zsh, so quote globs such as
  `--include='*.go'`.
- **Temp files**: use `$TMPDIR` or the session scratchpad, never `/tmp`. Scratchpad contents (the e2e project,
  tarballs, npm cache) can disappear between sessions; the recipe below recreates them.
- Anything downloaded (npm packages, tarballs) is untrusted data: keep it in its own directory.

## Verification recipes

### Real end to end (npm wrapper, Vite, sharp)

This is the check that unit tests cannot give. `S` is any writable scratch directory.

```sh
export S=<scratch> GOCACHE=$TMPDIR/gocache
mkdir -p $S/tarballs $S/e2e
node scripts/build-npm.mjs --only darwin-arm64            # use your own os-arch
(cd npm/hearth && npm pack --cache $S/npmcache --pack-destination $S/tarballs)
(cd npm/platforms/darwin-arm64 && npm pack --cache $S/npmcache --pack-destination $S/tarballs)
go build -o $S/hearth ./cmd/hearth
cd $S/e2e && $S/hearth init -name e2e .                   # scaffold the starter
npm pkg set "devDependencies.@lok-tech/hearth=file:../tarballs/lok-tech-hearth-0.1.0.tgz" \
            "devDependencies.@lok-tech/cli-darwin-arm64=file:../tarballs/lok-tech-cli-darwin-arm64-0.1.0.tgz"
npm install --cache $S/npmcache --no-audit --no-fund --ignore-scripts
mkdir -p src/images && node -e "require('sharp')({create:{width:2400,height:1200,channels:3,background:{r:180,g:68,b:31}}}).jpeg().toFile('src/images/hero.jpg')"
# put {{picture "hero" "alt"}} in a page, then:
npx hearth check && npm run build && npx tsc --noEmit
```

Expect `dist/public` with hashed `assets/`, three image variants, `hreflang` and canonical tags, `/he/...` pages with
`dir="rtl"`, `sitemap.xml`, `robots.txt`, and no `.vite/` or `.hearth/`. Set `HEARTH_BINARY_PATH` to test a freshly
built binary through the wrapper without repacking. Versions last verified: Vite 6.4, sharp 0.35, TypeScript 5.x for
the starter (`^5.6.0`).

### Web server contract

`deploy/verify.sh <base-url>` against a running Caddy or nginx (options at the top of the script). It has not been run
against a live server yet; if it fails on Caddy, see the "Caddy specifics" note at the end of [DEPLOY.md](DEPLOY.md).

### Guard tests must bite

After changing a guard (or adding one), break the rule on purpose and confirm the test fails, then restore. Examples
already done: a stray `internal/render` import in `internal/dev`; the build's `Site` configured with a different
`BaseURL`; removing the example's slug validation; trusting a redirect target from the database.

## Release

1. Bump `version` in `npm/hearth/package.json` (the six `optionalDependencies` and `scripts/build-npm.mjs` follow it;
   a test checks they agree).
2. `node scripts/build-npm.mjs` writes `npm/platforms/<os-arch>/` (static Go binaries, `-X main.version`).
3. Publish every platform package first, then `hearth` (`npm publish` in each directory).
4. Tag the repository; Go users get `pkg/hearth` through the module version.

The npm names are `@lok-tech/hearth` and `@lok-tech/cli-<os>-<cpu>` (`hearth` and `hearth-cli` are taken on npm). Packages are public (`publishConfig.access`). The installed binary is still `hearth`. The npm account uses 2FA: each `npm publish` asks for a one-time code, so run the publish loop in an interactive terminal.

## How to extend

- **Template helper**: add it in `render.funcs`, document it in the starter README and the root README, test in
  `render_test.go`. Helpers must not return `template.HTML` from untrusted data.
- **`Options` field** (public): add it to `pkg/hearth.Options`, handle it in `New`, keep `doc.go` and PLAN's API
  sketch in step. Public types must use only stdlib types.
- **Config option**: `internal/config` (field, default, validation, test), then map it into `hearth.Options` in
  `build.newSite` **and** `dev.NewMock`, or the build and dev will differ from a server. The parity test builds with
  prefix and subdomain configs; extend it if your option changes output.
- **Anything that changes a page's HTML**: change `Site.pageData` or the templates, never the build.
- **CLI command**: `cmd/hearth/main.go` (usage text, flag set, exit codes 0/1/2) plus `main_test.go`; the logic lives
  in an `internal` package.
- **i18n mode**: `internal/i18n/urls.go` (`Location`, `Origin`, `Resolve`) and `newURLs` validation in `Site.New`; add
  cases to `urls_test.go`, `pkg/hearth/i18n_test.go` and the parity test.
- **Cache layout or manifest meaning**: bump `manifest.BundleFormat` (now 3) and update ARCHITECTURE.md and every test manifest that names a format.
- **Starter**: edit `internal/starter/template/`; `starter_test.go` runs `check` and a build with a fake Vite on it.

## Conventions

- Errors from the library start with `hearth:`; messages say what to do, not only what failed.
- Paths from outside (URLs, data names, hosts, versions, image names) are validated at the boundary, and the
  validation has a test. Prefer rejecting over sanitising.
- No dotfile is ever served or exported; temp files are `.tmp-*`.
- Tests: table-driven where it helps, helpers in `internal/testutil`, `t.TempDir()` for files, no sleeps except
  bounded polling in the `Subscribe` tests. Race detector on.
- Keep `docs/PLAN.md` honest: list what was verified and what was not, including what the sandbox prevented.

## Pitfalls already hit

- A scripted refactor that qualifies identifiers also qualifies struct field names and function definitions; check
  `Config *config.Config` style fields and `func pkg.Name(` after any mass rename.
- Local variables named like a package (`cache`, `sitemap`, `site`) shadow it; rename the variable.
- Moving a symbol between packages can leave a stale import that reports as an import cycle instead of "unused".
- `html/template` needs the layout parsed as the root and pages added to the same set; per-language overrides change
  the page file, not the layout.
- Go's `go:embed` skips dotfiles unless the pattern is `all:`; npm rewrites `.gitignore` on publish.
- `node --test test/` fails on Node 24; use a glob: `test/*.test.js`.
- `Site.Warm` sends requests without a `Host`; in subdomain/domain mode use `Prerender` or `WarmSitemap` (both set the host).
