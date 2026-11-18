// Maps Node's platform/arch to the npm package that carries the Go binary
// (the esbuild pattern: one optional dependency per platform).
export const PLATFORMS = {
  "darwin-arm64": "@hearth/cli-darwin-arm64",
  "darwin-x64": "@hearth/cli-darwin-x64",
  "linux-arm64": "@hearth/cli-linux-arm64",
  "linux-x64": "@hearth/cli-linux-x64",
  "win32-arm64": "@hearth/cli-win32-arm64",
  "win32-x64": "@hearth/cli-win32-x64",
};

export function platformPackage(platform = process.platform, arch = process.arch) {
  return PLATFORMS[`${platform}-${arch}`] ?? null;
}

export function binaryName(platform = process.platform) {
  return platform === "win32" ? "hearth.exe" : "hearth";
}
