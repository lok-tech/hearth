import test from "node:test";
import assert from "node:assert/strict";
import { PLATFORMS, binaryName, platformPackage } from "../lib/platform.js";
import pkg from "../package.json" with { type: "json" };

test("known platforms map to scoped packages", () => {
  assert.equal(platformPackage("darwin", "arm64"), "@hearth/cli-darwin-arm64");
  assert.equal(platformPackage("win32", "x64"), "@hearth/cli-win32-x64");
  assert.equal(platformPackage("linux", "x64"), "@hearth/cli-linux-x64");
});

test("unknown platforms return null", () => {
  assert.equal(platformPackage("freebsd", "x64"), null);
  assert.equal(platformPackage("linux", "ia32"), null);
});

test("windows binary has .exe", () => {
  assert.equal(binaryName("win32"), "hearth.exe");
  assert.equal(binaryName("linux"), "hearth");
});

test("package.json optionalDependencies match PLATFORMS exactly", () => {
  assert.deepEqual(Object.keys(pkg.optionalDependencies).sort(), Object.values(PLATFORMS).sort());
  for (const v of Object.values(pkg.optionalDependencies)) assert.equal(v, pkg.version);
});
