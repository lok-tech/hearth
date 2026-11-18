# {{name}}

A Hearth site. Static pages are rendered at build time; server-rendered pages use the same templates.

```
npm install
npm run vite      # terminal 1: Vite dev server (assets, HMR)
npm run dev       # terminal 2: pages rendered from src with *.mock.json data
npm run check     # validate routes, data, locales; trial-render every page
npm run build     # vite build + prerender -> dist/public, dist/templates, dist/manifest.json
```

## Layout

| Path | What |
|---|---|
| `pages/` | File path is the route. `about.html` -> `/about`, `post/[id].html` -> `post/{id}`, `about.he.html` overrides one language |
| `layouts/base.html` | The one layout. Blocks: `title`, `description`, `head`, `body` (canonical and hreflang are in the layout) |
| `partials/` | `{{define "name"}}` snippets, available in every page |
| `locales/` | `en.json`, `he.json`: `{{t "key"}}`; plurals use CLDR suffixes (`items.one`, `items.other`). A missing key fails the build |
| `data/` | Local JSON, read with `{{data "name"}}` |
| `src/` | `entry.ts`, CSS, `images/` |
| `public/` | Copied as-is to the site root |

Template helpers: `t`, `plural`, `asset`, `picture`, `json`, `data`. Use `{{.Link "/about"}}` for internal links so they stay in the current language (`/he/about` in Hebrew).

## Images

Put `src/images/hero.jpg` (or `.png`) in place and use `{{picture "hero" "Describe the image"}}`. The build
emits AVIF, WebP and the original format with `width` and `height` set (no layout shift) and `loading="lazy"`.
For the image that is the largest paint above the fold, write the `<img>` yourself with `fetchpriority="high"` and no lazy loading.

## Rules

- One `<h1>` per page, a unique `<title>` and description per page and language.
- Pages must work without JavaScript; `src/entry.ts` only enhances.
- Every asset comes from the manifest (`asset`), so it is hashed and cacheable forever. Never hard-code `/assets/...`.
- No render-blocking chains: no CSS `@import`, one stylesheet per page, scripts as `type="module"`.
- Images always carry `width`/`height` and `alt`.
- Fonts: see the note at the top of `src/styles/main.css`.
