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
  /** Widest output in pixels; smaller images are never enlarged. Default 1600. */
  maxWidth?: number;
  quality?: { avif?: number; webp?: number; jpeg?: number };
  /** Replace the sharp encoder (tests, other libraries). The fallback format must be last. */
  encoder?: (file: string, opts: { maxWidth: number; quality: Required<NonNullable<HearthImagesOptions["quality"]>> }) => Promise<ImageVariant[]>;
}

export function hearthImages(options?: HearthImagesOptions): Plugin;
