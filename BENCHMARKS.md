# Benchmark measurements

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
