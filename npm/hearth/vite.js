import { resolve } from "node:path";
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
