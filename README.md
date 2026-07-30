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
wechat-wire msg send --user_id <wechat-user-id> --file ./report.pdf --caption "report"

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
wechat-wire msg send --user_id <wechat-user-id> --file ./image.png --caption "result"
wechat-wire msg send --user_id <wechat-user-id> --file ./artifact.bin --file_name report.pdf
wechat-wire user list --format json
wechat-wire user show --user_id <wechat-user-id>
wechat-wire user forget --user_id <wechat-user-id>
wechat-wire context-guard show --format json
wechat-wire context-guard set --enabled=true --timezone Asia/Shanghai
wechat-wire mcp --channel
```

## MCP Tools

- `wechat_wire_status`
- `wechat_wire_list_users`
- `wechat_wire_send_message`
- `wechat_wire_send_attachment`
- `wechat_wire_send_typing`
- `wechat_wire_forget_user`
- `wechat_wire_get_context_guard`
- `wechat_wire_configure_context_guard`

Incoming WeChat messages are delivered as `notifications/claude/channel` notifications when the MCP client advertises experimental `claude/channel` support, or when the server is started with `--channel`.
If the MCP process needs a WeChat login, it sends a `login_required` channel notification containing the QR URL so the agent can guide the user to scan it.

Inbound images, voice messages, files, and videos are downloaded and decrypted through the upstream SDK. `wechat-wire` saves them under `<config-dir>/media/YYYY-MM-DD/` and includes the absolute path in the notification body, `meta.local_path`, and `attachments[].path`. JueX validates the native attachment, copies it to durable event-media storage, and turns supported images into model image blocks. Media metadata also includes `media_type`, `file_name`, and `media_size_bytes`.

```text
wechat-wire message from user_id=<id> type=image at 2026-07-27 10:45:16
[image]
local_path: /absolute/path/.config/wechat-wire/media/2026-07-27/...-image.png
file_name: image.png
```

Downloaded files use mode `0600`, and media directories use mode `0700`. Voice messages are stored in the format returned by iLink, currently `.silk`. If a download fails, the message notification is still delivered with `media_download_error` in its body and metadata.

Agents can send a readable local file with `wechat_wire_send_attachment`:

```json
{
  "user_id": "<wechat-user-id>",
  "path": "/absolute/path/to/report.pdf",
  "caption": "optional caption",
  "file_name": "optional-recipient-name.pdf"
}
```

`path` may be absolute or relative to the MCP process workspace. `file_name` must be a base filename and is useful when a JueX artifact path has a generated `.bin` name. The upstream SDK sends common image extensions (`png`, `jpg`, `jpeg`, `gif`, `webp`, `bmp`, `svg`) and video extensions (`mp4`, `mov`, `webm`, `mkv`, `avi`) as native media; other extensions are sent as files. A caption is delivered as a separate text message immediately before the attachment to satisfy iLink's one-item media request contract. One attachment may be at most 100 MiB. Outbound SILK has no upstream voice-send API and is therefore sent as a normal file.

The upstream SDK requires a current `context_token` before sending to a user. The long-lived MCP process obtains that context after it receives a message from the user.
For direct CLI sends, `wechat-wire listen` records the latest context token for each observed user in the local config directory; `wechat-wire msg send` uses that stored context to send text or `--file` content through the upstream SDK.

## Context Expiry Guard

The optional context expiry guard sends one direct reminder before the latest observed context token is expected to expire. It is disabled by default because iLink does not provide an authoritative expiry timestamp; the default policy estimates a 24-hour lifetime and reminds 60 minutes before expiry.

Only an inbound user message carrying a context token starts a fresh cycle. Sending the reminder does not extend the cycle. Each cycle is durably claimed before sending so process restarts and concurrent `listen`/`mcp` processes do not send duplicates.
Existing user records created by older `wechat-wire` versions have no trustworthy context observation time and remain unscheduled until the next token-bearing inbound message.

Reminders are restricted to a local-time window, defaulting to `08:00` through `22:00`. If the normal reminder time falls outside the window, it moves earlier to the most recent window end. For example, a token estimated to expire at `03:00` is reminded at `22:00` the previous evening. If the service misses that window, it records the cycle as skipped and does not send a late-night catch-up.

```bash
wechat-wire context-guard set \
  --enabled=true \
  --assumed-ttl-minutes 1440 \
  --lead-time-minutes 60 \
  --timezone Asia/Shanghai \
  --reminder-window-from 08:00 \
  --reminder-window-to 22:00

wechat-wire context-guard set \
  --message-template '记得回复我一下，不然按当前估算，再过约 {{remaining_minutes}} 分钟，我就暂时没法主动给你发提醒啦。'
```

The message template supports `{{remaining_minutes}}`, `{{expires_at}}`, and `{{user_id}}`. Agents can inspect or partially update the same settings through `wechat_wire_get_context_guard` and `wechat_wire_configure_context_guard`; no MCP restart is required.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `WECHAT_WIRE_DIR` | `$HOME/.config/wechat-wire` | Base config directory input; normalized to `.config/wechat-wire`. |

Runtime state is intentionally kept under the config directory:

- `credentials.json` — upstream SDK credentials.
- `users.json` — locally observed users and latest reply context.
- `context-guard.json` — user-editable expiry reminder policy.
- `context-guard-state.json` — durable per-user deduplication and scheduling state.
- `media/YYYY-MM-DD/` — downloaded inbound image, voice, file, and video content.

SDK internals such as base URL, credential path, bot agent, and log level are fixed by `wechat-wire` instead of exposed as public environment variables.

## Local Tests

```bash
bash scripts/build.sh
( cd cli && go test ./... )
./tests/run.sh all
```

The E2E suite uses `WECHAT_WIRE_FAKE=1`, so it does not require real WeChat credentials. The `WECHAT_WIRE_FAKE*` variables are localtest hooks, not public runtime configuration.
The MCP E2E suite also verifies that fake inbound media is saved under the isolated config directory, notifications expose a readable absolute path, and agents can send a local attachment.

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
