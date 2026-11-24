import { resolve, sep } from "node:path";
import { DEFAULT_SERVER, DEFAULT_WATCH, devHint, findConfig, isTemplateFile, pageProxy, readConfig, validateProxy, viteProxy } from "./lib/dev.js";
import { buildImages, normalizeWidths } from "./lib/images.js";

/**
 * Vite plugin: image step for `vite build`.
 * Every .jpg/.png under `dir` becomes AVIF + WebP + fallback in assets/img/,
 * available to templates as {{picture "name" "alt text"}} and {{pictureSet ...}}.
 * `widths` (default [maxWidth]) writes one rendition per width and format for responsive srcset.
 */
export function hearthImages({ dir = "src/images", maxWidth = 1600, widths, quality, encoder } = {}) {
  widths = normalizeWidths(widths, maxWidth); // fail at config time, not at the end of the build
  let root = process.cwd();
  let outDir = "";
  return {
    name: "hearth-images",
    apply: "build",
    configResolved(config) {
      root = config.root;
      outDir = resolve(root, config.build.outDir);
    },
    async closeBundle() {
      if (this.meta?.watchMode) return;
      await buildImages({ srcDir: resolve(root, dir), outDir, maxWidth, widths, quality, encoder });
    },
  };
}

/**
 * Vite plugin: the development half of a Hearth site, for `vite` (serve). Vite is the one origin the
 * browser uses. It answers modules and assets itself; this plugin adds what it cannot know about:
 *
 *  - template reload: edits under pages, layouts, partials, locales, data and public reload the browser
 *    (those files are outside Vite's module graph), over the websocket `{{entry}}` already opened;
 *  - backend proxy: dev.proxy ("/api", "/s") goes to its backend through Vite's own server.proxy;
 *  - page proxy: every other request Vite did not answer goes to the Go server.
 *
 * It does not start or restart the Go server. Start it yourself with the matching overrides:
 * `HEARTH_ENV=dev HEARTH_BUNDLE=<the site directory> HEARTH_ASSETS=vite:<this server's URL>`. Settings come from hearth.config.json
 * (found from the Vite root upwards); options override the file.
 */
export function hearthDev({ server, watch, proxy } = {}) {
  let siteDir = process.cwd();
  let target = server;
  let dirs = watch ?? DEFAULT_WATCH;
  let backends = proxy;
  return {
    name: "hearth-dev",
    apply: "serve",
    config(userConfig) {
      const root = resolve(userConfig.root ?? process.cwd());
      const file = findConfig(root);
      const cfg = file ? readConfig(file) : {};
      siteDir = file ? resolve(file, "..") : root;
      target = server ?? cfg.dev?.server ?? DEFAULT_SERVER;
      backends = proxy ?? cfg.dev?.proxy ?? {};
      validateProxy(backends, proxy ? "hearthDev({ proxy })" : "dev.proxy");
      try {
        new URL(target);
      } catch {
        throw new Error(`hearth: page server "${target}" must be an absolute http(s) URL such as "${DEFAULT_SERVER}"`);
      }
      return {
        // "custom": Vite adds no index.html fallback or 404 of its own, so a request it did not answer reaches the page proxy.
        appType: "custom",
        server: { proxy: viteProxy(backends) },
      };
    },
    configureServer(vite) {
      const roots = dirs.map((d) => resolve(siteDir, d));
      vite.watcher.add(roots);
      let timer;
      const reload = (file) => {
        if (!isTemplateFile(file.split(sep).join("/"), roots.map((r) => r.split(sep).join("/")))) return;
        clearTimeout(timer);
        timer = setTimeout(() => vite.ws.send({ type: "full-reload", path: "*" }), 50);
      };
      for (const ev of ["add", "change", "unlink"]) vite.watcher.on(ev, reload);
      vite.httpServer?.once("close", () => clearTimeout(timer));
      vite.httpServer?.once("listening", () => {
        vite.config.logger.info(`  ${devHint({ target, siteDir, viteUrl: vite.resolvedUrls?.local?.[0] })}`);
      });
      // A post-hook: it runs after Vite's own middlewares, so it only gets what Vite did not answer.
      return () => vite.middlewares.use(pageProxy(target));
    },
  };
}
