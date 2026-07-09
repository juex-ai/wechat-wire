# AGENTS.md

Guidance for agents and humans working in this repository.

For product overview and quick start, see `README.md`. For architecture internals, see `ARCHITECTURE.md`.

## Repo Layout

- `cli/` — Go module. Cobra CLI plus the stdio MCP server.
- `scripts/` — `build.sh` and `install_local.sh`. Bash, `set -euo pipefail`.
- `tests/` — E2E tests. They use `WECHAT_WIRE_FAKE=1` and do not hit real WeChat.
- `.agents/skills/wechat-wire-localtest/` — local validation skill for agents.

## Conventions

- Use the upstream MIT SDK `github.com/corespeed-io/wechatbot/golang` for iLink Bot protocol behavior. Do not reimplement raw iLink HTTP/auth/crypto unless the SDK API is insufficient and the tradeoff is documented first.
- CLI config uses `--homedir`, then `WECHAT_WIRE_DIR`, then the current home directory. The final directory is normalized to `.config/wechat-wire`.
- SDK credentials live at `WECHAT_WIRE_CRED_PATH` or `<config-dir>/credentials.json`.
- The local user book lives at `<config-dir>/users.json`; it stores observed user IDs, last-message metadata, and the latest context token used by `msg send`. Treat it as private local runtime state.
- Build metadata is injected with `-ldflags` into `main.version` and `main.commit`.
- MCP message notifications use `notifications/claude/channel` when the client advertises experimental `claude/channel`, or when `wechat-wire mcp --channel` is used.

## Development Workflow

Run `./scripts/build.sh` to build the binary. Run `go test ./...` inside `cli/` for unit tests. Run `./tests/run.sh all` for fake-backend MCP E2E validation.

Before committing, run:

```bash
bash scripts/build.sh
( cd cli && go test ./... )
./tests/run.sh all
```
