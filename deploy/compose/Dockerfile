# syntax=docker/dockerfile:1
# One build produces both images, so the files Caddy serves and the templates the Go server
# renders always come from the same `hearth build`.
#
# Expected project layout (build context = project root):
#   site/     Hearth site (hearth.config.json, pages/, package.json ...)
#   server/   your Go server; embeds ./templates with `//go:embed all:templates`
#   deploy/   this folder
#
# Targets: app (Go server), web (Caddy + prebuilt files).

FROM node:22-alpine AS site
WORKDIR /src
COPY site/package.json site/package-lock.json ./
RUN npm ci
COPY site/ ./
RUN npx hearth build        # -> dist/public, dist/templates, dist/manifest.json

FROM golang:1.27-alpine AS server
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=site /src/dist/templates ./templates
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/app . && mkdir -p /out/cache

FROM gcr.io/distroless/static-debian12:nonroot AS app
COPY --from=server /out/app /app
# A named volume mounted here inherits this ownership on first use, so the nonroot user can write.
COPY --from=server --chown=65532:65532 /out/cache /var/cache/hearth
EXPOSE 8080
ENTRYPOINT ["/app"]

FROM caddy:2-alpine AS web
COPY deploy/caddy/Caddyfile /etc/caddy/Caddyfile
COPY --from=site /src/dist/public /srv/public
