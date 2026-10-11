# Java, Go and Rust comparison

The `codex/rust-port` branch adds a native Rust implementation alongside Go.
Rust does not delegate requests to Java or Go. Both implementations target pinned
WireMock **4.0.0-beta.40**, upstream commit
`0f5b90519fecfcc66accc5d6ddc884484fa1a1e1`.

## Compatibility and quality

The same local differential corpus passes for Go and Rust except the known binary
mismatch diagnostic that includes a changing JVM object identity (`[B@...`). The
strict runner deliberately fails on that case; this branch does not claim a green
strict suite or universal compatibility. Generated IDs, timestamps, timings and
server origins are normalized where the existing harness documents them. Remaining
JSON formatting and gzip streams are compared as bytes.

Coverage includes 434 matcher cases, 429 templates, 130 HTTP admin cases, 46 runtime
cases, 99 gzip cases, 140 matcher distances, 40/41 diagnostic cases, 7 webhooks,
32 faults, 34 entities, 79 messaging cases, 51 strict streaming cases plus two
lifecycle checks, 17 HTTP statuses and 20 streaming-journal cases. Baseline and
upstream disk-mapping fixtures also pass. An additional seven asynchronous message
wait checks and two disabled-journal checks pass for Rust. Reports are in
[testdata/reports/rust](testdata/reports/rust).

Rust unit tests passed (23 library and five binary tests). Clippy completed with
style/performance suggestions; it is not warning-free. CI separately runs Go and
Rust against Java 21 on Linux; local measurements use Java 27 on macOS.

Important implementation limits remain beyond this bounded corpus:

- The Rust HTTP/1 and WebSocket transport is manually implemented over Tokio.
  Its protocol maturity is not established by these tests. Go uses `net/http`;
  upstream Java uses Jetty. Neither port inherits upstream's full ecosystem.
- Rust holds a global state lock while selecting/matching stubs. Complex regular
  expressions and large mapping sets can serialize traffic. Simple-stub benchmarks
  do not measure this cost, proxying, large payloads or all template helpers.
- Explicit JSON Schema version options and arbitrary Java date-format tokens are
  not fully reproduced. Java extension JARs, an HTTPS listener and HTTP/2 are not
  implemented in Rust.
- Rust applies a 64 MiB request/message ceiling and bounded outbound queues of 64
  messages. These protective limits differ from unlimited configurations upstream.
- HTTPS proxy/webhook header casing is not preserved by the fallback HTTP client.
  Dribble delays currently delay the complete body rather than reproducing chunk
  timing. Outgoing compressed WebSocket frame bytes are not established equivalent.
- Lone UTF-16 surrogate JSON bodies are preserved for individual mapping creation
  and disk loading, but bulk-import/persistence edge cases need further coverage.

These are properties of these implementations, not intrinsic language limitations.
A representative production mapping corpus, long-running load tests, malformed
protocol fuzzing and dependency/security review are needed before deployment.

## Measurement method

All three implementations run sequentially on the same Apple M4 Pro host with 14
logical CPUs. The Go load generator shares the host and verifies response bodies.
Two workloads are used: a fixed plain-text response and
`Hello {{request.query.name}}: {{math 2 '+' 3}}`. Both ports optimize this simple
request interpolation; it does not represent arbitrary template complexity.

Matched-rate CPU uses 10,000 offered RPS, 32 persistent clients, 10 seconds of
warmup and 15 seconds measured per workload, three rounds per server. CPU is
process user+system time across all threads, excluding the client. 100% means one
core. Arrivals are batched at 1 ms; latency includes scheduled-arrival queue delay.
Errors, drops or achieved RPS more than 2% from target invalidate a run.

Saturation uses the earlier benchmark settings: 32 clients, three-second warmup,
five-second measurement and two rounds. Server ordering rotates each round.
Journals are tested disabled and capped at 1,000 entries. RSS is sampled every
100 ms and is not equivalent to heap allocations. These short local runs are
observations, not a statistical significance test or a production capacity promise.

Startup measures fresh processes with warm OS caches, from process launch through
a correct response from a disk-loaded templated mapping. Polling is every 2 ms;
results include launch and HTTP-probe overhead. This is not cold-machine startup.

Clean build timings use separate empty artifact caches with dependencies already
downloaded, followed by a no-change rebuild. They are one local sample each.
Java is the published upstream JAR; its source-build time is not measured.

## Reproduction

```sh
cargo test --locked --manifest-path rust/Cargo.toml
python3 scripts/verify.py --jar /tmp/wiremock.jar --candidate-language rust
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go,rust --rate 10000 --warmup 10s --duration 15s --rounds 3 --output testdata/reports/rust/benchmark-cpu-10k-three-way.json
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go,rust --rate 10000 --warmup 10s --duration 15s --rounds 3 --journal-limit 1000 --output testdata/reports/rust/benchmark-cpu-10k-journal-three-way.json
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go,rust --rounds 2 --output testdata/reports/rust/benchmark-throughput-three-way.json
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go,rust --rounds 2 --journal-limit 1000 --output testdata/reports/rust/benchmark-throughput-journal-three-way.json
python3 scripts/startup_benchmark.py --jar /tmp/wiremock.jar --go-binary bin/breakmock --rust-binary rust/target/release/breakmock-rust --rounds 15
python3 scripts/build_benchmark.py
```
