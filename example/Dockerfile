# syntax=docker/dockerfile:1
# Build context is the repository root (the example builds against this checkout):
#   docker compose -f example/compose.yaml up --build
# One build produces both images, so the files Caddy serves and the templates the server renders
# always come from the same `hearth build`. The site has no JavaScript, so no Node is needed.

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY . .
RUN go build -trimpath -o /out/hearth ./cmd/hearth
RUN cd example/site && /out/hearth build        # -> example/site/dist/{public,templates}
WORKDIR /src/example
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/app . && mkdir -p /out/cache

FROM gcr.io/distroless/static-debian12:nonroot AS app
COPY --from=build /out/app /app
COPY --from=build /src/example/site/dist/templates /bundle
# A named volume mounted here inherits this ownership on first use, so the nonroot user can write.
COPY --from=build --chown=65532:65532 /out/cache /var/cache/hearth
ENV BUNDLE=/bundle CACHE_DIR=/var/cache/hearth ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/app"]

FROM caddy:2-alpine AS web
COPY deploy/caddy/Caddyfile /etc/caddy/Caddyfile
COPY --from=build /src/example/site/dist/public /srv/public
