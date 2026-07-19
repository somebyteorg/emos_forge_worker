#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT_PATH="$ROOT_DIR/forge-worker"
BUILD_TIME="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

if [[ -z "${VERSION:-}" ]]; then
	if command -v git >/dev/null 2>&1; then
		VERSION="$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || true)"
	fi
	VERSION="${VERSION:-dev}"
fi

export GOOS=linux
export GOARCH=amd64
export CGO_ENABLED=0
export TMPDIR="${TMPDIR:-$ROOT_DIR/output/.tmp}"
export GOTMPDIR="${GOTMPDIR:-$ROOT_DIR/output/.tmp}"
export GOCACHE="${GOCACHE:-$ROOT_DIR/output/go-build-cache}"
export GOMODCACHE="${GOMODCACHE:-$ROOT_DIR/output/.gomod-cache}"

mkdir -p "$TMPDIR" "$GOTMPDIR" "$GOCACHE" "$GOMODCACHE"

LDFLAGS=(
	"-s"
	"-w"
	"-buildid="
	"-extldflags=-static"
	"-X"
	"main.version=$VERSION"
	"-X"
	"main.buildTime=$BUILD_TIME"
)

echo "building forge-worker"
echo "  output:     $OUTPUT_PATH"
echo "  version:    $VERSION"
echo "  build_time: $BUILD_TIME"
echo "  target:     $GOOS/$GOARCH"

go build \
	-trimpath \
	-buildvcs=false \
	-tags=netgo,osusergo \
	-ldflags="${LDFLAGS[*]}" \
	-o "$OUTPUT_PATH" \
	"$ROOT_DIR/cmd/forge-worker"

if command -v file >/dev/null 2>&1; then
	file "$OUTPUT_PATH"
fi

echo "done"
