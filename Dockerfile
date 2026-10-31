# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e

# Base images are pinned by digest (the multi-arch index), the tag kept for
# reading: a tag can be repointed, a digest cannot. To update one, resolve the
# new tag's digest (docker buildx imagetools inspect <image>:<tag>) and
# replace both.

FROM node:22-alpine@sha256:fe4f6d74e20370acb04ad889077d14d0f64a9c8ea6a42bc60cddf1e844699914 AS web-build
WORKDIR /src
COPY web/package.json web/package-lock.json ./web/
# No dependency runs an install script; esbuild's, the one the build needs,
# runs explicitly.
RUN npm ci --ignore-scripts --prefix web && node web/node_modules/esbuild/install.js
COPY web ./web
RUN npm run build --prefix web

FROM golang:1.26.10-alpine@sha256:27f2d3b83cfc5803c8078f8e730af0f7c431bf067afaec8f60a36fb763187cba AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /src/web/dist ./web/dist
ARG TARGETOS=linux
ARG TARGETARCH=amd64
# The commit this image is built from (.git is not in the build context, so Go
# cannot stamp it). The hub reports it to its runners, and the installer uses
# it to fetch the matching runner binary from GitHub. Empty is fine: the hub
# then reports none and the installer keeps the latest build.
ARG NODEVAS_COMMIT=""
RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -tags=nomsgpack -trimpath \
    -ldflags="-s -w -X nodevas/internal/buildinfo.commit=${NODEVAS_COMMIT}" \
    -o /out/nodevas ./cmd/nodevas

FROM alpine:3.22@sha256:2e0b81d60597f449baa56dc25d355367869d2dca4cd57e866f1e2b52af3eb55f
# git: each project's version history is a git repository it keeps
# (docs/history-and-backup.md); without git the hub falls back to file
# copies.
RUN apk add --no-cache ca-certificates tzdata git \
    && addgroup -S -g 568 nodevas \
    && adduser -S -D -H -u 568 -G nodevas nodevas \
    && install -d -o 568 -g 568 /data
COPY --from=go-build /out/nodevas /usr/local/bin/nodevas
USER 568:568
# The database is always an external server here (NODEVAS_DATABASE_URL or
# NODEVAS_DATABASE_URL_FILE): an image that started its own embedded
# PostgreSQL inside /data would be a second, unbacked-up database.
ENV NODEVAS_REQUIRE_DATABASE_URL=1
WORKDIR /data
VOLUME ["/data"]
EXPOSE 5666
# /api/healthz answers 503 when the database does not, which the probe
# treats as a failure; its successes are left out of the request log. The
# probe finds the port, listen address and scheme the way serve does
# (NODEVAS_SERVE_PORT and friends, then /data/nodevas.yaml), so a hub moved
# off the default port that way stays healthy. A --port given only on the
# command line is invisible to it: set NODEVAS_SERVE_PORT instead.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/nodevas", "healthcheck", "--project", "/data"]
ENTRYPOINT ["/usr/local/bin/nodevas"]
# --no-runner: the image has no coding agents, so a runner in it could take
# no run. Runners belong on the machines that have them.
CMD ["serve", "--project", "/data", "--no-runner"]
