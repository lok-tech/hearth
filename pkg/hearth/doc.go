// Package hearth serves Hearth sites from your own Go server.
//
// Static pages and server-rendered pages share one template location and one render
// logic. A Site renders a page on first request, writes it to the cache
// directory as a plain file, and a web server in front serves every later request from
// that file at static speed. See docs/DEPLOY.md for the web server side.
//
//	site, err := hearth.New(hearth.Options{Bundle: bundleFS, CacheDir: "/var/cache/hearth"})
//	site.MustHandle("post/{id}", loadPost) // checked against the discovered template at startup
//	http.ListenAndServe(":8080", site)
//
// The CLI is built on this package: `hearth build` runs a Site, prerenders every page
// (Prerender) and exports the files (Export); `hearth dev` runs a Site with the dev overrides. A page
// is the same whichever of them made it.
//
// There is no development mode. A Site is layers with production defaults (templates parsed once, pages cached
// as files, assets from the bundle's manifest), and the HEARTH_* environment variables replace one layer each:
// HEARTH_TEMPLATES=request re-reads the templates on every request, HEARTH_CACHE=off keeps nothing,
// HEARTH_ASSETS=vite:<url> takes assets from a Vite dev server, HEARTH_DIAGNOSTICS=1 explains failures, and
// HEARTH_BUNDLE=<dir> reads the source tree instead of Options.Bundle, and HEARTH_ENV=dev presets
// the first, second and last. Your server is the same code in every environment;
// see Overrides. Site.Matches tells a mux which requests no page claims.
//
// The cache is flat (<CacheDir>/<path>/index.html), with no versions: a deploy that replaces the
// directory is the full invalidation, and Site.Invalidate drops named pages after your data change is
// committed. Set Options.TmpDir to a directory on the same mount so temp files never appear in the
// served tree. The package has no database driver; your server decides what changes.
//
// Import it as github.com/lok-tech/hearth/pkg/hearth. The build, the CLI and everything
// else live under internal/ and cmd/hearth; this package is the only public Go API.
package hearth
