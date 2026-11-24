import type { Plugin } from "vite";

export interface ImageVariant {
  ext: string;
  type: string;
  buffer: Buffer;
  width: number;
  height: number;
}

export interface HearthImagesOptions {
  /** Folder with .jpg/.jpeg/.png sources, relative to the Vite root. Default "src/images". */
  dir?: string;
  /** Widest output in pixels (the cap for `widths`); smaller images are never enlarged. Default 1600. */
  maxWidth?: number;
  /**
   * Rendition widths in pixels, for responsive srcset. Default [maxWidth] (one size, as before).
   * Positive integers, at most 8 after removing duplicates, none above maxWidth (an error). Every
   * format is written at every width; a source narrower than a width only gets its own size.
   */
  widths?: number[];
  quality?: { avif?: number; webp?: number; jpeg?: number };
  /** Replace the sharp encoder (tests, other libraries). The fallback format must be last. */
  encoder?: (file: string, opts: { maxWidth: number; width: number; quality: Required<NonNullable<HearthImagesOptions["quality"]>> }) => Promise<ImageVariant[]>;
}

export function hearthImages(options?: HearthImagesOptions): Plugin;

export interface HearthDevOptions {
  /** The Go server that renders pages. Default: dev.server in hearth.config.json, else "http://127.0.0.1:8080". */
  server?: string;
  /** Folders (relative to hearth.config.json) whose changes reload the browser. Default: pages, layouts, partials, locales, data, public. */
  watch?: string[];
  /** Path prefixes sent to a backend instead of the Go server, such as { "/api": "http://localhost:8090" }. Default: dev.proxy in hearth.config.json. */
  proxy?: Record<string, string>;
}

/** Development half of a Hearth site: template reload, backend proxy and page proxy. Serve only; it never starts your Go server. */
export function hearthDev(options?: HearthDevOptions): Plugin;
