# AGENTS.md

Guidance for agents and humans working in this repository.

For product overview and quick start, see `README.md`. For architecture internals, see `ARCHITECTURE.md`.

## Repo Layout

- `cli/` — Go module. Cobra CLI plus the stdio MCP server.
- `cli/internal/contextguard/` — persistent context-expiry policy, quiet-hour scheduling, and restart-safe reminder deduplication.
- `cli/internal/media/` — private persistence for decrypted inbound media.
- `scripts/` — `build.sh` and `install_local.sh`. Bash, `set -euo pipefail`.
- `tests/` — E2E tests. They use `WECHAT_WIRE_FAKE=1` and do not hit real WeChat.
- `.agents/skills/wechat-wire-localtest/` — local validation skill for agents.

## Conventions

- Use the upstream MIT SDK `github.com/corespeed-io/wechatbot/golang` for iLink Bot protocol behavior. Do not reimplement raw iLink HTTP/auth/crypto unless the SDK API is insufficient and the tradeoff is documented first.
- CLI config precedence is `--homedir`, then `WECHAT_WIRE_DIR`, then the default `$HOME/.config/wechat-wire`. Explicit flag and environment values are final config directories and must not have path segments appended.
- SDK credentials live at `<config-dir>/credentials.json`. Do not add SDK-level environment variables unless there is a product requirement for the user-facing configuration.
- The local user book lives at `<config-dir>/users.json`; it stores observed user IDs, last-message metadata, and the latest context token used by `msg send`. Treat it as private local runtime state.
- Context expiry reminders are enabled by default and configured in `<config-dir>/context-guard.json`; users can disable them through CLI or MCP. Durable attempts live in `context-guard-state.json`. Preserve at-most-once behavior across restarts, keep unsent schedules active after restart, and never persist a raw token in reminder state.
- Only inbound messages carrying a context token refresh `context_observed_at`. Outbound sends must not extend the estimated context lifetime.
- A fresh token-bearing inbound message must replace an unsent reminder schedule with a new schedule based on the fresh observation time.
- Keep `last_seen_at` and `context_observed_at` monotonic when callbacks deliver out-of-order messages.
- State files use atomic replacement without `.lock` sidecars. Support one long-lived `listen` or `mcp` process per config directory.
- Do not infer `context_observed_at` from legacy `last_seen_at`; wait for a fresh token-bearing inbound message.
- Quiet-hour reminders move earlier to the configured window end. Once that delivery window is missed, mark the cycle skipped instead of sending a late catch-up.
- The WeChat Session module in `cli/internal/session` owns User Book choreography, bot adapter creation, remembered message handling, local outbound-file validation, and context-backed sends. CLI and MCP callers should use that seam instead of reimplementing context-token lookup or file loading.
- Use the upstream SDK's media download/decryption API. Persist inbound media under `<config-dir>/media/` through `cli/internal/media`; do not expose CDN references or reimplement CDN crypto.
- Use the upstream SDK's `ReplyContent`/`SendFile` path for outbound attachments. Do not duplicate CDN upload, encryption, or media-type routing.
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
