import { defineConfig } from "vite";
import { hearthImages } from "@lok-tech/hearth/vite";

// `hearth build` runs `vite build` with --outDir, --emptyOutDir and --manifest,
// then renders the pages. Add one `input` entry per script/style bundle a page needs.
export default defineConfig({
  // Variables whose names start with one of these prefixes are read at build time and baked
  // into the client bundle as import.meta.env.<NAME>. They are PUBLIC: anyone can read them in the
  // shipped JavaScript, so never put a secret under a prefix. Set them in .env files or the
  // build environment. Values that must change without a rebuild belong in page data instead.
  envPrefix: ["VITE_", "PUBLIC_"],
  plugins: [
    // Turns src/images/**/*.{jpg,png} into AVIF + WebP + fallback, used as {{picture "name"}}.
    // Add widths: [480, 960, 1600] for responsive srcset (see README).
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
