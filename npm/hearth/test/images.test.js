import test from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, symlinkSync, writeFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { buildImages, imageName } from "../lib/images.js";

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
