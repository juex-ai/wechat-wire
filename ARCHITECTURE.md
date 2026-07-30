# Architecture

`wechat-wire` is a single binary that wraps the upstream WeChat iLink Bot Go SDK.

```
+----------------------------+
| wechat-wire CLI            |
|                            |
|  login/status/user/listen  |
|  context-guard             |
|  mcp (stdio server)        |
+-------------+--------------+
              |
              v
+----------------------------+
| Context Guard module       |
| - estimated expiry policy  |
| - quiet-hour scheduling    |
| - durable at-most-once     |
+-------------+--------------+
              |
              v
+----------------------------+
| WeChat Session module      |
| - User Book choreography   |
| - text/attachment sends    |
| - inbound media handling   |
| - bot adapter creation     |
+-------------+--------------+
              |
              v
+----------------------------+
| bot.Client interface       |
| - real SDK adapter         |
| - fake localtest adapter   |
+-------------+--------------+
              |
              v
+----------------------------+
| github.com/corespeed-io/   |
| wechatbot/golang           |
+----------------------------+
```

## CLI

Commands:

- `version` — build metadata.
- `status` — config directory, credential path, login state, known user count.
- `login` — invokes the upstream QR login flow and persists SDK credentials.
- `listen` — logs in, receives incoming messages through the SDK listener, prints them, and records users locally.
- `msg send` — sends text or one local attachment to a locally observed user using the latest stored context token.
- `user list|show|forget` — manages the local user book.
- `context-guard show|set` — reads or partially updates proactive context-expiry reminder policy.
- `mcp` — starts a stdio MCP server.

## WeChat Session Module

The WeChat Session module is the seam shared by CLI and MCP callers. It owns the interface for recording incoming messages, downloading inbound media, listing/forgetting observed users, creating bot adapters, and sending text or local attachments with the latest context token from the User Book.

This keeps the context-token invariant in one implementation: callers do not know how `users.json` is shaped, when login must happen, or whether the concrete adapter is the real upstream SDK or fake localtest adapter.

The bot adapter retains the upstream parsed message only long enough to call the SDK's `Download` method. `cli/internal/media` then persists decrypted bytes under the active config directory with private permissions and sanitized filenames. Protocol download, CDN crypto, and media parsing remain owned by the upstream SDK.

For outbound attachments, the Session module resolves and validates one readable regular file, enforces a 100 MiB memory-safety limit, and passes its bytes plus a base filename to the bot adapter. The adapter sends an optional caption as a separate SDK text reply because iLink media requests accept one item per `item_list`, then calls the upstream SDK's `ReplyContent` and `SendFile` for the file. The SDK routes known image and video extensions to native WeChat media messages and sends other extensions as files.

## MCP

The MCP server logs in through the same SDK adapter and starts the message listener after MCP initialization. Incoming WeChat messages are recorded in the local user book and forwarded as `notifications/claude/channel` notifications when supported. Media messages are downloaded asynchronously so CDN I/O does not block the listener; their notifications contain an absolute local path in both human-readable metadata and the native `attachments` array after persistence completes. When SDK login needs QR scanning, the MCP server sends a `login_required` channel notification with the QR URL before continuing the login flow.

Tools:

- `wechat_wire_status`
- `wechat_wire_list_users`
- `wechat_wire_send_message`
- `wechat_wire_send_attachment`
- `wechat_wire_send_typing`
- `wechat_wire_forget_user`
- `wechat_wire_get_context_guard`
- `wechat_wire_configure_context_guard`

The upstream SDK only allows proactive sends after it has a `context_token` for the target user. `wechat-wire` records the latest context token from incoming messages. CLI `msg send` uses the stored token through the SDK reply paths; MCP sends reuse the active SDK process and the stored token.

## Context Guard

`cli/internal/contextguard` is an independent scheduler around the Session module. It derives an estimated expiry and reminder time from the latest inbound context observation, claims due work under a cross-process file lock, persists the claim before network I/O, and delegates the actual message to `Session.SendText`.

The persisted claim provides at-most-once behavior across process crashes, restarts, and concurrent `listen`/`mcp` processes. A failed or interrupted attempt is terminal for that context cycle because retrying an ambiguous network result could duplicate the user-visible reminder. A later inbound message creates a new cycle and rearms the scheduler.

The configured local-time window is also a delivery deadline. A reminder whose nominal time is outside the window moves to the preceding window end; a process that resumes after that deadline marks the cycle skipped rather than sending during quiet hours. MCP configuration changes and inbound messages wake the in-process scheduler immediately; a 30-second poll covers cross-process changes such as the CLI editing the policy.

The expiry is explicitly an estimate: iLink supplies the token but no authoritative expiry timestamp. The assumed TTL, lead time, timezone, window, enable switch, and message template are persisted and can be changed through either CLI or MCP.

## Storage

Default config directory:

```text
$HOME/.config/wechat-wire/
```

Files:

- `credentials.json` — upstream SDK credentials.
- `users.json` — local user book with `user_id`, last message metadata, message count, latest context observation time, and the latest context token needed for direct CLI replies. Treat this file as local private runtime state.
- `context-guard.json` — private reminder configuration.
- `context-guard-state.json` — private per-user reminder cycle, schedule, and terminal outcome. Cycle IDs are hashes and never contain the raw context token.
- `media/YYYY-MM-DD/` — decrypted inbound media. Directories use mode `0700`; files use mode `0600`.

## Test Backend

`WECHAT_WIRE_FAKE=1` switches the bot factory to an in-process fake implementation. Fake messages are supplied with `WECHAT_WIRE_FAKE_MESSAGES_JSON`, allowing CLI and MCP tests to run without a real WeChat login. Media fixtures use `media_base64`, `file_name`, and `media_format` fields inside each fake message object.
