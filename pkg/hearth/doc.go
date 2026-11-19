// Package hearth serves Hearth sites from your own Go server.
//
// Static pages and server-rendered pages share one template location and one render
// logic. A Site renders a page on first request, writes it to a versioned cache
// directory as a plain file, and a web server in front serves every later request from
// that file at static speed. See docs/DEPLOY.md for the web server side.
//
//	site, err := hearth.New(hearth.Options{Bundle: bundleFS, CacheDir: "/var/cache/hearth"})
//	site.MustHandle("post/{id}", loadPost) // checked against the discovered template at startup
//	http.ListenAndServe(":8080", site)
//
// The CLI is built on this package: `hearth build` runs a Site, prerenders every page
// (Prerender) and exports the files (Export); `hearth dev` runs a Site in Dev mode. A page
// is the same whichever of them made it.
//
// Invalidation is event driven: call Site.SetVersion for a new version of everything, Site.Invalidate
// to drop named pages (after your data change is committed), or give Site.Subscribe a Source
// (Poll is a stdlib-only one). The package has no database driver; your server decides
// what changes.
//
// Import it as github.com/orelhadad/hearth/pkg/hearth. The build, the CLI and everything
// else live under internal/ and cmd/hearth; this package is the only public Go API.
package hearth
