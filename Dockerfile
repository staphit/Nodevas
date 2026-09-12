# syntax=docker/dockerfile:1

FROM node:22-alpine AS web-build
WORKDIR /src
COPY web/package.json web/package-lock.json ./web/
RUN npm ci --prefix web
COPY web ./web
RUN npm run build --prefix web

FROM golang:1.25.13-alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /src/web/dist ./web/dist
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -tags=nomsgpack -trimpath -ldflags="-s -w" \
    -o /out/nodevas ./cmd/nodevas

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 568 nodevas \
    && adduser -S -D -H -u 568 -G nodevas nodevas \
    && install -d -o 568 -g 568 /data
COPY --from=go-build /out/nodevas /usr/local/bin/nodevas
USER 568:568
WORKDIR /data
VOLUME ["/data"]
EXPOSE 5666
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:5666/ || exit 1
ENTRYPOINT ["/usr/local/bin/nodevas"]
CMD ["serve", "--project", "/data"]
