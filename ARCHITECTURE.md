# Architecture

`wechat-wire` is a single binary that wraps the upstream WeChat iLink Bot Go SDK.

```
+----------------------------+
| wechat-wire CLI            |
|                            |
|  login/status/user/listen  |
|  mcp (stdio server)        |
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

The upstream SDK only allows proactive sends after it has a `context_token` for the target user. `wechat-wire` records the latest context token from incoming messages. CLI `msg send` uses the stored token through the SDK reply paths; MCP sends reuse the active SDK process and the stored token.

## Storage

Default config directory:

```text
$HOME/.config/wechat-wire/
```

Files:

- `credentials.json` — upstream SDK credentials.
- `users.json` — local user book with `user_id`, last message metadata, message count, and the latest context token needed for direct CLI replies. Treat this file as local private runtime state.
- `media/YYYY-MM-DD/` — decrypted inbound media. Directories use mode `0700`; files use mode `0600`.

## Test Backend

`WECHAT_WIRE_FAKE=1` switches the bot factory to an in-process fake implementation. Fake messages are supplied with `WECHAT_WIRE_FAKE_MESSAGES_JSON`, allowing CLI and MCP tests to run without a real WeChat login. Media fixtures use `media_base64`, `file_name`, and `media_format` fields inside each fake message object.
