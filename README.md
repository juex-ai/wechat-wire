# wechat-wire

WeChat iLink Bot CLI and MCP bridge.

`wechat-wire` wraps the MIT-licensed Go SDK at `github.com/corespeed-io/wechatbot/golang` into one binary. The CLI handles login, status, message listening, and a local user book. The `mcp` command runs a stdio MCP server that forwards incoming WeChat messages to agents and exposes tools for sending messages back to users.

## Quick Start

```bash
# Build into ./bin/
./scripts/build.sh

# Install into ~/.local/bin
./scripts/install_local.sh

# Login with the upstream SDK QR flow
wechat-wire login

# Check local state
wechat-wire status
wechat-wire user list

# Listen for messages and record users
wechat-wire listen

# Run the MCP server
wechat-wire mcp
wechat-wire mcp --channel
```

## Commands

```bash
wechat-wire version --format json
wechat-wire status --format json
wechat-wire login --force
wechat-wire listen --once --format json
wechat-wire user list --format json
wechat-wire user show --user_id <wechat-user-id>
wechat-wire user forget --user_id <wechat-user-id>
wechat-wire mcp --channel
```

## MCP Tools

- `wechat_wire_status`
- `wechat_wire_list_users`
- `wechat_wire_send_message`
- `wechat_wire_send_typing`
- `wechat_wire_forget_user`

Incoming WeChat messages are delivered as `notifications/claude/channel` notifications when the MCP client advertises experimental `claude/channel` support, or when the server is started with `--channel`.

The upstream SDK requires a current `context_token` before sending to a user. The long-lived MCP process obtains that context after it receives a message from the user.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `WECHAT_WIRE_DIR` | `$HOME/.config/wechat-wire` | Base config directory input; normalized to `.config/wechat-wire`. |
| `WECHAT_WIRE_CRED_PATH` | `<config-dir>/credentials.json` | Upstream SDK credential file. |
| `WECHAT_WIRE_BASE_URL` | SDK default | Optional iLink base URL override. |
| `WECHAT_WIRE_LOG_LEVEL` | `info` | SDK log level. |
| `WECHAT_WIRE_BOT_AGENT` | `wechat-wire/<version>` | SDK `bot_agent` identity. |
| `WECHAT_WIRE_VERIFY_CODE` | unset | Non-interactive pairing code when WeChat requests verification. |

## Local Tests

```bash
bash scripts/build.sh
( cd cli && go test ./... )
./tests/run.sh all
```

The E2E suite uses `WECHAT_WIRE_FAKE=1`, so it does not require real WeChat credentials.

## Upstream SDK

The core iLink Bot protocol is provided by `github.com/corespeed-io/wechatbot/golang` under the MIT license. `wechat-wire` depends on that package directly and keeps protocol/auth/crypto behavior there.

