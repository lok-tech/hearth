import { createHash } from "node:crypto";
import { mkdir, readdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { join, extname, relative, sep } from "node:path";

const SOURCE_EXTS = new Set([".jpg", ".jpeg", ".png"]);
const NAME_RE = /^[A-Za-z0-9_-]+(\/[A-Za-z0-9_-]+)*$/;

/** Logical image name for a file under the image dir: "team/ana.jpg" -> "team/ana". */
export function imageName(dir, file) {
  const rel = relative(dir, file).split(sep).join("/");
  const name = rel.slice(0, rel.length - extname(rel).length);
  if (!NAME_RE.test(name)) {
    throw new Error(
      `hearth/images: ${rel}: file names may only contain letters, digits, "-" and "_" (got "${name}")`,
    );
  }
  return name;
}

async function walk(dir) {
  const out = [];
  for (const e of await readdir(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isSymbolicLink()) throw new Error(`hearth/images: refusing symlink ${p}`);
    if (e.isDirectory()) out.push(...(await walk(p)));
    else if (SOURCE_EXTS.has(extname(e.name).toLowerCase())) out.push(p);
  }
  return out.sort();
}

/**
 * Default encoder: AVIF, WebP and the original format at most `maxWidth` wide, via sharp.
 * Returns [{ext, type, buffer, width, height}] with the fallback LAST.
 */
export async function sharpEncoder(file, { maxWidth, quality }) {
  let sharp;
  try {
    sharp = (await import("sharp")).default;
  } catch {
    throw new Error('hearth/images needs the "sharp" package: npm i -D sharp');
  }
  const src = await readFile(file);
  const base = () => sharp(src).rotate().resize({ width: maxWidth, withoutEnlargement: true });
  const ext = extname(file).toLowerCase();
  const fallback = ext === ".png" ? "png" : "jpeg";
  const jobs = [
    ["avif", "image/avif", base().avif({ quality: quality.avif })],
    ["webp", "image/webp", base().webp({ quality: quality.webp })],
    [
      fallback === "jpeg" ? "jpg" : "png",
      fallback === "jpeg" ? "image/jpeg" : "image/png",
      fallback === "jpeg" ? base().jpeg({ quality: quality.jpeg, mozjpeg: true }) : base().png({ compressionLevel: 9 }),
    ],
  ];
  const out = [];
  for (const [e, type, pipeline] of jobs) {
    const { data, info } = await pipeline.toBuffer({ resolveWithObject: true });
    out.push({ ext: e, type, buffer: data, width: info.width, height: info.height });
  }
  return out;
}

/**
 * Encodes every image under srcDir into outDir/assets/img and writes
 * outDir/.hearth/images.json for `hearth build` (name -> [{url,type,width,height}], fallback last).
 */
export async function buildImages({
  srcDir,
  outDir,
  maxWidth = 1600,
  quality,
  encoder = sharpEncoder,
}) {
  quality = { avif: 55, webp: 78, jpeg: 80, ...quality };
  let files;
  try {
    files = await walk(srcDir);
  } catch (e) {
    if (e.code === "ENOENT") return {};
    throw e;
  }
  const manifest = {};
  const taken = new Map();
  const imgDir = join(outDir, "assets", "img");
  await mkdir(imgDir, { recursive: true });
  for (const file of files) {
    const name = imageName(srcDir, file);
    if (taken.has(name)) {
      throw new Error(`hearth/images: ${file} and ${taken.get(name)} both map to "${name}"`);
    }
    taken.set(name, file);
    const variants = await encoder(file, { maxWidth, quality });
    if (!variants.length) throw new Error(`hearth/images: encoder returned nothing for ${file}`);
    manifest[name] = [];
    for (const v of variants) {
      const hash = createHash("sha256").update(v.buffer).digest("hex").slice(0, 8);
      const fileName = `${name.replaceAll("/", "-")}-${hash}.${v.ext}`;
      const tmp = join(imgDir, `.${fileName}.tmp`);
      await writeFile(tmp, v.buffer);
      await rename(tmp, join(imgDir, fileName));
      manifest[name].push({ url: `/assets/img/${fileName}`, type: v.type, width: v.width, height: v.height });
    }
  }
  const metaDir = join(outDir, ".hearth");
  await rm(metaDir, { recursive: true, force: true });
  await mkdir(metaDir, { recursive: true });
  await writeFile(join(metaDir, "images.json"), JSON.stringify(manifest, null, 2));
  return manifest;
}
