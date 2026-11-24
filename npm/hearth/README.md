# hearth

CLI for Hearth sites: `hearth init`, `build`, `check`, `dev`, `preview`.

The CLI is a Go binary shipped in a per-platform package (`@lok-tech/cli-<os>-<cpu>`), installed
automatically as an optional dependency. `@lok-tech/hearth/vite` has two Vite plugins: `hearthImages` (the
image step: AVIF + WebP + fallback; needs `sharp`) and `hearthDev` (the development half, below).

```
npm i -D @lok-tech/hearth vite sharp
npx hearth init
```

## `hearthDev`: Vite in front of your Go server

For a site that is served by your own Go server (`pkg/hearth`). Vite is the one origin the browser uses; it
answers modules and assets and hands everything else to your server.

```js
// vite.config.js
import { hearthDev, hearthImages } from "@lok-tech/hearth/vite";
export default { plugins: [hearthDev(), hearthImages()] };
```

What it adds, for `vite` (serve) only:

- **Template reload.** Saving a file under `pages`, `layouts`, `partials`, `locales`, `data` or `public` reloads
  the browser. Vite does not watch those (nothing imports them); the plugin does, and uses the websocket that
  `{{entry}}` already opened.
- **Backend proxy.** `dev.proxy` from `hearth.config.json` (`{"/api": "http://localhost:8090"}`) goes to its backend.
  A prefix matches whole path segments: `/api` takes `/api/x`, not `/apiary`.
- **Page proxy.** Whatever Vite does not answer goes to your Go server (`dev.server`, default `http://127.0.0.1:8080`),
  with the browser's own `Host` and `X-Forwarded-*`.

It reads `hearth.config.json` from the Vite root upwards, so nothing is repeated in `vite.config.js`; the options
`server`, `watch` and `proxy` override the file. It does **not** start or restart your Go server. Start it with
the overrides that point its assets at Vite (the plugin prints them when Vite starts):

```
HEARTH_ENV=dev HEARTH_BUNDLE=. HEARTH_ASSETS=vite:http://localhost:5173 go run ./cmd/server
```

`HEARTH_ENV=dev` renders every request (templates re-read, nothing cached, errors shown); `HEARTH_BUNDLE=.` makes
the server read the source tree instead of `dist/templates` (and take the `ssr` list from `hearth.config.json`, since
a source tree has no manifest); the individual variables (`HEARTH_TEMPLATES`, `HEARTH_CACHE`, `HEARTH_ASSETS`,
`HEARTH_DIAGNOSTICS`) change one layer each. Your
server's code is the same in development and production.
