#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

./scripts/build.sh

mkdir -p "$HOME/.local/bin"
install -m 0755 bin/wechat-wire "$HOME/.local/bin/wechat-wire"

echo "installed: $HOME/.local/bin/wechat-wire"
echo "run 'wechat-wire version' to verify"

if [[ ":$PATH:" != *":$HOME/.local/bin:"* ]]; then
    echo "note: add \$HOME/.local/bin to your PATH (e.g. export PATH=\"\$HOME/.local/bin:\$PATH\")"
fi

