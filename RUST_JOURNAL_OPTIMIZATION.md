# Rust journal optimization and fresh three-way comparison

Measured on 2026-10-12. This supersedes the initial journal-throughput findings in
[RUST_COMPARISON.md](RUST_COMPARISON.md). Results compare the existing optimized Go
implementation with the optimized Rust implementation and pinned Java WireMock
4.0.0-beta.40 on the same Apple M4 Pro host (14 logical CPUs).

## What this changes in the language decision

**The earlier throughput-based reason to prefer Go no longer holds.** Removing
journal work from the shared critical section raised Rust's journaled throughput
by 2.58–2.66 times. In this fresh run Rust is about 13% ahead of Go with journaling;
without journaling, static throughput differs by only 2.5% and template throughput
is effectively tied. With only two short throughput rounds, treat the smaller
percentages as observed differences, not proof of statistical significance.

At the same 10,000 RPS Rust uses 18% less CPU with journaling and 35–36% less without
it. Its RSS is lower at that matched rate. At saturation, however, journaled Rust
can consume as much or more RSS than Go: the template workload reaches 50–60 MiB
versus 46–47 MiB. Parallel event construction improves throughput but is not a
blanket memory reduction.

For a service whose main priority is runtime CPU/memory efficiency, **Rust is now
a strong candidate on measured performance**. Go retains a roughly fivefold
clean-build speed advantage (one local sample), a standard HTTP server and less
custom transport code to maintain. Rust's custom HTTP/WebSocket implementation,
remaining feature gaps and unproven long-running behavior still require scrutiny.
These results justify evaluating Rust seriously; they do not establish intrinsic
language superiority or production readiness. No new AI token-efficiency claim
is made: the initial port's reuse/confounding caveats still apply.

## Measured results

Mean saturation throughput, requests per second:

| Journal | Workload | Java | Go | Optimized Rust | Rust / Go |
| --- | --- | ---: | ---: | ---: | ---: |
| Disabled | static | 87,137 | 109,587 | 112,276 | 1.025× |
| Disabled | template | 88,910 | 105,522 | 105,399 | 0.999× |
| 1,000 entries | static | 82,685 | 81,627 | 92,533 | 1.134× |
| 1,000 entries | template | 81,178 | 79,344 | 89,528 | 1.128× |

Fresh old-Rust versus optimized-Rust comparison with journaling enabled:

| Workload | Old Rust mean RPS | Optimized Rust mean RPS | Improvement |
| --- | ---: | ---: | ---: |
| static | 35,918 | 92,533 | 2.58× |
| template | 33,704 | 89,528 | 2.66× |

The old binary was rerun on the same date and host, before the three-way run.
All runs are sequential; this is a before/after observation, not a randomized controlled study.

Mean CPU at **10,000 offered RPS**, microseconds per completed request:

| Journal | Workload | Java | Go | Optimized Rust | Rust reduction vs Go |
| --- | --- | ---: | ---: | ---: | ---: |
| Disabled | static | 94.67 | 52.40 | 33.42 | 36.2% |
| Disabled | template | 97.91 | 54.58 | 35.60 | 34.8% |
| 1,000 entries | static | 117.29 | 80.20 | 65.93 | 17.8% |
| 1,000 entries | template | 122.16 | 82.98 | 68.27 | 17.7% |

Peak sampled RSS ranges across the matched-rate rounds, MiB:

| Journal | Workload | Java | Go | Optimized Rust |
| --- | --- | ---: | ---: | ---: |
| Disabled | static | 279.8–374.2 | 25.8–27.0 | 10.3–10.4 |
| Disabled | template | 295.9–397.9 | 25.7–26.3 | 9.6–11.1 |
| 1,000 entries | static | 347.6–393.1 | 39.6–40.9 | 21.0–21.6 |
| 1,000 entries | template | 372.0–440.2 | 38.6–39.2 | 22.6–24.4 |

Peak sampled RSS ranges during saturation, MiB:

| Journal | Workload | Java | Go | Optimized Rust |
| --- | --- | ---: | ---: | ---: |
| Disabled | static | 488.0–619.2 | 29.0–29.2 | 10.6–10.6 |
| Disabled | template | 574.1–598.6 | 29.0–30.5 | 10.7–12.8 |
| 1,000 entries | static | 678.7–753.5 | 44.5–45.9 | 44.7–46.5 |
| 1,000 entries | template | 705.8–884.4 | 45.9–47.0 | 50.5–60.3 |

Process startup (15 samples each, warm OS caches):

| Implementation | Median readiness | Range | Median RSS at readiness |
| --- | ---: | ---: | ---: |
| java | 598.4 ms | 576.6–710.0 ms | 205.5 MiB |
| go | 11.9 ms | 7.3–44.8 ms | 16.7 MiB |
| rust | 8.3 ms | 7.4–22.4 ms | 11.2 MiB |

Build measurements (one sample each; source dependencies already downloaded):

| Implementation | Clean artifacts | No-change rebuild | Binary size |
| --- | ---: | ---: | ---: |
| go | 5.23 s | 0.171 s | 30.21 MiB |
| rust | 27.73 s | 0.229 s | 22.03 MiB |

All 60 three-way load measurements had zero errors/drops. The matched-rate runs checked 5,400,000 responses. The four old-Rust measurements are additional.

Raw data and compatibility/profile evidence are in [testdata/reports/rust-journal-optimized](testdata/reports/rust-journal-optimized). CPU, latency, RSS and every round are retained.

## What the profile showed and what changed

A native macOS `sample` capture under the capped-journal workload found 28,287
mutex-wait leaf samples out of 31,094 Tokio worker samples (91.0%). The corresponding
post-fix capture had 14,650 out of 31,752 (46.1%). These are wall-stack observations
including blocked threads, **not CPU-time percentages**. Sampling itself reduced
throughput substantially; only separate unprofiled runs are used for performance
claims. Some shared-state contention remains.

Rust previously held the shared state mutex while constructing complete journal
entries: timestamps, UUIDs, headers, body/base64 strings, response definitions and
mapping snapshots. It also recursively freed evicted JSON values under that lock.
Go already constructs journal entries before its insertion lock.

Rust now prepares an owned event outside the lock, briefly locks to recheck the
journal setting and current capacity and insert the entry, then drops evicted
payloads after unlocking. Normal eviction moves one value without allocating a
retirement collection. Journal-disabled requests avoid preparation and the final
journal lock. No response fields or journal contents were removed, sampled,
truncated, deferred to a background task or disabled for the benchmark.

Scenario matching and state transitions remain atomic. Entries are committed
before response handling finishes and before webhook dispatch. Newest-first order
is insertion order; concurrent requests can naturally finish in different orders.

## Compatibility and concurrency validation

All original Rust differential suites still pass except the previously documented
JVM identity diagnostic. That diagnostic includes a changing Java object identity;
it remains a strict failure, with no added normalization or exception. This does
not establish universal WireMock compatibility; the existing feature/dialect limits
remain documented in the initial comparison.

The Rust unit suite passes 27 library tests and five binary tests, including new
checks for commit ordering, current capacity, disabled commits, ownership of evicted
values and unchanged binary body/header/mapping snapshots.

A new concurrent journal regression runs against Java, Go and Rust with a capacity
of 64 and 16 clients, repeated three times below and above capacity. All 24 per-server
invariants pass, as do four exact admin status/body comparisons and two journal-body
byte comparisons after documented dynamic-scalar substitutions. It checks complete
capture below capacity, bounded retention above it, no duplicated IDs, sequential
retention order and visibility of completed scenario transitions. Concurrent event
order is deliberately not expected to match across independent processes.

The journal-body comparison preserves whitespace, property order, fixed UUIDs and
response bodies. Only generated event IDs, logged timestamps/ports, measured timing
values and the response Date header are substituted. These boundaries are explicit
in the saved report and test source.

Linux CI on Java 21 independently confirms the Rust suite and the new concurrent
journal checks, including identifier uniqueness across resets. See the
[CI run](https://github.com/Breaking-IT/BreakMock/actions/runs/38208942222) and
[saved CI summary](testdata/reports/rust-journal-optimized/linux-ci/run.json).
The Go baseline's previously known message-wait failures remain; Go's new journal
concurrency checks pass. The optimization does not claim to fix unrelated gaps.

## Unchanged benchmark method

- Static response and the same simple template: `Hello {{request.query.name}}: {{math 2 '+' 3}}`.
- Journals disabled, and separately capped at 1,000 entries; warmup fills the cap.
- Matched CPU: 10,000 offered RPS, 32 clients, 10-second warmup, 15-second measure,
  three rotating-order rounds per implementation.
- Saturation: 32 clients, three-second warmup, five-second measure, two rounds.
- Startup: 15 fresh processes per implementation, warm OS caches, readiness requires
  a correct disk-loaded templated response; 2 ms polling includes probe overhead.
- Clean-artifact build and unchanged rebuild: one sample each, dependencies already
  downloaded; Go default optimized build and Rust release thin LTO.

Server CPU includes all server threads, excludes the client, and is reported per
completed request. RSS is sampled every 100 ms. Every load response is checked;
errors/drops fail the driver, and fixed-rate achieved throughput must be within 2%.
Benchmarks run sequentially without local builds, profilers or test servers in
parallel. The client and unrelated desktop activity still share the host. Static
precedes template within each process, so template RSS includes retained memory.
Short measurements are observations, not statistical significance or production
capacity guarantees. Binary hashes identify the exact artifacts independently of
checkout metadata. The Go implementation was not modified in this optimization.

## Reproduce

Use the pinned, checksum-verified reference JAR described in `testdata/reference.json`.

```sh
cargo test --locked --manifest-path rust/Cargo.toml
python3 scripts/verify.py --jar /tmp/wiremock.jar --candidate-language rust
python3 scripts/profile_journal.py --binary rust/target/release/breakmock-rust --output-dir /tmp/rust-profile --label optimized
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go,rust --rate 10000 --warmup 10s --duration 15s --rounds 3 --output /tmp/cpu-no-journal.json
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go,rust --rate 10000 --warmup 10s --duration 15s --rounds 3 --journal-limit 1000 --output /tmp/cpu-journal.json
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go,rust --rounds 2 --output /tmp/throughput-no-journal.json
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go,rust --rounds 2 --journal-limit 1000 --output /tmp/throughput-journal.json
python3 scripts/startup_benchmark.py --jar /tmp/wiremock.jar --go-binary bin/breakmock --rust-binary rust/target/release/breakmock-rust --rounds 15 --output /tmp/startup.json
python3 scripts/build_benchmark.py --output /tmp/builds.json
```
