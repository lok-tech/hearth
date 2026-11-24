import test from "node:test";
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import http from "node:http";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { hearthDev } from "../vite.js";
import { devHint, findConfig, isTemplateFile, pageProxy, proxyKey, readConfig, validateProxy, viteProxy, VIA_HEADER } from "../lib/dev.js";

const tmp = () => mkdtempSync(join(tmpdir(), "hearth-dev-"));

function listen(handler) {
  return new Promise((ok) => {
    const s = http.createServer(handler).listen(0, "127.0.0.1", () => ok(s));
  });
}
const urlOf = (s) => `http://127.0.0.1:${s.address().port}`;
const close = (s) => new Promise((ok) => (s.closeAllConnections?.(), s.close(ok)));

function get(url, { method = "GET", headers = {}, body } = {}) {
  return new Promise((ok, fail) => {
    const req = http.request(url, { method, headers }, (res) => {
      let data = "";
      res.setEncoding("utf8");
      res.on("data", (c) => (data += c));
      res.on("end", () => ok({ status: res.statusCode, headers: res.headers, body: data }));
    });
    req.on("error", fail);
    req.end(body);
  });
}

test("findConfig walks up to the nearest hearth.config.json", () => {
  const root = tmp();
  mkdirSync(join(root, "a", "b"), { recursive: true });
  writeFileSync(join(root, "hearth.config.json"), `{"dev":{"server":"http://127.0.0.1:9"}}`);
  assert.equal(findConfig(join(root, "a", "b")), join(root, "hearth.config.json"));
  assert.equal(readConfig(join(root, "hearth.config.json")).dev.server, "http://127.0.0.1:9");
  writeFileSync(join(root, "hearth.config.json"), `{oops`);
  assert.throws(() => readConfig(join(root, "hearth.config.json")), /hearth\.config\.json/);
});

test("validateProxy applies the rules of the Go side", () => {
  validateProxy({ "/api": "http://localhost:8090", "/s/x": "https://b.test" });
  const bad = [
    [{ "/": "http://x.test" }, /whole site/],
    [{ api: "http://x.test" }, /start with/],
    [{ "/api/": "http://x.test" }, /end with/],
    [{ "/a b": "http://x.test" }, /plain path/],
    [{ "/a//b": "http://x.test" }, /empty/],
    [{ "/healthz": "http://x.test" }, /Hearth itself/],
    [{ "/api": "localhost:8090" }, /absolute http/],
    [{ "/api": "ftp://x.test" }, /absolute http/],
  ];
  for (const [m, re] of bad) assert.throws(() => validateProxy(m), re, JSON.stringify(m));
});

test("a proxy key matches whole path segments", () => {
  const re = new RegExp(proxyKey("/api"));
  for (const p of ["/api", "/api/x", "/api?q=1"]) assert.ok(re.test(p), p);
  for (const p of ["/apiary", "/x/api", "/ap"]) assert.ok(!re.test(p), p);
  assert.ok(new RegExp(proxyKey("/a.b")).test("/a.b/c") && !new RegExp(proxyKey("/a.b")).test("/axb"));
  assert.deepEqual(Object.keys(viteProxy({ "/api": "http://x.test" })), [proxyKey("/api")]);
  assert.equal(viteProxy({ "/api": "http://x.test" })[proxyKey("/api")].changeOrigin, true);
});

test("devHint prints the variables a Go server needs in development", () => {
  assert.equal(
    devHint({ target: "http://127.0.0.1:8080", siteDir: "/work/site", viteUrl: "http://localhost:5173/" }),
    "hearth: pages from http://127.0.0.1:8080; start that server with HEARTH_ENV=dev HEARTH_BUNDLE=/work/site HEARTH_ASSETS=vite:http://localhost:5173",
  );
  assert.doesNotMatch(devHint({ target: "http://x.test", siteDir: "/s" }), /HEARTH_ASSETS/);
});

test("isTemplateFile only accepts real files under the watched folders", () => {
  const roots = ["/site/pages", "/site/layouts"];
  assert.ok(isTemplateFile("/site/pages/index.html", roots));
  assert.ok(isTemplateFile("/site/layouts/base.html", roots));
  assert.ok(!isTemplateFile("/site/src/a.ts", roots));
  assert.ok(!isTemplateFile("/site/pages2/a.html", roots));
  assert.ok(!isTemplateFile("/site/pages/.a.html.swp", roots));
  assert.ok(!isTemplateFile("/site/pages/a.html~", roots));
  assert.ok(!isTemplateFile("/site/pages/.hidden", roots));
});

test("pageProxy forwards the request and relays the response", async () => {
  let seen;
  const go = await listen((req, res) => {
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      seen = { method: req.method, url: req.url, headers: req.headers, body };
      res.writeHead(201, { "content-type": "text/html", "x-go": "1", "set-cookie": ["a=1", "b=2"] });
      res.end("<p>page</p>");
    });
  });
  const front = await listen((req, res) => pageProxy(urlOf(go))(req, res, () => ((res.statusCode = 599), res.end("next"))));
  const r = await get(`${urlOf(front)}/he/about?x=1`, { method: "POST", headers: { host: "he.example.test", "content-type": "text/plain" }, body: "hello" });
  assert.equal(r.status, 201);
  assert.equal(r.body, "<p>page</p>");
  assert.equal(r.headers["x-go"], "1");
  assert.deepEqual(r.headers["set-cookie"], ["a=1", "b=2"]);
  assert.equal(seen.method, "POST");
  assert.equal(seen.url, "/he/about?x=1");
  assert.equal(seen.body, "hello");
  assert.equal(seen.headers.host, "he.example.test", "the browser's Host reaches the Go server");
  assert.equal(seen.headers["x-forwarded-host"], "he.example.test");
  assert.equal(seen.headers["x-forwarded-proto"], "http");
  assert.match(seen.headers["x-forwarded-for"], /127\.0\.0\.1|::1/);
  assert.equal(seen.headers[VIA_HEADER], "1");
  await close(front);
  await close(go);
});

test("pageProxy answers 502 with a hint when the Go server is down", async () => {
  const dead = await listen(() => {});
  const target = urlOf(dead);
  await close(dead);
  const front = await listen((req, res) => pageProxy(target)(req, res, () => {}));
  const r = await get(`${urlOf(front)}/`);
  assert.equal(r.status, 502);
  assert.match(r.body, /not reachable/);
  assert.match(r.body, /dev\.server/);
  await close(front);
});

test("pageProxy never sends a request back that already came through Vite", async () => {
  let hits = 0;
  const go = await listen((req, res) => ((hits += 1), res.end("go")));
  let nextCalled = false;
  const front = await listen((req, res) => pageProxy(urlOf(go))(req, res, () => ((nextCalled = true), (res.statusCode = 404), res.end("vite 404"))));
  const r = await get(`${urlOf(front)}/x`, { headers: { [VIA_HEADER]: "1" } });
  assert.equal(r.status, 404);
  assert.ok(nextCalled);
  assert.equal(hits, 0);
  await close(front);
  await close(go);
});

// A stand-in for the parts of ViteDevServer the plugin touches.
function fakeVite() {
  const watcher = new EventEmitter();
  watcher.added = [];
  watcher.add = (p) => watcher.added.push(...[].concat(p));
  const sent = [];
  const used = [];
  return {
    watcher,
    ws: { send: (m) => sent.push(m) },
    middlewares: { use: (fn) => used.push(fn) },
    httpServer: null,
    sent,
    used,
  };
}

test("hearthDev reads hearth.config.json and configures Vite", () => {
  const root = tmp();
  writeFileSync(join(root, "hearth.config.json"), JSON.stringify({ dev: { server: "http://127.0.0.1:8099", proxy: { "/api": "http://localhost:8090" } } }));
  const p = hearthDev();
  const cfg = p.config({ root });
  assert.equal(cfg.appType, "custom");
  assert.deepEqual(Object.keys(cfg.server.proxy), [proxyKey("/api")]);
  assert.equal(cfg.server.proxy[proxyKey("/api")].target, "http://localhost:8090");
  assert.equal(p.apply, "serve");
});

test("hearthDev options win over the file, and bad settings fail at startup", () => {
  const root = tmp();
  writeFileSync(join(root, "hearth.config.json"), JSON.stringify({ dev: { proxy: { "/api": "http://localhost:1" } } }));
  const cfg = hearthDev({ proxy: { "/s": "http://localhost:2" } }).config({ root });
  assert.deepEqual(Object.keys(cfg.server.proxy), [proxyKey("/s")]);
  assert.throws(() => hearthDev({ proxy: { "/": "http://localhost:2" } }).config({ root }), /whole site/);
  assert.throws(() => hearthDev({ server: "nope" }).config({ root }), /absolute http/);
  assert.deepEqual(hearthDev().config({ root: tmp() }).server.proxy, {}, "no hearth.config.json: no proxies, defaults");
});

test("hearthDev reloads the browser once per burst of template edits", async () => {
  const root = tmp();
  writeFileSync(join(root, "hearth.config.json"), "{}");
  const p = hearthDev();
  p.config({ root });
  const vite = fakeVite();
  const post = p.configureServer(vite);
  assert.ok(vite.watcher.added.includes(join(root, "pages")), "template folders are added to Vite's watcher");
  assert.equal(typeof post, "function", "the page proxy is installed after Vite's own middlewares");
  post();
  assert.equal(vite.used.length, 1);

  vite.watcher.emit("change", join(root, "src", "a.ts"));
  vite.watcher.emit("change", join(root, "pages", ".index.html.swp"));
  await new Promise((r) => setTimeout(r, 120));
  assert.equal(vite.sent.length, 0, "files outside the template folders do not reload");

  vite.watcher.emit("change", join(root, "pages", "index.html"));
  vite.watcher.emit("add", join(root, "layouts", "x.html"));
  vite.watcher.emit("unlink", join(root, "locales", "he.json"));
  await new Promise((r) => setTimeout(r, 150));
  assert.deepEqual(vite.sent, [{ type: "full-reload", path: "*" }]);
});

test("hearthDev watch option replaces the default folders", () => {
  const root = tmp();
  const p = hearthDev({ watch: ["tpl"] });
  p.config({ root });
  const vite = fakeVite();
  p.configureServer(vite);
  assert.deepEqual(vite.watcher.added, [join(root, "tpl")]);
});

// The whole chain against a real Vite, when it is installed (a devDependency).
let vite;
try {
  vite = await import("vite");
} catch {}

test("with a real Vite: Vite answers modules, the plugin proxies backends and pages", { skip: !vite && "vite is not installed" }, async () => {
  const hits = [];
  const go = await listen((req, res) => ((hits.push(`go ${req.method} ${req.url} ${req.headers[VIA_HEADER] ?? ""}`), res.setHeader("content-type", "text/html"), res.end(req.url === "/missing" ? "go 404" : `<p>page ${req.url}</p>`))));
  const api = await listen((req, res) => (hits.push(`api ${req.url} host=${req.headers.host}`), res.end(`api ${req.url}`)));
  const root = tmp();
  mkdirSync(join(root, "src"));
  mkdirSync(join(root, "pages"));
  writeFileSync(join(root, "src", "a.js"), "export const a = 1;\n");
  writeFileSync(join(root, "hearth.config.json"), JSON.stringify({ dev: { server: urlOf(go), proxy: { "/api": urlOf(api) } } }));
  const server = await vite.createServer({ root, logLevel: "silent", configFile: false, plugins: [hearthDev()], server: { port: 0, host: "127.0.0.1" } });
  await server.listen();
  const base = urlOf(server.httpServer);
  try {
    let r = await get(`${base}/he/about`);
    assert.equal(r.status, 200);
    assert.equal(r.body, "<p>page /he/about</p>", "a page Vite does not know comes from the Go server");

    r = await get(`${base}/src/a.js`);
    assert.equal(r.status, 200);
    assert.match(r.body, /export const a/, "a module is answered by Vite");
    assert.ok(!hits.some((h) => h.includes("/src/a.js")), "and never reaches the Go server");

    r = await get(`${base}/api/search?q=1`);
    assert.equal(r.body, "api /api/search?q=1", "dev.proxy reaches its backend through Vite");
    assert.ok(hits.some((h) => h.startsWith("api /api/search?q=1 host=127.0.0.1:" + new URL(urlOf(api)).port)), "the backend sees its own Host");

    r = await get(`${base}/apiary`);
    assert.equal(r.body, "<p>page /apiary</p>", "/api does not capture /apiary");

    r = await get(`${base}/@vite/client`);
    assert.equal(r.status, 200, "Vite's client is served by Vite");

    r = await get(`${base}/missing`, { headers: { [VIA_HEADER]: "1" } });
    assert.notEqual(r.body, "go 404", "a request that already went through Vite is not proxied again");
  } finally {
    await server.close();
    await close(go);
    await close(api);
  }
});

test("with a real Vite: saving a template sends full-reload over the HMR websocket", { skip: !vite && "vite is not installed" }, async () => {
  const root = tmp();
  mkdirSync(join(root, "pages"));
  writeFileSync(join(root, "pages", "index.html"), "v1");
  writeFileSync(join(root, "hearth.config.json"), "{}");
  const server = await vite.createServer({ root, logLevel: "silent", configFile: false, plugins: [hearthDev()], server: { port: 0, host: "127.0.0.1" } });
  await server.listen();
  const ws = new WebSocket(`ws://127.0.0.1:${server.httpServer.address().port}/`, "vite-hmr");
  const got = new Promise((ok, fail) => {
    const t = setTimeout(() => fail(new Error("no full-reload within 5s")), 5000);
    ws.addEventListener("message", (e) => {
      const m = JSON.parse(e.data);
      if (m.type === "full-reload") (clearTimeout(t), ok(m));
    });
  });
  await new Promise((ok) => ws.addEventListener("open", ok));
  try {
    await new Promise((r) => setTimeout(r, 300)); // let the watcher settle
    writeFileSync(join(root, "pages", "index.html"), "v2");
    assert.equal((await got).type, "full-reload");
  } finally {
    ws.close();
    await server.close();
  }
});
