# Benchmark measurements

The Rust comparison branch also provides a [three-way Java/Go/Rust report](RUST_COMPARISON.md),
with matched-rate CPU, throughput, memory, startup and development-effort measurements.
The historical two-way results below are retained; compare languages using results
from the same run set rather than mixing measurements from different sessions.

Measured on 2026-10-11 against **WireMock 4.0.0-beta.40** using the current v4 port.
Two alternating-order rounds per server, 32 persistent clients, a three-second
warmup and five-second measurement per workload. Every run reported zero errors.
The same local Go load generator drove both servers and checked response bodies.

These are small same-host workloads, not a production capacity guarantee. The
client shares CPU with the server. RSS is sampled every 100 ms; Java heap growth,
GC, JIT warmup, body sizes, mapping counts and journal settings affect results.

| Journal | Workload | Java mean RPS | BreakMock mean RPS | Go / Java | Java peak RSS MiB | BreakMock peak RSS MiB |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Disabled | static | 89,569 | 112,340 | 1.254× | 485–636 | 29–29 |
| Disabled | template | 89,611 | 109,591 | 1.223× | 501–646 | 31–31 |
| Capped at 1,000 | static | 83,063 | 83,969 | 1.011× | 762–769 | 44–44 |
| Capped at 1,000 | template | 83,171 | 81,220 | 0.977× | 911–926 | 45–45 |

Without journals, BreakMock delivered about 22–25% more RPS. With journals capped
at 1,000, throughput was close: approximately 1% faster for static responses and
2% slower for this template workload. Lower memory use is established in these
runs; a significant throughput improvement is **not** established for journaled
workloads.

The template workload is `Hello {{request.query.name}}: {{math 2 '+' 3}}`.
It exercises the optimized deterministic template path, not the full helper suite.
Memory ranged from 29–45 MiB for BreakMock and 485–926 MiB for Java across measured
workloads. Startup includes process launch and readiness polling, so sub-second
startup values are only coarse observations.

Platform: macOS arm64, Go 1.27.1, OpenJDK 27. Runtime details and the pinned JAR
checksum are recorded in [no-journal results](testdata/reports/benchmark-v4.json)
and [bounded-journal results](testdata/reports/benchmark-v4-journal.json).

Reproduce:

```sh
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --rounds 2 --duration 5s --output testdata/reports/benchmark-v4.json
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --rounds 2 --duration 5s --journal-limit 1000 --output testdata/reports/benchmark-v4-journal.json
```

Earlier v3.13.2 measurements are retained in `benchmark.json` and
`benchmark-journal.json` for history; they describe a different revision/reference.

## CPU at the same request rate

At **10,000 offered RPS**, BreakMock used **31% less server CPU without journals**
and **19–21% less with journals capped at 1,000** in these workloads. This is a
matched-rate comparison, separate from the maximum-throughput measurements above.

Three fresh-process rounds per server on Apple M4 Pro (14 logical CPUs), macOS
arm64, Go 1.27.1 and OpenJDK 27; WireMock 4.0.0-beta.40. Each workload has a
10-second warmup followed by a 15-second measurement, using 32 persistent clients.
Server order alternates each round. Every measured run completed 150,000 requests,
with **zero errors or dropped requests** and achieved approximately 9,998–9,999 RPS.
All response bodies are checked, not just status codes.

The table reports means of the three rounds. CPU percentage is relative to **one
core** (100% = one fully occupied core), not a percentage of all 14 cores.

| Journal | Workload | Java CPU | Go CPU | Java CPU µs/request | Go CPU µs/request | CPU reduction |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Disabled | static | 104.1% | 71.5% | 104.33 | 71.62 | 31.4% |
| Disabled | template | 109.6% | 75.8% | 109.84 | 75.91 | 30.9% |
| Capped at 1,000 | static | 139.4% | 112.5% | 139.67 | 112.73 | 19.3% |
| Capped at 1,000 | template | 141.9% | 112.4% | 142.16 | 112.58 | 20.8% |

The no-journal static CPU ranges across rounds were 103.3–105.6 µs/request for Java
and 69.3–73.2 for Go; template ranges were 107.5–112.7 and 74.3–76.7 respectively.
With journals, static ranges were 135.0–142.4 for Java and 111.7–113.7 for Go;
template ranges were 140.6–144.7 and 111.5–113.5.
Mean per-run p95 latencies were 1.58–1.67 ms across these combinations. Arrivals
are dispatched in 1 ms batches; reported latency includes scheduler and queue
waiting from the intended arrival time, so this is not a sub-millisecond latency
comparison. The queue is bounded to one second of offered arrivals, and runs fail
on drops, errors or achieved RPS more than 2% from the target.

CPU measurement uses the difference in cumulative process user+system CPU time
from `ps`, across all server threads. It includes server GC/JIT work during the
measurement and excludes startup, warmup and the load generator's CPU. The sampling
window includes driver launch and report serialization, approximately 15.02–15.03
seconds. Both processes share the host; these are repeated local observations,
not statistical significance tests or guarantees for other rates, hardware,
JDK versions, mappings or longer steady-state runs.

Raw results include every round's CPU time, CPU percentage, CPU/request, achieved
RPS, latency and sampled RSS:
[no journal](testdata/reports/benchmark-cpu-10k.json),
[bounded journal](testdata/reports/benchmark-cpu-10k-journal.json).

```sh
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --rate 10000 --warmup 10s --duration 15s --rounds 3 --output testdata/reports/benchmark-cpu-10k.json
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --rate 10000 --warmup 10s --duration 15s --rounds 3 --journal-limit 1000 --output testdata/reports/benchmark-cpu-10k-journal.json
```
