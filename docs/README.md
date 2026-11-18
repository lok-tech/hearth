# Documentation

| Document | Read it to |
|---|---|
| [../README.md](../README.md) | understand what Hearth is, use it, and know the security rules and when not to use it |
| [PLAN.md](PLAN.md) | see the decisions, deliverable status, what is verified, what is not, and the open items |
| [ARCHITECTURE.md](ARCHITECTURE.md) | change the code: packages, the single-engine rule, request and build flow, data formats, invariants |
| [DEVELOPMENT.md](DEVELOPMENT.md) | build, test and release; environment constraints; end-to-end recipe; how to extend; pitfalls |
| [DEPLOY.md](DEPLOY.md) | serve it: Caddy, nginx, S3/CDN, compose, release order, rollback, `deploy/verify.sh` |
| [../example/README.md](../example/README.md) | run and read the Postgres example |

Starting a new session on this repository: read PLAN.md (status and open items), then ARCHITECTURE.md, then run the
commands at the top of DEVELOPMENT.md to confirm everything is green before changing anything.
