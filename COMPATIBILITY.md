# Compatibility contract

Target: Java WireMock 3.13.2. This repository does **not** currently claim to accept
all possible mappings or reproduce all WireMock behavior byte-for-byte.

## Implemented and exercised

- HTTP mapping loading, priorities, newest-first ties, scenarios, inline/text/file/
  binary responses, headers, delays, faults, and basic proxying.
- Matching: URL/path/template, headers/query/forms/cookies, authentication, logical
  conditions, JSON equality/placeholders/JSONPath/schema, XML/XPath, multipart,
  common Java regex constructs and date/time conditions.
- Handlebars request context and common string, number, collection, date, JSON,
  XML, assignment and random helpers. Templates use WireMock's no-HTML-escaping
  behavior; JSON data preserves insertion order where tested.
- Administration: mappings CRUD/import/save/reset, metadata matching, scenarios,
  files, request journal/find/count/remove, settings, health/version, recording
  start/stop/status/snapshot and basic proxy record/replay.

Checked-in reports under `testdata/reports/` describe the actual bounded corpus.

| Suite | Result | Comparison |
| --- | ---: | --- |
| Original upstream fixtures | 9/9 | Response body bytes |
| Baseline workflows | 30/30 | 26 stub byte checks, 4 semantic admin checks |
| Request matchers | 138/138 | Status and body bytes, explicit negative fallback |
| Templates | 93/95 strict | 2 JVM diagnostic bodies remain different |
| Admin workflows | 51/51 | Formatting/order exact; nondeterministic scalar substitutions |
| Runtime/proxy | 43/43 | Body bytes and selected headers |
| Gzip | 99/99 | Encoded bytes and negotiation |

Some suites overlap. These counts describe test cases, not percentages of the
WireMock API or upstream test suite covered.
The fixture suite uses unmodified files from the original WireMock repository.

## What “byte-for-byte” currently means

Stub response bodies are compared without JSON normalization, including JSON key
order, whitespace, escaping and binary files. Suites also compare HTTP status;
some check selected headers. Negative matcher cases use an explicit fallback
stub, rather than pretending WireMock's unmatched diagnostic body is equivalent.

Admin tests preserve Jackson JSON whitespace and property order, including source
mapping order and captured request/proxy response header order. Only generated IDs,
timestamps, request origins and measured timings have scalar substitutions in the
test harness. Application values are not normalized. This is exact formatting with
specified nondeterministic values substituted, not literal equality of every byte
from independently running servers.

Gzip uses native zlib with the Java header/trailer format. Compressed bytes are
compared directly against the reference, including binary data through 1 MiB.
CGO and zlib are required; a CGO-disabled build reports a compression error instead
of silently producing a different stream. Different JDK/zlib versions may differ.

Two invalid templated-jsonBody cases check HTTP 500 but still have different JVM
HTML diagnostic bodies; they are not counted as strict body-byte passes.

The harness does not assert identical TCP framing, header order/capitalization,
Date headers, HTTP/2 frames, timing jitter or random samples. These are not the
same as literal network-byte equivalence.

## Known gaps and limits

- Custom Java extensions, transformers, matchers and webhook extensions cannot
  execute in this Go process. They need native equivalents.
- The version endpoint identifies BreakMock and deliberately differs from WireMock.
- Near-miss diagnostics and unmatched-request body formatting, full recording
  extraction/transformation options, full proxy/recording semantics and all admin
  endpoints are not complete. Unsupported implemented recording options return
  explicit errors.
- Java regex, Jayway JSONPath, XMLUnit and Java date-format dialects have edge
  cases beyond the tested subset. Equivalent third-party Go libraries alone do
  not prove compatibility.
- HTTPS keystores, TLS options, HTTP/2 parity, browser proxy mode and arbitrary
  status reason phrases are not implemented by the current CLI.
- Request journal timing values and some low-level transport details are not
  faithful measurements of Jetty's behavior.
- Advanced WireMock extensions, gRPC and GraphQL extensions, and WireMock Cloud
  features are outside the currently verified core.
- Journal capacity defaults to unbounded, matching WireMock; optional caps and
  journal disabling affect both memory use and verification/recording features.
- Exact inbound journal header order requires the provided HTTP/1 listener wrapper
  (enabled by the CLI). Embedding Server in another HTTP server without the wrapper
  loses this information. Header capture is bounded and does not cover HTTP/2.
- Proxy header order capture covers direct HTTP and HTTPS; HTTPS through a CONNECT
  proxy is not yet covered. Header case/framing on outgoing HTTP remains Go-native.

## Completion criteria

Full compatibility needs a much larger upstream-derived regression corpus,
explicit versioned mapping/API coverage, resolution of the gaps above and tests
against real downstream WireMock clients. Performance targets need representative
mapping counts, body sizes, concurrency levels, journaling and template workloads.
Passing this repository's current tests is not proof that those criteria are met.
