import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmodSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const bin = join(dirname(fileURLToPath(import.meta.url)), "..", "bin", "hearth.js");

function fake(script) {
  const dir = mkdtempSync(join(tmpdir(), "hearth-bin-"));
  const p = join(dir, "fake.sh");
  writeFileSync(p, `#!/bin/sh\n${script}\n`);
  chmodSync(p, 0o755);
  return p;
}

const run = (env, ...args) =>
  spawnSync(process.execPath, [bin, ...args], { env: { PATH: process.env.PATH, ...env }, encoding: "utf8" });

test("passes arguments through and forwards stdout", { skip: process.platform === "win32" }, () => {
  const r = run({ HEARTH_BINARY_PATH: fake('echo "args: $@"') }, "build", "-site", "x y");
  assert.equal(r.status, 0);
  assert.equal(r.stdout.trim(), "args: build -site x y");
});

test("propagates the exit code", { skip: process.platform === "win32" }, () => {
  const r = run({ HEARTH_BINARY_PATH: fake("exit 7") }, "check");
  assert.equal(r.status, 7);
});

test("override pointing nowhere fails clearly", () => {
  const r = run({ HEARTH_BINARY_PATH: "/nonexistent/hearth" });
  assert.equal(r.status, 1);
  assert.match(r.stderr, /HEARTH_BINARY_PATH/);
});

test("missing platform package explains how to fix it", () => {
  const r = run({});
  assert.equal(r.status, 1);
  assert.match(r.stderr, /not installed|no Hearth binary/);
});
