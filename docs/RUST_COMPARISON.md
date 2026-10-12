# Java, Go and Rust comparison

> Historical measurement report. Rust source and the original three-way scripts
> are preserved on [`codex/rust-port`](https://github.com/Breaking-IT/BreakMock/tree/codex/rust-port).
> Reproduction commands below describe that historical checkout. Current `main`
> contains the Go implementation and Java-versus-Go comparison tools.

**Historical initial-port measurements (2026-10-11).** The Rust journal bottleneck
has since been profiled and optimized. See the [updated fair comparison](RUST_JOURNAL_OPTIMIZATION.md)
for the current results and revised language decision; the measurements below
remain unchanged as historical evidence.

The `codex/rust-port` branch adds a native Rust implementation alongside Go.
Rust does not delegate requests to Java or Go. Both implementations target pinned
WireMock **4.0.0-beta.40**, upstream commit
`0f5b90519fecfcc66accc5d6ddc884484fa1a1e1`.

## Recommendation for BreakMock

**Keep Go as the main implementation for now.** In this experiment Go has the
best no-journal throughput, approximately matches Java with journals enabled,
and builds about 4.7 times faster than Rust from a clean artifact cache. Both
native ports start in milliseconds and use much less memory than Java.

Rust is attractive when memory and CPU at moderate request rates dominate: it
uses 29% less CPU than Go without journals and 6–8% less with journals at 10,000
RPS, and has the lowest RSS. But its journal-enabled saturation throughput is
only about 45% of Go's. With journals disabled it also delivers 10–18% less RPS
than Go. Lower CPU at one offered rate does not imply higher maximum capacity.

The Rust implementation constructs journal entries while holding a shared state
mutex, and matching also uses that mutex. This is a plausible contention source
from code inspection, not a profiled causal finding. It is an implementation
optimization opportunity; these measurements do not establish that Go is
intrinsically faster than Rust. Reworking this path and repeating the identical
benchmarks could change the decision.

Quality also prevents a blanket ranking: Go benefits from its standard HTTP
server, while Rust currently has more custom transport code to maintain. Rust
passes the new wait semantics cases that expose two Go bugs. Neither port has
universal byte equivalence or upstream WireMock's production history. Keep Rust
as a comparison/optimization branch; retain Java where complete upstream feature
support matters more than the resource savings demonstrated here.

## Measured runtime results

Mean server CPU at **10,000 offered RPS**, in microseconds per completed request:

| Journal | Workload | Java | Go | Rust | Rust reduction vs Go |
| --- | --- | ---: | ---: | ---: | ---: |
| Disabled | static | 85.20 | 45.20 | 31.93 | 29.4% |
| Disabled | template | 93.47 | 48.24 | 34.04 | 29.4% |
| 1,000 entries | static | 116.18 | 77.16 | 72.49 | 6.0% |
| 1,000 entries | template | 119.73 | 80.78 | 74.22 | 8.1% |

Peak sampled RSS range across those matched-rate rounds, in MiB:

| Journal | Workload | Java | Go | Rust |
| --- | --- | ---: | ---: | ---: |
| Disabled | static | 322.7–352.3 | 26.3–26.4 | 10.3–10.4 |
| Disabled | template | 339.6–362.0 | 26.7–27.1 | 9.5–12.8 |
| 1,000 entries | static | 400.9–415.7 | 38.9–39.4 | 20.3–20.4 |
| 1,000 entries | template | 407.8–495.9 | 38.5–39.9 | 23.6–23.8 |

Mean saturation throughput in requests/second (two rounds; client and server share the host):

| Journal | Workload | Java RPS | Go RPS | Rust RPS | Rust / Go |
| --- | --- | ---: | ---: | ---: | ---: |
| Disabled | static | 89,004 | 110,747 | 99,149 | 0.895× |
| Disabled | template | 89,092 | 112,925 | 92,182 | 0.816× |
| 1,000 entries | static | 82,995 | 83,570 | 37,593 | 0.450× |
| 1,000 entries | template | 82,046 | 81,157 | 36,397 | 0.448× |

Repeated process startup (15 samples each, warm OS caches):

| Implementation | Median readiness | Range | Median RSS at readiness |
| --- | ---: | ---: | ---: |
| java | 591.3 ms | 581.2–680.9 ms | 204.5 MiB |
| go | 12.3 ms | 7.5–42.8 ms | 16.6 MiB |
| rust | 8.6 ms | 5.7–22.7 ms | 11.2 MiB |

All 60 load measurements completed with zero errors or dropped requests. The matched-rate runs completed 5,400,000 checked responses.

Raw reports: [CPU, journal disabled](../testdata/reports/rust/benchmark-cpu-10k-three-way.json), [CPU, journal capped](../testdata/reports/rust/benchmark-cpu-10k-journal-three-way.json), [throughput, journal disabled](../testdata/reports/rust/benchmark-throughput-three-way.json), [throughput, journal capped](../testdata/reports/rust/benchmark-throughput-journal-three-way.json), [startup](../testdata/reports/rust/startup.json).

Benchmark binaries are frozen at Go `371ba5e` and Rust `3cd7d7f`; SHA-256 hashes are recorded in each report. The later Rust locale fix is not exercised by these workloads. Source-checkout revision fields alone should not be used to identify the benchmark binaries.

## Compatibility and quality

The original shared differential corpus passes for Go and Rust except the known binary
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
[testdata/reports/rust](../testdata/reports/rust).

Rust unit tests passed (24 library and five binary tests after the locale fix). Clippy completed with
style/performance suggestions; it is not warning-free. CI separately runs Go and
Rust against Java 21 on Linux; local measurements use Java 27 on macOS.
The Linux rerun at `c6d8a64` confirms all Rust suites except the identity case.
The added message-wait suite finds three failures in the frozen Go baseline
(two underlying semantics differences): the count endpoint returns all existing
events instead of capping at the requested count, including at zero timeout; the
single-event endpoint honors an irrelevant `count` and waits unnecessarily.
The latter returns the same body but at the wrong time. Rust passes all seven
wait cases and both disabled-journal cases.
See [CI run](https://github.com/Breaking-IT/BreakMock/actions/runs/38166939838).

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

## Development effort and token observations

The local build samples were **5.79 s for Go** and **27.46 s for Rust** with clean
artifact caches and pre-downloaded dependencies. No-change builds were **0.169 s**
and **0.230 s** respectively. The executables were 30.21 MiB (Go) and 21.89 MiB
(Rust), using the current build configurations, without a size-tuning exercise.
See [build measurements](../testdata/reports/rust/builds.json).

Observed AI usage across this chat and its three agents:

| Development window | Uncached input tokens | Cached input tokens | Output tokens |
| --- | ---: | ---: | ---: |
| Go work, 07:34–09:57 UTC | 3,120,976 | 184,632,448 | 520,350 |
| Rust work, 18:32–19:04 UTC | 823,081 | 61,550,336 | 175,328 |

These are local cumulative usage-counter differences, **not a billing statement**.
Cached input includes repeated context; reasoning tokens are already included in
output. The Rust window ends at the first published implementation checkpoint,
so later benchmark analysis and final documentation are excluded. Go's window
includes initial compatibility discovery, corpus creation, upstream-version
migration and earlier benchmarks. Rust reused that implementation knowledge,
fixtures and harness. The windows also differ in scope and elapsed time (about
143 minutes and 32 minutes). Therefore these observations **cannot establish that
Rust intrinsically needs fewer tokens or is faster to develop than Go**. A fair
language experiment would require equivalent starting material and repeated,
independent implementations. Java development effort was not measured.

Counters and scope are preserved in [Go usage](../testdata/reports/rust/go-development-tokens.json)
and [Rust usage](../testdata/reports/rust/rust-development-tokens.json). No conversation
content or private session-log paths are exported.

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
