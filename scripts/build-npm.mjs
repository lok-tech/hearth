#!/usr/bin/env node
// Cross-compiles the hearth CLI and writes one npm package per platform into npm/platforms/.
// Usage: node scripts/build-npm.mjs [--only darwin-arm64,linux-x64]
// Publish order: every npm/platforms/* first, then npm/hearth.
import { spawnSync } from "node:child_process";
import { chmodSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const main = JSON.parse(readFileSync(join(root, "npm", "hearth", "package.json"), "utf8"));
const version = main.version;

// node platform-arch -> [GOOS, GOARCH]
const TARGETS = {
  "darwin-arm64": ["darwin", "arm64"],
  "darwin-x64": ["darwin", "amd64"],
  "linux-arm64": ["linux", "arm64"],
  "linux-x64": ["linux", "amd64"],
  "win32-arm64": ["windows", "arm64"],
  "win32-x64": ["windows", "amd64"],
};

const onlyArg = process.argv.indexOf("--only");
const only = onlyArg > -1 ? process.argv[onlyArg + 1].split(",") : Object.keys(TARGETS);
for (const k of only) if (!TARGETS[k]) throw new Error(`unknown target ${k}`);

for (const key of only) {
  const [goos, goarch] = TARGETS[key];
  const [os, cpu] = key.split("-");
  const name = `@lok-tech/cli-${key}`;
  if (!(name in main.optionalDependencies)) throw new Error(`${name} missing from npm/hearth optionalDependencies`);
  const dir = join(root, "npm", "platforms", key);
  rmSync(dir, { recursive: true, force: true });
  mkdirSync(join(dir, "bin"), { recursive: true });
  const exe = os === "win32" ? "hearth.exe" : "hearth";
  const r = spawnSync(
    "go",
    ["build", "-trimpath", "-ldflags", `-s -w -X main.version=${version}`, "-o", join(dir, "bin", exe), "./cmd/hearth"],
    { cwd: root, stdio: "inherit", env: { ...process.env, CGO_ENABLED: "0", GOOS: goos, GOARCH: goarch } },
  );
  if (r.status !== 0) throw new Error(`go build failed for ${key}`);
  if (os !== "win32") chmodSync(join(dir, "bin", exe), 0o755);
  writeFileSync(
    join(dir, "package.json"),
    JSON.stringify(
      {
        name,
        version,
        description: `The hearth CLI binary for ${key}. Install \`@lok-tech/hearth\`, not this package.`,
        license: main.license,
        os: [os],
        cpu: [cpu],
        files: ["bin"],
        preferUnplugged: true,
        repository: { type: "git", url: "git+ssh://git@github.com/lok-tech/hearth.git", directory: "npm/hearth" },
        publishConfig: { access: "public" },
      },
      null,
      2,
    ) + "\n",
  );
  writeFileSync(join(dir, "README.md"), `# ${name}\n\nPlatform binary for [hearth](https://www.npmjs.com/package/@lok-tech/hearth). Do not install directly.\n`);
  console.log(`built ${name}@${version}`);
}
