import { defineConfig } from "vite";
import { hearthImages } from "hearth/vite";

// `hearth build` runs `vite build` with --outDir, --emptyOutDir and --manifest,
// then renders the pages. Add one `input` entry per script/style bundle a page needs.
export default defineConfig({
  plugins: [
    // Turns src/images/**/*.{jpg,png} into AVIF + WebP + fallback, used as {{picture "name"}}.
    hearthImages({ dir: "src/images", maxWidth: 1600 }),
  ],
  build: {
    target: "es2022",
    manifest: true,
    cssCodeSplit: true,
    assetsInlineLimit: 0, // every asset is a real, cacheable, hashed file
    rollupOptions: {
      input: { entry: "src/entry.ts" },
    },
  },
  server: { port: 5173, strictPort: true, cors: true },
});
