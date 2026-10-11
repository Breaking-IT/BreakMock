# WebSocket protocol adapter

Gorilla WebSocket v1.5.3, Copyright The Gorilla WebSocket Authors, BSD license
retained in LICENSE. BreakMock modifications:

- Optional protocol error formatter preserves Jetty close codes/reason strings.
- Reject a one-byte close payload as required by RFC 6455 section 5.5.1.
- An optional pre-handshake callback registers channels before clients observe HTTP 101.
- Separate text and binary message read limits match the reference configuration; compressed messages use the bounded decompressed reader in streaming.go.
- Optional nonempty challenge-key acceptance matches Jetty's handshake behavior.

The frame parser, masking, message fragmentation, control frames, compression,
and concurrency protections remain the upstream implementation.

The differential streaming corpus compares unchanged uncompressed server frames
and SSE event bytes. It also negotiates permessage-deflate and compares exact
close frames when decoded input crosses configured limits. Byte identity of
compressed outgoing data frames has not been established.
