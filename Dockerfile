# syntax=docker/dockerfile:1.7

FROM node:22-bookworm-slim AS frontend
WORKDIR /src

COPY web/package.json web/package-lock.json ./web/
RUN npm ci --prefix web

COPY web ./web
RUN npm run build --prefix web

FROM golang:1.25-bookworm AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=frontend /src/web/dist ./web/dist

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags='-s -w' -o /out/nodevas ./cmd/nodevas

FROM alpine:3.22

RUN apk add --no-cache ca-certificates wget \
    && addgroup -S nodevas \
    && adduser -S -D -H -G nodevas -h /var/lib/nodevas nodevas \
    && install -d -o nodevas -g nodevas /var/lib/nodevas/workspace /var/lib/nodevas/config

COPY --from=builder /out/nodevas /usr/local/bin/nodevas
COPY docker/bootstrap.sh /usr/local/bin/nodevas-bootstrap
RUN chmod 0755 /usr/local/bin/nodevas /usr/local/bin/nodevas-bootstrap

ENV HOME=/var/lib/nodevas \
    XDG_CONFIG_HOME=/var/lib/nodevas/config \
    NODEVAS_SERVE_LOG_FORMAT=json \
    NODEVAS_SERVE_LOG_LEVEL=info

USER nodevas:nodevas
WORKDIR /var/lib/nodevas
VOLUME ["/var/lib/nodevas/workspace", "/var/lib/nodevas/config"]
EXPOSE 5666

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:5666/api/auth/status || exit 1

ENTRYPOINT ["/usr/local/bin/nodevas"]
CMD ["serve", "--project", "/var/lib/nodevas/workspace"]
