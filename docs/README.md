# Documentation

| Document | Read it to |
|---|---|
| [../README.md](../README.md) | understand what Hearth is, use it, and know the security rules and when not to use it |
| [PLAN.md](PLAN.md) | see the decisions, deliverable status, what is verified, what is not, and the open items |
| [ARCHITECTURE.md](ARCHITECTURE.md) | change the code: packages, the single-engine rule, request and build flow, data formats, invariants |
| [DEVELOPMENT.md](DEVELOPMENT.md) | build, test and release; environment constraints; end-to-end recipe; how to extend; pitfalls |
| [DEPLOY.md](DEPLOY.md) | serve it: Caddy, nginx, S3/CDN, compose, release order, rollback, `deploy/verify.sh` |
| [MIGRATING-FROM-ASTRO.md](MIGRATING-FROM-ASTRO.md) | port an Astro site (voters.co.il as the example): routes, styles, embedded data, funcs, redirects, images, parity check |
| [FEATURE-PLAN.md](FEATURE-PLAN.md) | working plan for the general features added after the first release, with designs and per-phase status |
| [LAYERS.md](LAYERS.md) | the layers (templates, render, store, assets), the `HEARTH_*` overrides that replaced `Options.Dev`, `hearth dev` for sites with no Go server and the `hearthDev()` Vite plugin: design, decisions and phase status |
| [../example/README.md](../example/README.md) | run and read the Postgres example |

Starting a new session on this repository: read PLAN.md (status and open items), then ARCHITECTURE.md, then run the
commands at the top of DEVELOPMENT.md to confirm everything is green before changing anything.
