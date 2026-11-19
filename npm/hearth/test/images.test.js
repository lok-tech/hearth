import test from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, symlinkSync, writeFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { hearthImages } from "../vite.js";
import { buildImages, imageName, normalizeWidths } from "../lib/images.js";

const fakeEncoder = async (file) => [
  { ext: "avif", type: "image/avif", buffer: Buffer.from("avif:" + file), width: 800, height: 600 },
  { ext: "webp", type: "image/webp", buffer: Buffer.from("webp:" + file), width: 800, height: 600 },
  { ext: "jpg", type: "image/jpeg", buffer: Buffer.from("jpg:" + file), width: 800, height: 600 },
];

function setup(files) {
  const root = mkdtempSync(join(tmpdir(), "hearth-img-"));
  const srcDir = join(root, "src", "images");
  for (const f of files) {
    mkdirSync(join(srcDir, f, ".."), { recursive: true });
    writeFileSync(join(srcDir, f), "x");
  }
  return { root, srcDir, outDir: join(root, "out") };
}

test("writes hashed variants and a manifest with the fallback last", async () => {
  const { srcDir, outDir } = setup(["hero.jpg", "team/ana.png", "notes.txt"]);
  const m = await buildImages({ srcDir, outDir, encoder: fakeEncoder });
  assert.deepEqual(Object.keys(m).sort(), ["hero", "team/ana"]);
  assert.deepEqual(m.hero.map((f) => f.type), ["image/avif", "image/webp", "image/jpeg"]);
  assert.match(m.hero[0].url, /^\/assets\/img\/hero-[0-9a-f]{8}\.avif$/);
  assert.match(m["team/ana"][2].url, /^\/assets\/img\/team-ana-[0-9a-f]{8}\.jpg$/);
  const files = readdirSync(join(outDir, "assets", "img"));
  assert.equal(files.length, 6);
  assert.ok(!files.some((f) => f.endsWith(".tmp")));
  const onDisk = JSON.parse(readFileSync(join(outDir, ".hearth", "images.json"), "utf8"));
  assert.deepEqual(onDisk, m);
  assert.equal(m.hero[0].width, 800);
});

test("hash changes with content, stays stable otherwise", async () => {
  const a = setup(["a.jpg"]);
  const b = setup(["a.jpg"]);
  const enc2 = async (f) => (await fakeEncoder(f)).map((v) => ({ ...v, buffer: Buffer.concat([v.buffer, Buffer.from("!")]) }));
  const m1 = await buildImages({ ...a, encoder: fakeEncoder });
  const m1b = await buildImages({ srcDir: a.srcDir, outDir: join(a.root, "out2"), encoder: fakeEncoder });
  const m2 = await buildImages({ ...b, encoder: enc2 });
  assert.equal(m1.a[0].url, m1b.a[0].url);
  assert.notEqual(m1.a[0].url, m2.a[0].url);
});

test("a missing image dir is not an error", async () => {
  const { root } = setup([]);
  assert.deepEqual(await buildImages({ srcDir: join(root, "nope"), outDir: join(root, "out"), encoder: fakeEncoder }), {});
});

test("rejects unsafe names and collisions", async () => {
  assert.throws(() => imageName("/s", "/s/a b.jpg"), /may only contain/);
  assert.throws(() => imageName("/s", "/s/..x/a.jpg"), /may only contain/);
  const dup = setup(["a.jpg", "a.png"]);
  await assert.rejects(buildImages({ ...dup, encoder: fakeEncoder }), /both map to "a"/);
  const bad = setup(["we ird.jpg"]);
  await assert.rejects(buildImages({ ...bad, encoder: fakeEncoder }), /may only contain/);
});

test("refuses symlinks in the image dir", async () => {
  const s = setup(["a.jpg"]);
  symlinkSync("/etc/hosts", join(s.srcDir, "link.jpg"));
  await assert.rejects(buildImages({ ...s, encoder: fakeEncoder }), /symlink/);
  assert.ok(!existsSync(join(s.outDir, ".hearth", "images.json")));
});

test("an encoder that returns nothing is an error", async () => {
  const s = setup(["a.jpg"]);
  await assert.rejects(buildImages({ ...s, encoder: async () => [] }), /returned nothing/);
});

test("partial quality settings keep the other defaults", async () => {
  const s = setup(["a.jpg"]);
  let seen;
  await buildImages({ ...s, quality: { avif: 40 }, encoder: async (f, o) => ((seen = o.quality), fakeEncoder(f)) });
  assert.deepEqual(seen, { avif: 40, webp: 78, jpeg: 80 });
});

// Honours the requested width but never upscales a source of `srcWidth` pixels, like sharp's withoutEnlargement.
const sizedEncoder = (srcWidth) => async (file, { width }) => {
  const w = Math.min(width, srcWidth);
  return ["avif", "webp", "jpg"].map((ext) => ({
    ext,
    type: ext === "jpg" ? "image/jpeg" : `image/${ext}`,
    buffer: Buffer.from(`${ext}:${file}:${w}`),
    width: w,
    height: w / 2,
  }));
};

test("several widths write every format at every width, grouped by format", async () => {
  const s = setup(["hero.jpg"]);
  const m = await buildImages({ ...s, widths: [960, 480, 960], maxWidth: 1600, encoder: sizedEncoder(2000) });
  assert.deepEqual(m.hero.map((f) => [f.type, f.width]), [
    ["image/avif", 480], ["image/avif", 960],
    ["image/webp", 480], ["image/webp", 960],
    ["image/jpeg", 480], ["image/jpeg", 960],
  ]);
  assert.equal(new Set(m.hero.map((f) => f.url)).size, 6);
  assert.equal(readdirSync(join(s.outDir, "assets", "img")).length, 6);
  assert.deepEqual(JSON.parse(readFileSync(join(s.outDir, ".hearth", "images.json"), "utf8")), m);
});

test("a narrow source is never upscaled and gets only its own size", async () => {
  const s = setup(["small.png", "wide.jpg"]);
  const enc = (f, o) => sizedEncoder(f.endsWith("small.png") ? 700 : 2000)(f, o);
  const m = await buildImages({ ...s, widths: [480, 960, 1600], encoder: enc });
  assert.deepEqual(m.small.filter((f) => f.type === "image/avif").map((f) => f.width), [480, 700]);
  assert.equal(m.wide.filter((f) => f.type === "image/jpeg").length, 3);
  const tiny = setup(["t.jpg"]);
  const mt = await buildImages({ ...tiny, widths: [480, 960], encoder: sizedEncoder(300) });
  assert.deepEqual(mt.t.map((f) => f.width), [300, 300, 300]);
  assert.equal(mt.t.length, 3);
});

test("the default is one width (maxWidth) and the encoder sees it", async () => {
  const s = setup(["a.jpg"]);
  const seen = [];
  const m = await buildImages({ ...s, maxWidth: 1200, encoder: async (f, o) => (seen.push(o.maxWidth), fakeEncoder(f)) });
  assert.deepEqual(seen, [1200]);
  assert.equal(m.a.length, 3);
});

test("renditions hash by content and are stable across runs", async () => {
  const s = setup(["a.jpg"]);
  const m1 = await buildImages({ ...s, widths: [480, 960], encoder: sizedEncoder(2000) });
  const m2 = await buildImages({ srcDir: s.srcDir, outDir: join(s.root, "out2"), widths: [960, 480], encoder: sizedEncoder(2000) });
  assert.deepEqual(m1, m2);
  const m3 = await buildImages({ srcDir: s.srcDir, outDir: join(s.root, "out3"), widths: [480, 961], encoder: sizedEncoder(2000) });
  assert.equal(m1.a[0].url, m3.a[0].url); // the 480 rendition is unchanged
  assert.notEqual(m1.a[1].url, m3.a[1].url);
});

test("widths validation", () => {
  assert.deepEqual(normalizeWidths(undefined, 1600), [1600]);
  assert.deepEqual(normalizeWidths([960, 480, 960, 1600], 1600), [480, 960, 1600]);
  for (const bad of [[], "480", [0], [-5], [480.5], ["480"], [NaN], [1601], [null]]) {
    assert.throws(() => normalizeWidths(bad, 1600), /hearth\/images/, JSON.stringify(bad));
  }
  assert.throws(() => normalizeWidths([1, 2, 3, 4, 5, 6, 7, 8, 9], 1600), /at most 8/);
  assert.equal(normalizeWidths([1, 2, 3, 4, 5, 6, 7, 8, 8], 1600).length, 8);
  // the plugin fails when it is configured, not at the end of a build
  assert.throws(() => hearthImages({ widths: [0] }), /positive integers/);
  assert.throws(() => hearthImages({ maxWidth: 800, widths: [480, 960] }), /above maxWidth/);
  assert.equal(hearthImages({ widths: [480, 960] }).name, "hearth-images");
});
