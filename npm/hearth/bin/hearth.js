#!/usr/bin/env node
import { spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { createRequire } from "node:module";
import { binaryName, platformPackage } from "../lib/platform.js";

function findBinary() {
  // Escape hatch for development and unsupported layouts.
  const override = process.env.HEARTH_BINARY_PATH;
  if (override) {
    if (!existsSync(override)) {
      fail(`HEARTH_BINARY_PATH points to a file that does not exist: ${override}`);
    }
    return override;
  }
  const pkg = platformPackage();
  if (!pkg) {
    fail(`no Hearth binary for ${process.platform}-${process.arch}. Supported: darwin, linux, win32 on x64 and arm64.`);
  }
  try {
    return createRequire(import.meta.url).resolve(`${pkg}/bin/${binaryName()}`);
  } catch {
    fail(
      `the platform package ${pkg} is not installed.\n` +
        `It is an optional dependency; reinstall without --no-optional / --omit=optional,\n` +
        `and make sure node_modules was not copied from another OS or CPU.`,
    );
  }
}

function fail(msg) {
  process.stderr.write(`hearth: ${msg}\n`);
  process.exit(1);
}

const result = spawnSync(findBinary(), process.argv.slice(2), { stdio: "inherit" });
if (result.error) fail(result.error.message);
if (result.signal) process.kill(process.pid, result.signal);
process.exit(result.status ?? 1);
