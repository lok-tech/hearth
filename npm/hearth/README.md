# hearth

CLI for Hearth sites: `hearth init`, `build`, `check`, `dev`, `preview`.

The CLI is a Go binary shipped in a per-platform package (`@hearth/cli-<os>-<cpu>`), installed
automatically as an optional dependency. `hearth/vite` is the Vite plugin for the image step
(AVIF + WebP + fallback; needs `sharp`).

```
npm i -D hearth vite sharp
npx hearth init
```
