# Context

## WeChat Session

A WeChat Session is the local runtime slice that combines the upstream iLink Bot adapter, SDK credentials, and the local User Book. It is responsible for logging in, remembering incoming messages, and sending replies with the latest observed context token.

## User Book

The User Book is the private local file that records observed WeChat users, last-message metadata, and the latest context token needed for direct replies. It is runtime state, not user-facing output.

