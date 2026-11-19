# {{name}}

A Hearth site. Static pages are rendered at build time; server-rendered pages use the same templates.

```
npm install
npm run dev       # one server: pages from src with *.mock.json data, assets built by Vite in watch mode, the browser reloads on every change
npm run check     # validate routes, data, locales; trial-render every page
npm run build     # vite build + prerender -> dist/public, dist/templates, dist/manifest.json
```

## Layout

| Path | What |
|---|---|
| `pages/` | File path is the route. `about.html` -> `/about`, `post/[id].html` -> `post/{id}`, `about.he.html` overrides one language; `404.html` / `410.html` are the error pages (not routes), built to `dist/public/404.html` |
| `layouts/base.html` | The one layout. Blocks: `title`, `description`, `head`, `body` (canonical and hreflang are in the layout) |
| `partials/` | `{{define "name"}}` snippets, available in every page |
| `locales/` | `en.json`, `he.json`: `{{t "key"}}`; plurals use CLDR suffixes (`items.one`, `items.other`). A missing key fails the build |
| `data/` | Local JSON, read with `{{data "name"}}` |
| `src/` | `entry.ts`, CSS, `images/` |
| `public/` | Copied as-is to the site root |

Template helpers: `t`, `plural`, `asset`, `entry`, `picture`, `pictureSet`, `json` (for `<script type="application/json">` and `data-*` attributes), `data`. Use `{{.Link "/about"}}` for internal links so they stay in the current language (`/he/about` in Hebrew).

## Images

Put `src/images/hero.jpg` (or `.png`) in place and use `{{picture "hero" "Describe the image"}}`. The build
emits AVIF, WebP and the original format with `width` and `height` set (no layout shift) and `loading="lazy"`.
Pass `"eager"` last for the image above the fold (`{{picture "hero" "Alt" "eager"}}`): no lazy loading. For
several sizes, set `widths: [480, 960, 1600]` in `hearthImages` in `vite.config.ts` and add a `sizes` string
(`{{picture "hero" "Alt" "(min-width: 60rem) 50vw, 100vw"}}`; the default is `100vw`). `{{pictureSet}}` switches
the image by media query (dark and light logos). Files in `public/` are not processed: write their markup by hand.

## Rules

- One `<h1>` per page, a unique `<title>` and description per page and language.
- Pages must work without JavaScript; `src/entry.ts` only enhances.
- Every asset comes from the manifest (`entry`, `asset`), so it is hashed and cacheable forever. Never hard-code `/assets/...`.
- `{{entry "src/entry.ts"}}` in the layout emits every stylesheet, `modulepreload` hint and the module script of an entry. A page can load another entry from the `head` block (`{{entry "src/game.ts"}}`, after adding it to the `input` of `vite.config.ts`).
- No render-blocking chains: no CSS `@import`, scripts as `type="module"`.
- Images always carry `width`/`height` and `alt`.
- Fonts: see the note at the top of `src/styles/main.css`.
