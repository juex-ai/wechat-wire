#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

usage() {
    cat <<'EOF'
usage: ./tests/run.sh [--skip-build] [cli|mcp|all]

Runs wechat-wire end-to-end tests with the fake bot backend.

Suites:
  cli   CLI listen/send flow with fake incoming WeChat messages
  mcp   MCP stdio flow with fake incoming WeChat messages
  all   all suites (default)

Options:
  --skip-build  reuse existing ./bin/wechat-wire
EOF
}

skip_build=false
suites=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --skip-build)
            skip_build=true
            ;;
        cli|mcp|all)
            suites+=("$1")
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "error: unknown argument: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
    shift
done

if [[ ${#suites[@]} -eq 0 ]]; then
    suites=(all)
fi

if [[ "$skip_build" == false ]]; then
    bash scripts/build.sh
fi

packages=()
for suite in "${suites[@]}"; do
    case "$suite" in
        all)
            packages+=("./cli" "./mcp")
            ;;
        cli)
            packages+=("./cli")
            ;;
        mcp)
            packages+=("./mcp")
            ;;
    esac
done

export WECHAT_WIRE_BIN="$REPO_ROOT/bin/wechat-wire"

(
    cd tests
    go test -count=1 -v "${packages[@]}"
)
