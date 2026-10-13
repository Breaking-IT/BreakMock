# Compatibility contract

Target: **Java WireMock 4.0.0-beta.40**, source commit
`0f5b90519fecfcc66accc5d6ddc884484fa1a1e1`. The standalone JAR SHA-256 is
`3a53dec6c814ff73da4abb0bc51624215ea2b913beec8d19aa6d54e4c62161d3`.
The upstream version is a beta. Implemented BreakMock features, including native
WebSockets and SSE, are available by default without beta gating.

This repository does **not** claim compatibility with every possible mapping,
extension or transport configuration. Checked-in differential reports document a
bounded corpus; a passing fixture is not proof of an entire feature's equivalence.

## Implemented and exercised

- HTTP mappings, priorities, insertion ordering, scenarios, text/JSON/binary/file
  responses, v4 entity objects and charsets, templating, delays, faults and proxying.
- Request matching including v4 `isOneOf`/`isNoneOf` methods and `QUERY`, numeric
  comparisons, JSONPath/schema/placeholders, XML/XPath, multipart, dates and common
  Java regex constructs. Near-miss matching distances and diagnostic formatting.
- Handlebars request/message models and string, number, collection, date, JSON,
  XML, assignment and random helpers. Upstream-derived fixtures retain provenance.
- HTTP admin mappings/import/save/reset, scenarios, files, request journals,
  settings, health/version, docs/recorder assets, recording and webhooks.
- Native WebSocket text/binary frames, fragmentation, ping/pong, close handling,
  malformed-frame errors, configurable independent text/binary limits and idle timeouts.
- Native SSE event framing, comments, IDs, event types, retry fields and multiline data.
- Message mappings, HTTP-request/HTTP-stub/incoming-message triggers, message
  templates, priority selection, originating-channel replies, channel selection,
  broadcasts, admin push, fixed in-memory channels/providers, message journals and
  verification. Message files load from `message-mappings/`. As in the reference,
  HTTP `/__admin/reset` does not clear message mappings, and `/mappings/save` saves
  HTTP mappings only.

The admin route inventory is in `testdata/admin_endpoint_coverage.json`.
The test runner rebuilds BreakMock, starts isolated Java/Go servers for each suite,
checks both version and implementation identity, and returns nonzero on any strict
failure. `testdata/reports/suites.json` records the consolidated run.

## Byte comparison boundaries

HTTP application bodies and gzip streams are compared as actual bytes, preserving
JSON order, whitespace, escaping and binary content. Every template case compares
status and body, including error diagnostics. Separate suites compare full
WebSocket data/control frames, SSE event bytes, fault socket bytes and HTTP status
lines. Header defaults are checked where stated by each suite; they are not
silently included in a broader claim.

Admin and streaming-journal tests preserve exact JSON formatting and property order.
They substitute only explicitly nondeterministic scalar values: generated IDs,
timestamps, measured timings and local origins. Fixed IDs and application values
are preserved. This is formatting equivalence with those substitutions, **not**
literal equality of every byte from two independent server processes.

One diagnostic contains a value that no other process can reproduce: for a
binary matcher mismatch, WireMock prints the actual body as Java's default
`byte[]` string, `[B@` followed by a per-object JVM identity hash (for example
`[B@6b9cd557`). Identical requests to the same Java server produce different
identities. BreakMock prints the same form with a random identity. For this one
case the suite replaces the `[B@<hex>` token **and its padding** with a placeholder
of the same width on both sides, requires exactly one such token on each side, and
compares every other byte, including column alignment. BreakMock builds that print
anything else (earlier builds printed base64) still fail. See
[COMPATIBILITY_FIXES.md](COMPATIBILITY_FIXES.md) for the evidence that Java's value
is non-deterministic.

The harness does not claim universal TCP/HTTP header order, chunk boundaries,
Date values, HTTP/2 frames, scheduling jitter or random-output equivalence.
WebSocket handshake headers differ in some transport defaults; frame comparisons
are explicit. Outbound webhook transport headers are reported separately.

Gzip uses native zlib and the v4 Jetty header/trailer layout. CGO and zlib are
required. DEFLATE bytes can depend on the reference JDK/zlib version. Localized date
format data is captured from the reference JDK and documented with its generator;
locale data can change between JDK releases.

## Remaining limits

- Java extensions cannot execute inside Go. Custom transports/channel providers,
  arbitrary Java transformers/matchers and programmatically populated JVM data
  stores need native equivalents. File-backed `__files` references are supported.
- Java regex, Jayway JSONPath, XMLUnit and date/locale dialects have edge cases beyond
  the corpus. Random values and real-time helpers do not share a Java process's RNG/clock.
- HTTPS keystores, configured JKS CA export, TLS configuration, HTTP/2 parity and
  browser proxy mode are not implemented by the current CLI.
- Multi-channel iteration order depends on generated UUIDs and differs between runtimes.
- Full proxy/recording behaviors, extension-specific endpoints and all malformed
  input error responses are not exhaustively covered.
- SSE peer disconnects follow the reference: a channel may remain until a failed
  send or explicit admin close. Idle disconnected channels can retain resources.
- HTTP journal timings measure Go's response path, not Jetty scheduling. Default
  journals are unbounded; disabling/capping journals affects verification and memory.
- HTTP/1 header capture and status reason compatibility require the supplied
  listener wrapper (enabled by the CLI). Embedding without it loses those details.
- WireMock Cloud proprietary code and extensions such as gRPC/GraphQL are not included.
  Future test-data and chaos features require independent implementation.
- HTTP request header values are decoded as ISO-8859-1, one character per byte, as
  Jetty does. Matching, templates, cookies and the journal therefore see a UTF-8
  `é` sent in a header as `Ã©`, exactly like WireMock.
- Template syntax errors are not yet Java-compatible. Handlebars.java renders some
  malformed input as literal text (for example an unclosed `{{foo` or a single `}`),
  where BreakMock returns an error, and its error messages use a different format
  (`[ERROR] line:col: found: …, expected: …`). Well-formed templates are unaffected.
- Intentional divergence: after a templated body file is edited, WireMock keeps
  serving its cached compiled template until the mapping changes. BreakMock serves
  the edited file immediately.
- Request journals differ for templated responses (WireMock logs the rendered body
  in `responseDefinition`) and for gzip responses (WireMock logs the uncompressed
  body). Served bytes are identical.
- Runtime comparisons cover two small workloads. See `BENCHMARKS.md` for the latest
  optimized Go/Rust results and their limits; broader workloads remain unmeasured.

## Results

See the individual JSON reports for case-level inputs, failures and comparison
rules. Suite counts describe test cases, not a percentage of WireMock coverage.

Consolidated local Go and Rust runs (2026-10-12), retained under
`testdata/reports/compatibility-fixes/`:

| Suite | Passing / total | Comparison |
| --- | ---: | --- |
| Original upstream mappings | 9/9 | HTTP response bytes |
| Baseline workflows | 30/30 | 26 body checks, 4 semantic admin checks |
| Request matchers | 434/434 | Status and response bytes |
| Templates | 429/429 | Status and response bytes, including errors |
| HTTP admin | 130/130 | JSON bytes with explicit scalar substitutions |
| Runtime/proxy | 46/46 | Body bytes and selected headers |
| Gzip | 99/99 | Compressed bytes and negotiation |
| Diagnostics | 41/41 | Exact bytes; one JVM identity token masked width-for-width |
| Near-miss distances | 140/140 | Exact admin response bytes |
| Webhooks | 7/7 | Configured callback content; transport headers separate |
| Faults | 32/32 | Complete socket bytes and reset behavior |
| Entity bodies | 34/34 | Status/body bytes, charsets, error HTML |
| Messaging admin | 79/79 | JSON bytes with explicit scalar substitutions |
| WebSocket/SSE | 51/51 + 2/2 | Exact frame/event bytes + separate lifecycle checks |
| Status lines | 17/17 | Status-line and body bytes |
| Streaming journals | 20/20 | JSON bytes with explicit scalar substitutions |
| Message waits, enabled journal | 15/15 | Existing scalar substitutions; new error cases use exact bytes |
| Message waits, disabled journal | 5/5 | Existing origin substitution; new precedence cases use exact bytes |
| Concurrent journal | 16 invariants + 3 byte comparisons | Two exact admin responses; one with generated scalar substitutions |
| Templates inside 80 KB bodies, 3 requests each | 429/429 | Status and response bytes, including errors |
| Dynamic templates, edits and journal integrity | 238/238 | Exact bytes per request, at 2 sizes; random/time helpers by property |

The last two rows were run on 2026-10-12 against Java with `template_differential.py --pad 80000 --repeat 3`
and `dynamic_template_differential.py`. That suite checks values that vary by request,
random and time helpers, mapping/file/jsonBody edits with gzip, raw non-ASCII header
bytes, and that journal entries hold the body served to each of 120 concurrent requests.
Its `--discover` mode adds the known template syntax-error cases.

`go test -race ./...` and `go vet ./...` pass, and the strict differential runner
passes every suite (2026-10-13).
Outgoing compressed WebSocket frame byte identity is not established; incoming
compressed-message limits are exercised against Java.
