import { existsSync, readFileSync } from "node:fs";
import http from "node:http";
import https from "node:https";
import { dirname, join, resolve } from "node:path";

/** Folders of a Hearth site whose files are rendered into pages. A change in any of them reloads the browser. */
export const DEFAULT_WATCH = ["pages", "layouts", "partials", "locales", "data", "public"];

/** Where the importer's Go server listens when nothing says otherwise. */
export const DEFAULT_SERVER = "http://127.0.0.1:8080";

/**
 * Marks a request that already went through Vite's page proxy. `hearth dev` (the dev server of a site
 * with no Go server of its own) falls back to Vite for paths it does not know and the header survives
 * that hop, so Vite answers it instead of proxying it back: no loop.
 */
export const VIA_HEADER = "x-hearth-via-vite";

/** Walks up from startDir to the nearest hearth.config.json; null when there is none. */
export function findConfig(startDir) {
  for (let dir = resolve(startDir); ; dir = dirname(dir)) {
    const file = join(dir, "hearth.config.json");
    if (existsSync(file)) return file;
    if (dirname(dir) === dir) return null;
  }
}

/** Reads hearth.config.json; a syntax error names the file. */
export function readConfig(file) {
  try {
    return JSON.parse(readFileSync(file, "utf8"));
  } catch (err) {
    throw new Error(`hearth: ${file}: ${err.message}`);
  }
}

const RESERVED = ["/healthz", "/robots.txt", "/sitemap.xml", "/__hearth"];

/**
 * Checks a {prefix: backend URL} map with the rules of the Go side (internal/config ValidateProxy), so
 * the same entries are accepted by `hearth dev` and by this plugin.
 */
export function validateProxy(map, origin = "dev.proxy") {
  for (const [prefix, target] of Object.entries(map ?? {})) {
    const bad = (why) => new Error(`hearth: ${origin} prefix "${prefix}" ${why}`);
    if (prefix === "/") throw bad('would proxy the whole site; list the backend paths (such as "/api") instead');
    if (!prefix.startsWith("/")) throw bad("must start with /");
    if (prefix.endsWith("/")) throw bad('must not end with / (write "/api", it already matches "/api/x")');
    if (/[?#{}[\]\\ \t%]/.test(prefix)) throw bad("must be a plain path (no query, fragment, spaces, braces or escapes)");
    if (prefix.slice(1).split("/").some((s) => s === "" || s === "." || s === "..")) {
      throw bad('must not contain empty, "." or ".." segments');
    }
    if (RESERVED.some((r) => prefix === r || prefix.startsWith(r + "/"))) throw bad("is served by Hearth itself and cannot be proxied");
    let u;
    try {
      u = new URL(target);
    } catch {
      u = null;
    }
    if (!u || (u.protocol !== "http:" && u.protocol !== "https:") || !u.host || u.hash) {
      throw new Error(`hearth: ${origin}["${prefix}"] = "${target}" must be an absolute http(s) URL such as "http://localhost:8080"`);
    }
  }
}

/** Vite proxy key (a regular expression) for a prefix: "/api" takes "/api", "/api/x" and "/api?x", not "/apiary". */
export function proxyKey(prefix) {
  return `^${prefix.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}(?:[/?]|$)`;
}

/** Vite `server.proxy` entries for a {prefix: backend URL} map. */
export function viteProxy(map) {
  const out = {};
  for (const [prefix, target] of Object.entries(map ?? {})) {
    // changeOrigin: the backend sees its own Host, as with `hearth dev`; xfwd: X-Forwarded-* describe the browser's request.
    out[proxyKey(prefix)] = { target, changeOrigin: true, xfwd: true };
  }
  return out;
}

/** Whether a changed file (absolute path) is under one of the watched folders and not a dotfile or editor temp file. */
export function isTemplateFile(file, roots) {
  const base = file.slice(file.lastIndexOf("/") + 1);
  if (base.startsWith(".") || base.endsWith("~") || base.endsWith(".swp")) return false;
  return roots.some((r) => file === r || file.startsWith(r.endsWith("/") ? r : r + "/"));
}

/**
 * The line printed when Vite starts: how to start the Go server so it reads this site's source tree and
 * points its assets at this Vite server. The same server code runs in production without any of it.
 */
export function devHint({ target, siteDir, viteUrl }) {
  const vars = ["HEARTH_ENV=dev", `HEARTH_BUNDLE=${siteDir}`];
  if (viteUrl) vars.push(`HEARTH_ASSETS=vite:${viteUrl.replace(/\/$/, "")}`);
  return `hearth: pages from ${target}; start that server with ${vars.join(" ")}`;
}

/**
 * Connect middleware that forwards a request to the Go server, unchanged: method, path, query, body
 * and headers, with the browser's own Host (language hosts in subdomain/domain mode keep working) and
 * X-Forwarded-For, -Host and -Proto. It is installed after Vite's own middlewares, so it only sees what
 * Vite did not answer: pages, and anything else only the Go server knows.
 */
export function pageProxy(target) {
  const u = new URL(target);
  const lib = u.protocol === "https:" ? https : http;
  return function hearthPages(req, res, next) {
    if (req.headers[VIA_HEADER]) return next();
    const headers = { ...req.headers, [VIA_HEADER]: "1" };
    const peer = req.socket.remoteAddress;
    if (peer) headers["x-forwarded-for"] = req.headers["x-forwarded-for"] ? `${req.headers["x-forwarded-for"]}, ${peer}` : peer;
    if (req.headers.host) headers["x-forwarded-host"] = req.headers.host;
    headers["x-forwarded-proto"] = req.socket.encrypted ? "https" : "http";
    const up = lib.request(
      { protocol: u.protocol, hostname: u.hostname, port: u.port, method: req.method, path: req.url, headers },
      (ures) => {
        res.writeHead(ures.statusCode ?? 502, ures.statusMessage, ures.headers);
        ures.pipe(res);
      },
    );
    up.on("error", (err) => {
      if (res.headersSent) return res.destroy();
      res.statusCode = 502;
      res.setHeader("content-type", "text/plain; charset=utf-8");
      res.end(`hearth: the page server at ${u.host} is not reachable (${err.code ?? err.message}).\nStart your Go server there, or set hearthDev({ server }) / dev.server in hearth.config.json.\n`);
    });
    req.pipe(up);
  };
}
