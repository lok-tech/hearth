import { resolve } from "node:path";
import { buildImages } from "./lib/images.js";

/**
 * Vite plugin: image step for `vite build`.
 * Every .jpg/.png under `dir` becomes AVIF + WebP + fallback in assets/img/,
 * available to templates as {{picture "name" "alt text"}}.
 */
export function hearthImages({ dir = "src/images", maxWidth = 1600, quality, encoder } = {}) {
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
      await buildImages({ srcDir: resolve(root, dir), outDir, maxWidth, quality, encoder });
    },
  };
}
