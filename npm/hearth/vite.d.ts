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
