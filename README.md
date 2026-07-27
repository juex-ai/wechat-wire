# wechat-wire

WeChat iLink Bot CLI and MCP bridge.

`wechat-wire` wraps the MIT-licensed Go SDK at `github.com/corespeed-io/wechatbot/golang` into one binary. The CLI handles login, status, message listening, and a local user book. The `mcp` command runs a stdio MCP server that forwards incoming WeChat messages to agents and exposes tools for sending messages back to users.

Internally, CLI and MCP share a WeChat Session module that owns the User Book and context-backed sends, so the direct CLI flow and the MCP tool flow exercise the same sending rules.

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
wechat-wire msg send --user_id <wechat-user-id> --text "hi"

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
wechat-wire msg send --user_id <wechat-user-id> --text "hello"
wechat-wire msg send --user_id <wechat-user-id> --content "hello" --format json
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
If the MCP process needs a WeChat login, it sends a `login_required` channel notification containing the QR URL so the agent can guide the user to scan it.

Inbound images, voice messages, files, and videos are downloaded and decrypted through the upstream SDK. `wechat-wire` saves them under `<config-dir>/media/YYYY-MM-DD/` and includes the absolute path in both the notification body and `meta.local_path`. Media metadata also includes `media_type`, `file_name`, and `media_size_bytes`.

```text
wechat-wire message from user_id=<id> type=image at 2026-07-27 10:45:16
[image]
local_path: /absolute/path/.config/wechat-wire/media/2026-07-27/...-image.png
file_name: image.png
```

Downloaded files use mode `0600`, and media directories use mode `0700`. Voice messages are stored in the format returned by iLink, currently `.silk`. If a download fails, the message notification is still delivered with `media_download_error` in its body and metadata.

The upstream SDK requires a current `context_token` before sending to a user. The long-lived MCP process obtains that context after it receives a message from the user.
For direct CLI sends, `wechat-wire listen` records the latest context token for each observed user in the local config directory; `wechat-wire msg send` uses that stored context to send through the upstream SDK.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `WECHAT_WIRE_DIR` | `$HOME/.config/wechat-wire` | Base config directory input; normalized to `.config/wechat-wire`. |

Runtime state is intentionally kept under the config directory:

- `credentials.json` — upstream SDK credentials.
- `users.json` — locally observed users and latest reply context.
- `media/YYYY-MM-DD/` — downloaded inbound image, voice, file, and video content.

SDK internals such as base URL, credential path, bot agent, and log level are fixed by `wechat-wire` instead of exposed as public environment variables.

## Local Tests

```bash
bash scripts/build.sh
( cd cli && go test ./... )
./tests/run.sh all
```

The E2E suite uses `WECHAT_WIRE_FAKE=1`, so it does not require real WeChat credentials. The `WECHAT_WIRE_FAKE*` variables are localtest hooks, not public runtime configuration.
The MCP E2E suite also verifies that fake inbound media is saved under the isolated config directory and that notifications expose a readable absolute path.

You can also test the CLI loop directly without a real WeChat login:

```bash
tmpdir=$(mktemp -d)

WECHAT_WIRE_FAKE=1 \
WECHAT_WIRE_FAKE_MESSAGES_JSON='[{"user_id":"u1","text":"hello cli","type":"text","context_token":"ctx"}]' \
./bin/wechat-wire --homedir "$tmpdir" listen --once --format json

WECHAT_WIRE_FAKE=1 \
./bin/wechat-wire --homedir "$tmpdir" msg send --user_id u1 --text "reply" --format json
```

## Upstream SDK

The core iLink Bot protocol is provided by `github.com/corespeed-io/wechatbot/golang` under the MIT license. `wechat-wire` depends on that package directly and keeps protocol/auth/crypto behavior there.
