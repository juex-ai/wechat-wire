---
name: wechat-wire-localtest
description: Use when feature development, bugfix, or refactoring is complete in the project and code needs validation. Build the binary, run unit tests, and run fake-backend MCP E2E tests autonomously.
metadata:
  internal: true
---

# WeChat Wire Local Test

After completing any code change, build the binary, run affected unit tests, and run the fake-backend E2E suite. Do not ask the user; the scripts are idempotent and do not contact real WeChat.

## Execution Steps

1. **Build** — `bash scripts/build.sh`
2. **Run unit tests** — `( cd cli && go test -v ./... )`
3. **Run E2E tests** — `./tests/run.sh all`
   - Uses `WECHAT_WIRE_FAKE=1`
   - Verifies direct CLI `listen --once` plus `msg send`
   - Verifies CLI and MCP both cross the WeChat Session seam for context-backed sends
   - Starts `bin/wechat-wire mcp`
   - Verifies incoming fake messages become MCP notifications
   - Verifies inbound media is saved privately and notifications contain a readable absolute path
   - Verifies MCP tools can list users and send a fake reply

## Failure Handling

- If build fails, fix compilation errors first.
- If unit tests fail, fix them before running E2E.
- If E2E fails, inspect test logs, fix the MCP or fake backend path, rerun the failing suite, then rerun `./tests/run.sh all`.
