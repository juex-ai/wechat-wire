# Context

## WeChat Session

A WeChat Session is the local runtime slice that combines the upstream iLink Bot adapter, SDK credentials, and the local User Book. It is responsible for logging in, remembering incoming messages, and sending replies with the latest observed context token.

## User Book

The User Book is the private local file that records observed WeChat users, last-message metadata, and the latest context token needed for direct replies. It is runtime state, not user-facing output.

## Context Cycle

A Context Cycle begins when an inbound user message supplies a context token. `wechat-wire` records that observation time and estimates the token expiry from the configured TTL. Outbound sends, including expiry reminders, do not refresh the cycle.

## Context Guard

The Context Guard is the optional persistent scheduler that sends at most one reminder per Context Cycle. It moves reminders earlier when their nominal time falls outside the configured daytime window and never catches up after that window has closed.
