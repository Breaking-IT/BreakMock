# BreakMock

A Go implementation of WireMock-style HTTP mocking, WebSockets, Server-Sent Events,
response templating, and administration. Built by Breaking-IT as a foundation for API simulation, test data,
and controlled failures.

**Implementation choice: Go.** Development continues on `main`. Go is the
maintained implementation for all new
features, optimizations and releases, including embedded Toxiproxy. The Rust
prototype is retired and excluded from CI. Its source and comparison reports
remain as historical reference; see [the measured comparison](GO_JOURNAL_OPTIMIZATION.md).

**Development status:** a compatibility port under active construction, not a
complete drop-in replacement. The pinned reference is **WireMock 4.0.0-beta.40**. Upstream is a beta release;
BreakMock exposes its implemented messaging features by default, without a beta flag. See [COMPATIBILITY.md](COMPATIBILITY.md)
for tested coverage and known gaps. Performance and compatibility are separate
claims; neither is inferred from the implementation language.

## Why refactor WireMock to Go?

The goal is to keep WireMock's familiar mappings and APIs while reducing the
resources needed to run mock services in local development, CI and test
environments. In our latest measured Java-versus-Go workloads, optimized
BreakMock delivered **14–30% higher throughput**, used **32–45% less server CPU
at the same 10,000 RPS**, and had a much smaller resident memory footprint.
Median readiness was **10.7 ms for Go versus 600.7 ms for Java**.

These results are the basis for the refactor: lower resource use and faster
startup while retaining WireMock compatibility as the target. We chose Go as
our maintained implementation because it meets our efficiency goals and lets us
extend one executable with mocking, test-data and chaos capabilities, including
native integration with the Go-based Toxiproxy. See the [measurements and their
scope](#java-versus-go-benchmarks) below.

## Run

Requires Go 1.25 or newer, a C compiler, and zlib development headers (`zlib1g-dev`
on Debian/Ubuntu; supplied by the macOS SDK). CGO must be enabled for exact gzip
compression. No Java runtime is required to run BreakMock:

```sh
go build -trimpath -o bin/breakmock ./cmd/breakmock
./bin/breakmock --port 8080 --root-dir ./stubs
```

`stubs/mappings/` contains WireMock JSON mapping files; `stubs/__files/` contains
response body files. Mapping files may contain one mapping or a `mappings` array.
Message mappings load from `stubs/message-mappings/`, as individual mappings or a
`messageMappings` array.

```sh
curl -X POST http://localhost:8080/__admin/mappings \
  -H 'Content-Type: application/json' \
  -d '{"request":{"method":"GET","urlPath":"/hello"},"response":{"status":200,"body":"Hello {{request.query.name}}","transformers":["response-template"]}}'
curl 'http://localhost:8080/hello?name=Ada'
```

Supported command-line options include `--port`, `--bind-address`, `--root-dir`,
`--global-response-templating`, `--no-request-journal`, and
`--max-request-journal-entries`. The journal is unbounded by default, matching WireMock. Set
`--max-request-journal-entries 1000` for a bounded journal or disable it explicitly. Bind to `127.0.0.1` for local-only
use; the default listens on all interfaces. The admin API is unauthenticated.

A multi-stage Dockerfile is included. Its runtime is a native executable and CA
certificates; no Java runtime is required. Mount writable `/data` if using admin
persistence or recording persistence. The image runs as UID 65532.

## Embedded TCP fault injection

The Go executable includes Toxiproxy with shared configuration and a management
API available by default under `/__admin/toxiproxy` on the same HTTP listener.
No proxies or faults are created by default. Bandwidth and connection faults
affect only traffic routed through a proxy you configure; direct mock requests
keep their normal behavior. To start a configured chaos scenario:

```sh
./bin/breakmock --config examples/toxiproxy/breakmock.json
```

The example proxies port 8081 to BreakMock on port 8080 with downstream latency.
Use latency/jitter, bandwidth limits, timeouts, connection resets and byte limits
to test retries, deadlines, slow downloads and interrupted responses. See the
[chaos testing recipes](TOXIPROXY.md#chaos-testing-recipes).
See [Toxiproxy configuration and usage](TOXIPROXY.md) for API examples, CLI
precedence, reset isolation and lifecycle. Runtime proxy changes are not
persisted. MIT notices are retained and available through `--licenses`.

## WebSockets and SSE

Open a WebSocket channel with an HTTP mapping and attach message actions:

```json
{"request":{"urlPath":"/socket"},"response":{"openWebsocketChannel":true}}
```

POST the following to `/__admin/message-mappings` to echo incoming messages:

```json
{"trigger":{"type":"message"},"actions":[{"type":"send","message":{"body":"Echo: {{message.body}}"}}]}
```

Use `openSseChannel: true` for SSE. HTTP-triggered message mappings can push to
matching open channels using `channelTarget.type: "request-initiated"` and a
`requestPattern`; an optional `channelType` selects `websocket` or `sse`.
The `/__admin/channels` and `/__admin/messages` APIs provide channel management,
message journals, verification and server push. These features require no extra flag.

WebSocket limits use `--websocket-idle-timeout` (milliseconds),
`--websocket-max-text-message-size` and `--websocket-max-binary-message-size` (bytes).
Defaults match the reference: five minutes idle and 10 MiB per message.

## Test against the original Java server

```sh
go test -race ./...
go vet ./...
curl -fL https://repo.maven.apache.org/maven2/org/wiremock/wiremock-standalone/4.0.0-beta.40/wiremock-standalone-4.0.0-beta.40.jar -o /tmp/wiremock.jar
python3 scripts/verify.py --jar /tmp/wiremock.jar
```

The harness starts isolated Java and Go processes for every suite and checks the
reference version. It reuses unmodified upstream mapping fixtures and sends the
same mappings and requests to both implementations. Application response bodies
are compared as bytes. Admin response bodies are compared with exact formatting and property order,
with only nondeterministic scalar values (generated IDs, timestamps, timings and
local origins) substituted. Gzip response bytes are compared without decompression.
Every template case compares actual status and body bytes, including errors.
WebSocket frames, SSE events, fault socket output and status lines have separate
strict suites. Reference nondeterminism remains a reported strict failure; the
harness does not turn failures into passes. This is a
bounded differential corpus, not the entire upstream Java test suite.

## Java versus Go benchmarks

Latest measured comparison: **2026-10-12**, Apple M4 Pro (14 logical CPUs), macOS
arm64, Go 1.27.1 and OpenJDK 27. Java is **WireMock 4.0.0-beta.40**; Go is optimized
BreakMock at commit `7176b12`. Both servers use the same workload driver and
32 persistent clients. The workloads are a static response and the template
`Hello {{request.query.name}}: {{math 2 '+' 3}}`.

Mean throughput at saturation (higher is better), two rounds per workload with
3 seconds of warmup and 5 seconds of measurement:

| Request journal | Workload | Java RPS | Go RPS | Go improvement |
| --- | --- | ---: | ---: | ---: |
| Disabled | Static | 86,580 | 112,381 | +29.8% |
| Disabled | Template | 88,663 | 109,233 | +23.2% |
| Capped at 1,000 entries | Static | 77,532 | 92,829 | +19.7% |
| Capped at 1,000 entries | Template | 80,176 | 91,119 | +13.6% |

Resource use at **the same 10,000 offered RPS** (lower is better), three rounds
with 10 seconds of warmup and 15 seconds of measurement. CPU is mean process
user+system time per completed request across all server threads, excluding the
load generator. Memory is the range of peak sampled RSS across rounds.

| Request journal | Workload | Java CPU µs/request | Go CPU µs/request | Java RSS MiB | Go RSS MiB |
| --- | --- | ---: | ---: | ---: | ---: |
| Disabled | Static | 85.44 | 46.69 | 310.4–347.3 | 25.8–26.6 |
| Disabled | Template | 88.31 | 50.16 | 320.2–361.6 | 25.3–26.0 |
| Capped at 1,000 entries | Static | 113.33 | 74.64 | 334.6–364.6 | 37.7–39.9 |
| Capped at 1,000 entries | Template | 114.89 | 78.47 | 396.8–691.0 | 37.9–38.4 |

Startup measured 15 fresh processes per implementation with warm OS caches.
Readiness requires loading a mapping from disk and returning the correct templated
response. Median readiness was **600.7 ms for Java and 10.7 ms for Go**; median
RSS at readiness was **206.0 MiB and 16.6 MiB**, respectively.

All measured Java/Go load runs had zero errors or dropped arrivals, and matched-rate
runs achieved within 2% of the target. These are short, same-host measurements of
small workloads; the client shares the host, and Java's fixed warmup does not
prove JIT steady state. Journals were disabled or capped equally on both servers;
**the default unbounded journal was not benchmarked**. RSS is sampled every 100 ms.
These results predate subsequent admin compatibility fixes and Toxiproxy
integration, so they are not measurements of the current executable or active
chaos scenarios. Compatibility coverage is tracked separately in
[COMPATIBILITY.md](COMPATIBILITY.md).

See the [full measurement protocol and optimization report](GO_JOURNAL_OPTIMIZATION.md),
[raw results and executable hashes](testdata/reports/go-optimized), and
[historical benchmarks](BENCHMARKS.md). Reproduce the Java/Go workloads with:

```sh
# Saturation throughput, without and with a bounded journal.
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go --rounds 2 --duration 5s --output /tmp/throughput.json
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go --rounds 2 --duration 5s --journal-limit 1000 --output /tmp/throughput-journal.json

# CPU and memory at the same offered request rate.
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go --rate 10000 --warmup 10s --duration 15s --rounds 3 --output /tmp/cpu-10k.json
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go --rate 10000 --warmup 10s --duration 15s --rounds 3 --journal-limit 1000 --output /tmp/cpu-10k-journal.json
```

## License and provenance

Apache License 2.0; see [LICENSE](LICENSE) and [NOTICE.txt](NOTICE.txt).
The compatibility reference is WireMock commit
`0f5b90519fecfcc66accc5d6ddc884484fa1a1e1`. Upstream fixtures are retained under
`testdata/upstream/`. The modified Handlebars runtime in `internal/handlebars/`
retains Raymond's MIT license and documents local changes.

BreakMock is independent and is not affiliated with or endorsed by WireMock.
WireMock Cloud's proprietary implementation is not included. Additional test data
functionality and HTTP chaos extensions require independent implementation;
TCP fault injection is provided by the embedded MIT-licensed Toxiproxy.
