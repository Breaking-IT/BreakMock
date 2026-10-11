# Benchmark measurements

Measured on 2026-10-11 against WireMock 3.13.2. These are small same-host HTTP
workloads, not a capacity guarantee or evidence of complete compatibility.

Platform: `macOS-27.0.1-arm64-arm-64bit-Mach-O` (`arm64`).
Go: `go version go1.27.1 darwin/arm64`. Java details and reference JAR hash are in the raw report.

## Journaling disabled

| Workload | Java RPS | BreakMock RPS | Throughput ratio | Java peak RSS MiB | BreakMock peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| static | 75,187 | 100,858 | 1.34× | 516.5 | 27.5 |
| template | 77,937 | 100,441 | 1.29× | 641.3 | 28.2 |

Medians of three rounds; zero request errors in every round. RSS column is the
median of each round’s sampled peak. Raw results: [benchmark.json](testdata/reports/benchmark.json).

## Method and limitations

- The same Go HTTP client uses 32 persistent connections for both servers.
- Each workload gets a three-second warmup and five-second measurement.
- Server order alternates by round; only one measured server runs at a time.
- Static response: `Hello, benchmark!`. Template combines a query parameter with a math helper.
- The script verifies the response body before loading each server; the load phase checks status/read errors.
- RSS is sampled every 100 ms. JVM heap policy and GC influence these measurements.
- Client and server share the same machine and CPU; background host activity is not isolated.
- These workloads do not exercise TLS, gzip throughput, thousands of mappings, large bodies or complex templates.
- The template fast path is general straight-line interpolation. Stateful/random/time/dynamic/block templates use the full renderer.

Reproduce:

```sh
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --rounds 3 --duration 5s
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --rounds 3 --duration 5s --journal-limit 1000 --output testdata/reports/benchmark-journal.json
```
