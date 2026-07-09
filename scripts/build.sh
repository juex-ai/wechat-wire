#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

mkdir -p bin

VERSION="$(git describe --always --tags --dirty 2>/dev/null || echo dev)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"

(
    cd cli
    go build \
        -ldflags "-X main.version=$VERSION -X main.commit=$COMMIT" \
        -o ../bin/wechat-wire \
        ./cmd/wechat-wire
)

echo "built bin/wechat-wire (version=$VERSION commit=$COMMIT)"

