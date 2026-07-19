FROM golang:1.26-alpine3.23 AS builder

WORKDIR /src

ARG VERSION=260717
ARG BUILD_TIME=

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64 \
    TMPDIR=/src/output/.tmp \
    GOTMPDIR=/src/output/.tmp \
    GOCACHE=/src/output/go-build-cache \
    GOMODCACHE=/src/output/.gomod-cache

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN mkdir -p "$TMPDIR" "$GOTMPDIR" "$GOCACHE" "$GOMODCACHE" && go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN set -eu; \
    if [ -z "$BUILD_TIME" ]; then BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; fi; \
    go build \
      -trimpath \
      -buildvcs=false \
      -tags=netgo,osusergo \
      -ldflags="-s -w -buildid= -extldflags=-static -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
      -o /src/forge-worker \
      /src/cmd/forge-worker

FROM alpine:3.23

ARG PACKAGER_VERSION=3.9.1

RUN apk add --no-cache \
      ca-certificates \
      ffmpeg \
      libheif \
      libheif-tools \
      vips \
      vips-heif \
      vips-magick \
      vips-tools \
      wget \
    && wget -O /usr/local/bin/packager "https://github.com/shaka-project/shaka-packager/releases/download/v${PACKAGER_VERSION}/packager-linux-x64" \
    && chmod +x /usr/local/bin/packager \
    && mkdir -p /app/output/.tmp

WORKDIR /app

ENV FORGE_OUTPUT_DIR=/app/output \
    FORGE_FFMPEG_PATH=ffmpeg \
    FORGE_FFPROBE_PATH=ffprobe \
    FORGE_PACKAGER_PATH=packager \
    FORGE_VIPS_PATH=vips \
    TMPDIR=/app/output/.tmp \
    GOTMPDIR=/app/output/.tmp

COPY --from=builder /src/forge-worker /usr/local/bin/forge-worker

ENTRYPOINT ["forge-worker"]
CMD ["worker"]
