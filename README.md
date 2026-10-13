# BreakMock

A Go implementation of WireMock-style HTTP mocking, WebSockets, Server-Sent Events,
response templating, and administration. Built by Breaking-IT as a foundation for API simulation, test data,
and controlled failures.

**Implementation choice: Go.** Development continues on `main`. Go is the
maintained implementation for all new features, optimizations and releases,
including embedded Toxiproxy. The Rust prototype is retired and excluded from CI.
Its source is preserved only on the historical `codex/rust-port` branch; see
[the measured comparison](docs/GO_JOURNAL_OPTIMIZATION.md) for retained results.

**Development status:** a compatibility port under active construction, not a
complete drop-in replacement. The pinned reference is **WireMock 4.0.0-beta.40**. Upstream is a beta release;
BreakMock exposes its implemented messaging features by default, without a beta flag. See [COMPATIBILITY.md](docs/COMPATIBILITY.md)
for tested coverage and known gaps. We intentionally correct [five upstream bugs](docs/UPSTREAM_BUG_FIXES.md),
including matcher and JSON-template errors, so those cases differ from Java.
Performance and compatibility are separate
claims; neither is inferred from the implementation language.

## Why refactor WireMock to Go?

The goal is to keep WireMock's familiar mappings and APIs while reducing the
resources needed to run mock services in local development, CI and test
environments. In the latest Java-versus-Go measurements (2026-10-13, commit
`68cd311`):

- **Small responses** (a 17-byte body and a small template): BreakMock delivered
  **22–50% higher throughput**, used **23–34% less server CPU** at the same
  10,000 RPS and **90–92% less memory** (28–43 MiB versus 259–490 MiB).
- **Large JSON responses** (inline, body files, templates and gzip): the larger
  the body, the bigger the lead. At ~100 KiB BreakMock delivered **1.3–10.6× the
  throughput** with **54–96% less server CPU**; at ~1 MiB **3.1–86×** with
  **85–99% less**; at ~10 MiB **3.0–208×** with **92–99.5% less**, using
  0.4–0.9 GiB of memory where Java used 11.1–12.4 GiB. Responses were
  byte-identical to Java's.
- Median readiness was **10.7 ms for Go versus 600.7 ms for Java**.

These results are the basis for the refactor: lower resource use and faster
startup while retaining WireMock compatibility as the target. We chose Go as
our maintained implementation because it meets our efficiency goals and lets us
extend one executable with mocking, test-data and chaos capabilities, including
native integration with the Go-based Toxiproxy. See the [measurements and their
scope](#java-versus-go-benchmarks) below.

## Repository layout

```text
cmd/breakmock/       Standalone server executable
cmd/bmbench/         Benchmark load generator
pkg/breakmock/       Go server, matching, templates, admin APIs and tests
internal/           Supporting packages and embedded assets
examples/           Runnable mappings and chaos configurations
docs/               Usage, compatibility and benchmark reports
scripts/            Build support, benchmarks and compatibility checks
testdata/           Compatibility fixtures and recorded results
third_party/        Preserved dependency licenses and notices
```

The application is Go-only: no Java or Rust source or runtime is bundled.
Java is used only as an external WireMock reference for optional differential
checks and comparative benchmarks (including CI). Build and run BreakMock with Go
and the C compiler/zlib dependency described below. Applications embedding the
server import `github.com/Breaking-IT/BreakMock/pkg/breakmock`. This replaces
the former module-root import path. After editing the canonical license files,
run `python3 scripts/update_license_bundle.py` to refresh embedded notices.

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
[chaos testing recipes](docs/TOXIPROXY.md#chaos-testing-recipes).
See [Toxiproxy configuration and usage](docs/TOXIPROXY.md) for API examples, CLI
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

Measured **2026-10-13** on an Apple M4 Pro (14 logical CPUs, 48 GB), macOS arm64,
Go 1.27.1 and OpenJDK 27. Java is **WireMock 4.0.0-beta.40** (JAR SHA-256 checked
against `testdata/reference.json`) with default JVM settings. Go is BreakMock built
from a clean checkout of commit `68cd311`; every report records that commit, a
clean tree and the binary's SHA-256. The same Go load generator drives both servers
in alternating order with 32 persistent connections and checks every response body.
All runs had zero errors and zero dropped arrivals. Journals were disabled or
capped at 1,000 entries on both servers.

### Small responses

The workloads are a static 17-byte body and the template
`Hello {{request.query.name}}: {{math 2 '+' 3}}`.

Throughput at saturation, requests per second (two rounds, 3 s warmup, 5 s
measurement; higher is better):

| Request journal | Workload | Java | Go | Go vs Java |
| --- | --- | ---: | ---: | ---: |
| Disabled | Static | 72,580 | 108,579 | **+49.6%** |
| Disabled | Template | 82,988 | 105,756 | **+27.4%** |
| Capped at 1,000 | Static | 73,328 | 89,549 | **+22.1%** |
| Capped at 1,000 | Template | 72,037 | 87,565 | **+21.6%** |

Server CPU and memory at **the same 10,000 offered RPS** (three rounds, 10 s
warmup, 15 s measurement; lower is better). CPU is process user+system time per
completed request across all server threads, excluding the load generator.
Memory is the range of peak sampled RSS across rounds.

| Request journal | Workload | Java CPU µs/req | Go CPU µs/req | CPU saved | Java RSS MiB | Go RSS MiB |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Disabled | Static | 84.29 | 55.67 | **−34.0%** | 259.3–378.3 | 28.1–28.7 |
| Disabled | Template | 85.87 | 66.44 | **−22.6%** | 281.8–402.9 | 28.9–29.2 |
| Capped at 1,000 | Static | 122.42 | 86.49 | **−29.4%** | 372.2–429.5 | 42.1–42.6 |
| Capped at 1,000 | Template | 120.91 | 84.71 | **−29.9%** | 402.0–490.4 | 43.0–43.3 |

At saturation, Go also used 27–43% less CPU per request. Peak RSS at saturation
was 504–897 MiB for Java and 31–53 MiB for Go; idle RSS after startup was
176–200 MiB and 18–20 MiB. Mean p99 latency at 10,000 RPS was 2.5–3.8 ms for Go
and 2.0–6.1 ms for Java, so Go did **not** have lower tail latency at this
moderate rate; arrivals are dispatched in 1 ms batches, which dominates these
values.

### Large JSON responses

The workloads serve a ~100 KiB JSON document as inline `jsonBody`, inline `body`,
`bodyFileName`, templates (straight-line and full Handlebars with `jsonPath`) and
gzip. Three rounds, 2 s warmup, 5 s measurement, saturation. BreakMock before
this work (`51ac179`) ran in the same session:

| Request journal | Workload | Java RPS | Go before RPS | Go RPS | Go vs Java | Java CPU µs/req | Go CPU µs/req | Java peak RSS MiB | Go peak RSS MiB |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Disabled | jsonBody | 8,234 | 19,674 | 57,429 | **7.0×** | 1,012.9 | 83.2 | 3,631 | 34 |
| Disabled | body | 26,370 | 33,900 | 54,273 | **2.1×** | 318.3 | 81.1 | 3,271 | 34 |
| Disabled | bodyFileName | 13,966 | 27,221 | 54,505 | **3.9×** | 647.7 | 88.9 | 2,359 | 34 |
| Disabled | Template | 21,885 | 430 | 34,526 | **1.6×** | 381.0 | 151.7 | 3,145 | 40 |
| Disabled | Template file | 16,564 | 450 | 33,245 | **2.0×** | 537.4 | 160.9 | 2,915 | 41 |
| Disabled | Template + jsonPath | 21,136 | 459 | 28,312 | **1.3×** | 393.6 | 179.8 | 4,295 | 42 |
| Disabled | jsonBody, gzip | 8,245 | 13,142 | 87,070 | **10.6×** | 1,197.7 | 44.6 | 5,486 | 42 |
| Capped at 1,000 | jsonBody | 8,979 | 25,730 | 54,640 | **6.1×** | 1,034.7 | 97.2 | 5,656 | 392 |
| Capped at 1,000 | Template + jsonPath | 17,853 | 590 | 48,728 | **2.7×** | 455.4 | 118.4 | 6,329 | 440 |
| Capped at 1,000 | jsonBody, gzip | 8,143 | 14,221 | 79,141 | **9.7×** | 1,214.2 | 66.8 | 6,208 | 441 |

All 14 combinations, p99 latency and the measurement details are in the
[large JSON report](docs/LARGE_JSON_OPTIMIZATION.md). Every Go response was
byte-identical to Java's.

The lead grows with body size (journal disabled; Go throughput as a multiple of
Java's, and Go's CPU saving per request):

| Workload | ~100 KiB | ~1 MiB | ~10 MiB |
| --- | ---: | ---: | ---: |
| jsonBody | 7.0× (−92%) | 10.0× (−97%) | 15.7× (−98%) |
| body | 2.1× (−75%) | 3.7× (−90%) | 3.2× (−94%) |
| bodyFileName | 3.9× (−86%) | 3.1× (−87%) | 3.0× (−92%) |
| Template | 1.6× (−60%) | 4.5× (−86%) | 5.7× (−95%) |
| Template + jsonPath | 1.3× (−54%) | 4.4× (−85%) | 4.6× (−94%) |
| jsonBody, gzip | 10.6× (−96%) | 76× (−99%) | 208× (−99.5%) |
| Peak RSS, Java → Go | 2.3–5.4 GiB → 34–42 MiB | 3.0–5.0 GiB → 60–145 MiB | 11.1–12.4 GiB → 436–898 MiB |

At ~1 MiB and above, Go's static and template workloads reach ~10 GB/s through
loopback, the shared load generator's limit, so CPU per request is the fairer
comparison there. Details: [scaling with body size](docs/LARGE_JSON_OPTIMIZATION.md#scaling-with-body-size).

### What improved in this release

- **Large responses:** templated bodies of 64 KiB and more were re-parsed on every
  request, so Go was 30–51× *slower* than Java there; it is now 1.3–3.2× faster.
  Inline, file and gzip bodies use 39–94% less CPU than before, and peak RSS fell
  by about half or more.
- **Small responses** were already optimized and are unchanged: an in-process
  A/B of the exact workloads above (`BenchmarkSmallResponse`, alternating builds)
  is within ±2% ([raw output](testdata/reports/benchmark-small-ab.txt)).

### Scope

These are short, same-host measurements: the load generator shares the CPU, and
Java's fixed warmup does not prove JIT steady state. Results vary between runs on
this host (for example, Java's saturation RPS differed by up to 17% from a run the
day before), so compare implementations within one run, not across runs. Java ran
with its default heap (about a quarter of RAM), so its RSS shows what the JVM took,
not the minimum it needs. **The default unbounded journal was not benchmarked.**
RSS is sampled every 100 ms. Startup was measured separately on 15 fresh processes
per implementation with warm OS caches, loading a mapping from disk and returning a
templated response; median RSS at readiness was 206.0 MiB for Java and 16.6 MiB
for Go. Compatibility is tracked separately in [COMPATIBILITY.md](docs/COMPATIBILITY.md).

Raw results with commit, executable and JAR hashes:
[small responses](testdata/reports/java-go-2026-10-13) and large JSON at
[100 KiB](testdata/reports/benchmark-large-json.json),
[1 MiB](testdata/reports/benchmark-large-json-1mib.json) and
[10 MiB](testdata/reports/benchmark-large-json-10mib.json). Earlier measurements
are in [BENCHMARKS.md](docs/BENCHMARKS.md) and
[GO_JOURNAL_OPTIMIZATION.md](docs/GO_JOURNAL_OPTIMIZATION.md).

Reproduce:

```sh
# Small responses: saturation throughput, then CPU and memory at 10,000 RPS.
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go --rounds 2 --duration 5s --output /tmp/throughput.json
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go --rounds 2 --duration 5s --journal-limit 1000 --output /tmp/throughput-journal.json
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go --rate 10000 --warmup 10s --duration 15s --rounds 3 --output /tmp/cpu-10k.json
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go --rate 10000 --warmup 10s --duration 15s --rounds 3 --journal-limit 1000 --output /tmp/cpu-10k-journal.json

# Large JSON responses; list Java first so its responses are the byte reference.
# Add --size 1048576, or --size 10485760 --journal disabled, for the scaling runs.
go build -trimpath -o /tmp/breakmock ./cmd/breakmock
python3 scripts/json_benchmark.py --binary java=/tmp/wiremock.jar --binary go=/tmp/breakmock \
  --rounds 3 --duration 5s --warmup 2s --journal-check report --output /tmp/large-json.json
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
