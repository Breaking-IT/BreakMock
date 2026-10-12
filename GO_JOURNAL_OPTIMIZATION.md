# Go journal optimization and fresh Java/Go/Rust comparison

Measured on 2026-10-12 on the same Apple M4 Pro (14 logical CPUs). This supersedes
the runtime comparison in [RUST_JOURNAL_OPTIMIZATION.md](RUST_JOURNAL_OPTIMIZATION.md).
Java is pinned WireMock 4.0.0-beta.40. Rust is the previously optimized executable,
unchanged byte for byte (SHA-256 recorded in the manifest and each load report).
Go implementation and regression tests are frozen at `7176b12`.

Go's journaled throughput improved by **8.7–14.7%**, while CPU at the same 10,000 RPS
fell **10.0–11.2%** versus the fresh old-Go baseline. Fixed-rate RSS improves only
modestly; lower allocation volume is not an equivalent reduction in resident memory.
Across these workloads Go uses approximately **32–45% less CPU than Java**.

Go leads the throughput means in this run, by 2–7% over Rust. These short runs do
not establish a decisive throughput winner. Rust still uses 10–12% less CPU with
journaling and 34–35% less without it, and less RSS at the matched rate. Rust's
median startup is 8.1 ms versus Go's 10.7 ms; both are far below Java's 600.7 ms.
At saturation, journaled RSS overlaps substantially between Go and Rust. The choice
remains workload-dependent: this pass narrows Rust's journaled CPU advantage and
improves Go's throughput, without removing Rust's measured resource advantages.

## Results

Mean saturation throughput, requests per second; higher is better:

| Journal | Workload | Java | Optimized Go | Optimized Rust |
| --- | --- | ---: | ---: | ---: |
| Disabled | static | 86,580 | 112,381 | 105,452 |
| Disabled | template | 88,663 | 109,233 | 107,010 |
| 1,000 entries | static | 77,532 | 92,829 | 89,028 |
| 1,000 entries | template | 80,176 | 91,119 | 85,426 |

Mean server CPU at **10,000 offered RPS**, microseconds per completed request;
lower is better. This includes all server threads and excludes the load generator.

| Journal | Workload | Java | Optimized Go | Optimized Rust |
| --- | --- | ---: | ---: | ---: |
| Disabled | static | 85.44 | 46.69 | 30.67 |
| Disabled | template | 88.31 | 50.16 | 32.51 |
| 1,000 entries | static | 113.33 | 74.64 | 66.84 |
| 1,000 entries | template | 114.89 | 78.47 | 68.89 |

Peak sampled RSS ranges across matched-rate rounds, MiB; lower is better:

| Journal | Workload | Java | Optimized Go | Optimized Rust |
| --- | --- | ---: | ---: | ---: |
| Disabled | static | 310.4–347.3 | 25.8–26.6 | 10.3–10.4 |
| Disabled | template | 320.2–361.6 | 25.3–26.0 | 10.5–12.5 |
| 1,000 entries | static | 334.6–364.6 | 37.7–39.9 | 19.8–22.6 |
| 1,000 entries | template | 396.8–691.0 | 37.9–38.4 | 23.0–23.6 |

Peak sampled RSS ranges during saturation, MiB:

| Journal | Workload | Java | Optimized Go | Optimized Rust |
| --- | --- | ---: | ---: | ---: |
| Disabled | static | 490.1–643.7 | 28.0–28.2 | 10.6–10.7 |
| Disabled | template | 510.5–560.3 | 29.0–29.5 | 10.9–12.8 |
| 1,000 entries | static | 761.4–862.5 | 46.9–53.1 | 33.5–61.5 |
| 1,000 entries | template | 861.5–907.5 | 47.9–50.7 | 36.9–57.3 |

Fresh-process startup, 15 samples each, warm OS caches. Readiness requires loading
a mapping from disk and returning the correct templated response.

| Implementation | Median readiness | Range | Median RSS at readiness |
| --- | ---: | ---: | ---: |
| java | 600.7 ms | 580.9–662.0 ms | 206.0 MiB |
| go | 10.7 ms | 9.3–16.2 ms | 16.6 MiB |
| rust | 8.1 ms | 7.5–24.7 ms | 11.2 MiB |

All 60 main load measurements had zero errors and dropped arrivals. The matched-rate
runs checked 5,400,000 response bodies. Ten additional baseline-Go measurements
also had zero errors/drops. Every fixed-rate run achieved within 2% of 10,000 RPS.
See [raw rounds, latency, profiles and validation](testdata/reports/go-optimized).

## Go before and after

The old Go implementation was rebuilt from an archive of `ab2cd8e` and rerun on the
same date/host, with identical settings. Both versions use the 1,000-entry journal.

| Workload | RPS before → after | Throughput change | CPU µs/request before → after at 10k RPS | CPU reduction |
| --- | ---: | ---: | ---: | ---: |
| static | 85,412 → 92,829 | +8.7% | 82.98 → 74.64 | 10.0% |
| template | 79,444 → 91,119 | +14.7% | 88.33 → 78.47 | 11.2% |

These are sequential before/after observations, not interleaved controlled runs.
Host activity and runtime variability contribute to differences; short throughput
rounds do not establish statistical significance.

## What changed and why

- Messaging-store initialization uses `sync.Once`: ordinary requests no longer
  take the shared journal mutex just to retrieve the initialized store. Empty
  message-mapping lists also skip sorting, preserving the same empty JSON array.
- The bounded journal uses a ring once full. It overwrites the oldest entry without
  periodically allocating/copying the retained backing slice. Admin removal and
  capacity changes restore chronological storage as needed. Newest-first reads,
  reset behavior, delayed cap shrink and immutable snapshots remain unchanged.
- Request metadata maps are presized; discarded empty maps and a redundant
  header-order copy are removed. Serialization metadata is built outside the lock.
- UUIDs use fixed-buffer hexadecimal encoding with the same UUIDv4 shape.
- Empty HTTP request bodies skip `io.ReadAll`'s initial buffer, while preserving
  the nonnil empty-body value needed by body matchers. The request URI is built once.

No journal records or response fields are removed, sampled, delayed or omitted.
The same default HTTP transport and compression implementation are retained.
Header casing, property order, gzip bytes and journal ownership remain covered
by differential and race checks.

The isolated journal microbenchmark went from approximately 8,504 to 7,008 bytes
and 85 to 74 allocations per insertion. In the real-network profiling wrapper,
allocated bytes per completed request decreased from
13,472 to 11,447
(15.0%);
allocation count went from 135.1 to
122.1. These include small profiling/probe overheads.
Messaging disappears from the contended-lock profile; journal insertion remains a
contention source. Mutex delay is summed across waiting goroutines, not CPU time.
Instrumented throughput is excluded from all performance tables above.

## Compatibility and limits

The results below describe the benchmark-time code. Subsequent
[message-wait fixes](COMPATIBILITY_FIXES.md) remove the five Go wait failures and
add negative-count coverage for both languages. The strict JVM-identity diagnostic
remains failing; the benchmark measurements here were not rerun for those admin fixes.

The full Go differential run has **no new failures** against the baseline. Gzip
passes 99/99, templates 429/429 and admin 130/130. Unit/race tests and `go vet` pass.
New tests cover wrapped journal storage, resizing, removal, reset, disable,
subevents, snapshot isolation, concurrent messaging initialization and empty-body
matcher behavior. Concurrent Java/Go journaling passes 16 invariants, two exact
admin-byte comparisons and one journal-byte comparison with documented generated
scalar substitutions.

Universal byte-for-byte compatibility remains incomplete: 16 of 19 suite invocations
pass. Existing failures are one JVM object-identity diagnostic, three enabled
message-wait cases and two disabled-journal message-wait cases. The archived old Go
binary independently reproduces all five wait failures. No tests were weakened or
new normalizations added. Journal comparisons retain whitespace/property order but
substitute generated IDs, timestamps, ports, durations and response Date, as before.
Full details are in the [validation summary](testdata/reports/go-optimized/compatibility/README.md).
Rust source and executable are unchanged from the prior validated comparison;
its previously documented gaps also remain.

[Linux CI for the implementation commit](https://github.com/Breaking-IT/BreakMock/actions/runs/38213035151)
independently passes Go race tests, vet and the container build, plus Rust formatting,
unit tests and Clippy. Both implementations pass the journal concurrency suite.
The workflow remains red because of the same existing differential failures:
six Go cases and Rust's JVM-identity diagnostic. Saved step and failure evidence is
in [the CI summary](testdata/reports/go-optimized/linux-ci/summary.json).

## Unchanged measurement protocol

- Static response and template `Hello {{request.query.name}}: {{math 2 '+' 3}}`.
- Journals disabled, then separately capped at 1,000 entries; warmup fills the cap.
- Saturation: 32 clients, 3-second warmup, 5-second measure, two rotating-order rounds.
- Matched CPU: 10,000 offered RPS, 32 clients, 10-second warmup, 15-second measure,
  three rotating-order rounds. CPU is the cumulative process user+system delta.
- Startup: 15 fresh processes per implementation, warm OS caches, 2 ms polling.
- RSS sampled every 100 ms. Static precedes template within each process, so template
  RSS includes retained memory from static requests.
- No local test servers, profilers or builds run concurrently with measurement.
  The benchmark client and unrelated desktop processes share the host.
- Two saturation rounds do not fully balance three server positions; fixed-rate
  and startup rounds are balanced. Java's fixed warmup does not prove JIT steady state.
  Startup includes launch/probe overhead, which is material for fast native processes.

Go 1.27.1, Rust 1.98.1 and Java 27, matching the preceding comparison. The benchmark
and startup scripts are unchanged. Executable hashes identify what was measured;
checkout dirty flags can reflect uncommitted reports, not runtime changes. Build
time and token-efficiency experiments were not rerun; this pass makes no new claims
about either, nor about general language superiority or production readiness.

## Reproduce

```sh
python3 scripts/verify.py --jar /tmp/wiremock.jar --output-dir /tmp/go-compat
python3 scripts/profile_go_journal.py --output-dir /tmp/go-profile
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go,rust --rounds 2 --output /tmp/throughput-no-journal.json
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go,rust --rounds 2 --journal-limit 1000 --output /tmp/throughput-journal.json
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go,rust --rate 10000 --warmup 10s --duration 15s --rounds 3 --output /tmp/cpu-no-journal.json
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --servers java,go,rust --rate 10000 --warmup 10s --duration 15s --rounds 3 --journal-limit 1000 --output /tmp/cpu-journal.json
python3 scripts/startup_benchmark.py --jar /tmp/wiremock.jar --go-binary bin/breakmock --rust-binary rust/target/release/breakmock-rust --rounds 15 --output /tmp/startup.json
```
