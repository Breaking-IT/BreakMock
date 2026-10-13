# Large JSON responses: optimization and Java comparison

Earlier benchmarks served 17-byte bodies, so per-request costs that grow with body
size never showed up. This pass benchmarks large JSON documents served seven
ways: inline `jsonBody`, inline `body`, `bodyFileName`, response templates and
gzip. It found and removed several such costs, then compared the result with
WireMock 4.0.0-beta.40.

At ~100 KiB, the optimized BreakMock served **1.3–10.6× the throughput of Java
WireMock**, with **54–96% less server CPU** per request and **91–99% less peak
RSS**, on all 14 workload/journal combinations. The lead grows with body size:
**3.1–86×** at ~1 MiB and **3.0–208×** at ~10 MiB. Before this work, BreakMock
was **30–51× slower than Java** for large templated bodies.

Measured on 2026-10-13 from a clean checkout of commit `68cd311` on an Apple M4
Pro (14 logical CPUs, 48 GB), macOS arm64, Go 1.27.1, OpenJDK 27 (Homebrew).
The JAR's SHA-256 matches `testdata/reference.json`. Every response from every
Go build was **byte-identical to Java's response**, gzip-compressed bodies
included; the load generator checked every response.

## Java WireMock vs optimized BreakMock

`scripts/json_benchmark.py` ran Java WireMock, BreakMock before this work
(`51ac179`) and the optimized BreakMock in one session. Servers ran one at a time
on the same port, in rotating order, for 3 rounds. Each workload used 32
persistent connections, a 2 s warmup and a 5 s measurement. The same Go load
generator drove all three and shares the host with them. Journals were either
disabled or capped at 1,000 entries on every server.

Throughput, requests per second (mean of 3 rounds; higher is better):

| Journal | Workload | Java | BreakMock before | BreakMock optimized | Optimized vs Java |
| --- | --- | ---: | ---: | ---: | ---: |
| off | jsonBody | 8,234 | 19,674 | 57,429 | 6.97× |
| off | body | 26,370 | 33,900 | 54,273 | 2.06× |
| off | bodyFileName | 13,966 | 27,221 | 54,505 | 3.90× |
| off | templateBody | 21,885 | 430 | 34,526 | 1.58× |
| off | templateFile | 16,564 | 450 | 33,245 | 2.01× |
| off | templateHelper | 21,136 | 459 | 28,312 | 1.34× |
| off | jsonBodyGzip | 8,245 | 13,142 | 87,070 | 10.56× |
| 1,000 | jsonBody | 8,979 | 25,730 | 54,640 | 6.09× |
| 1,000 | body | 22,167 | 39,481 | 52,819 | 2.38× |
| 1,000 | bodyFileName | 12,912 | 28,685 | 52,899 | 4.10× |
| 1,000 | templateBody | 19,348 | 517 | 48,061 | 2.48× |
| 1,000 | templateFile | 15,839 | 497 | 50,698 | 3.20× |
| 1,000 | templateHelper | 17,853 | 590 | 48,728 | 2.73× |
| 1,000 | jsonBodyGzip | 8,143 | 14,221 | 79,141 | 9.72× |

Server CPU per completed request, µs (all server threads, excluding the load
generator; mean of 3 rounds; lower is better):

| Journal | Workload | Java | BreakMock before | BreakMock optimized | Optimized vs Java |
| --- | --- | ---: | ---: | ---: | ---: |
| off | jsonBody | 1,012.9 | 341.6 | 83.2 | −92% |
| off | body | 318.3 | 154.5 | 81.1 | −75% |
| off | bodyFileName | 647.7 | 239.0 | 88.9 | −86% |
| off | templateBody | 381.0 | 23,125.4 | 151.7 | −60% |
| off | templateFile | 537.4 | 23,315.1 | 160.9 | −70% |
| off | templateHelper | 393.6 | 23,720.4 | 179.8 | −54% |
| off | jsonBodyGzip | 1,197.7 | 710.3 | 44.6 | −96% |
| 1,000 | jsonBody | 1,034.7 | 305.8 | 97.2 | −91% |
| 1,000 | body | 379.5 | 160.7 | 97.3 | −74% |
| 1,000 | bodyFileName | 693.2 | 243.3 | 102.0 | −85% |
| 1,000 | templateBody | 452.4 | 19,017.5 | 109.9 | −76% |
| 1,000 | templateFile | 611.0 | 18,836.5 | 116.2 | −81% |
| 1,000 | templateHelper | 455.4 | 18,674.9 | 118.4 | −74% |
| 1,000 | jsonBodyGzip | 1,214.2 | 702.7 | 66.8 | −95% |

p99 latency, ms (mean of 3 rounds; lower is better):

| Journal | Workload | Java | BreakMock before | BreakMock optimized |
| --- | --- | ---: | ---: | ---: |
| off | jsonBody | 49.20 | 5.54 | 1.07 |
| off | body | 5.49 | 2.87 | 1.34 |
| off | bodyFileName | 16.97 | 3.60 | 1.28 |
| off | templateBody | 7.62 | 272.60 | 2.84 |
| off | templateFile | 12.89 | 238.24 | 3.05 |
| off | templateHelper | 8.70 | 211.58 | 5.22 |
| off | jsonBodyGzip | 33.00 | 8.12 | 1.68 |
| 1,000 | jsonBody | 38.07 | 5.11 | 1.54 |
| 1,000 | body | 8.99 | 2.67 | 1.55 |
| 1,000 | bodyFileName | 15.35 | 5.49 | 1.51 |
| 1,000 | templateBody | 9.76 | 193.70 | 2.18 |
| 1,000 | templateFile | 11.42 | 206.02 | 1.87 |
| 1,000 | templateHelper | 13.87 | 137.05 | 2.10 |
| 1,000 | jsonBodyGzip | 29.92 | 9.69 | 1.36 |

Peak sampled RSS, MiB (highest across rounds; lower is better). Each server
process runs every workload in sequence, so later workloads include earlier
growth. Java ran with default JVM heap settings:

| Journal | Java | BreakMock before | BreakMock optimized |
| --- | ---: | ---: | ---: |
| off | 2,359–5,486 | 69–90 | 34–42 |
| 1,000 | 4,345–6,562 | 966–1,176 | 392–441 |

With a 1,000-entry journal, BreakMock keeps ≈400 MiB because the journal holds
1,000 copies of ~100 KiB bodies.

Notes:

- The static workloads reach about 53–57k RPS, which moves ≈5.3 GB/s through
  loopback. At that point the shared load generator and kernel appear to be the
  limit, so use the CPU table to compare those workloads.
- Java got the same 2 s warmup per workload as Go. Its JIT also warmed up during
  earlier workloads in the same process. Results vary between rounds and runs on
  this host; every round is in the raw report.
- These are same-host observations of one mock workload shape. They are not
  capacity guarantees for other hardware, JVM settings or mappings.

## Scaling with body size

The same workloads at ~1 MiB and ~10 MiB, Java and the optimized BreakMock, with
the same settings. Every Go response was again byte-identical to Java's. The
100 KiB columns come from the run above. BreakMock's advantage **grows with body
size**: per request, Java's CPU rises roughly in proportion to the body, while
BreakMock serves cached bytes and renders templates in one pass.

Throughput, requests per second, journal disabled (Go ÷ Java in brackets):

| Workload | 100 KiB Java | 100 KiB Go | 1 MiB Java | 1 MiB Go | 10 MiB Java | 10 MiB Go |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| jsonBody | 8,234 | 57,429 (7.0×) | 1,049 | 10,502 (10.0×) | 55 | 862 (15.7×) |
| body | 26,370 | 54,273 (2.1×) | 2,774 | 10,357 (3.7×) | 260 | 840 (3.2×) |
| bodyFileName | 13,966 | 54,505 (3.9×) | 3,333 | 10,493 (3.1×) | 283 | 845 (3.0×) |
| Template | 21,885 | 34,526 (1.6×) | 2,507 | 11,386 (4.5×) | 145 | 828 (5.7×) |
| Template file | 16,564 | 33,245 (2.0×) | 2,487 | 11,005 (4.4×) | 165 | 828 (5.0×) |
| Template + jsonPath | 21,136 | 28,312 (1.3×) | 2,579 | 11,275 (4.4×) | 181 | 834 (4.6×) |
| jsonBody, gzip | 8,245 | 87,070 (10.6×) | 971 | 74,192 (76.4×) | 75 | 15,675 (208.4×) |

Server CPU per request, µs, journal disabled (Go saving in brackets):

| Workload | 100 KiB Java | 100 KiB Go | 1 MiB Java | 1 MiB Go | 10 MiB Java | 10 MiB Go |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| jsonBody | 1,013 | 83 (−92%) | 9,146 | 270 (−97%) | 124,868 | 1,980 (−98%) |
| body | 318 | 81 (−75%) | 2,882 | 281 (−90%) | 31,667 | 2,056 (−94%) |
| bodyFileName | 648 | 89 (−86%) | 2,223 | 283 (−87%) | 24,165 | 2,025 (−92%) |
| Template | 381 | 152 (−60%) | 3,386 | 488 (−86%) | 47,921 | 2,628 (−95%) |
| Template file | 537 | 161 (−70%) | 3,643 | 506 (−86%) | 50,880 | 2,949 (−94%) |
| Template + jsonPath | 394 | 180 (−54%) | 3,473 | 513 (−85%) | 45,869 | 2,598 (−94%) |
| jsonBody, gzip | 1,198 | 45 (−96%) | 11,135 | 79 (−99%) | 124,458 | 579 (−99.5%) |

Peak sampled RSS, MiB, journal disabled (highest across rounds):

| Size | Java | Go |
| --- | ---: | ---: |
| 100 KiB | 2,359–5,486 | 34–42 |
| 1 MiB | 3,055–5,138 | 60–145 |
| 10 MiB | 11,323–12,665 | 436–898 |

With a 1,000-entry journal at 1 MiB, BreakMock delivered 4.7–86× Java's
throughput and used 90–99% less CPU per request; peak RSS was 2.4–4.0 GiB for
BreakMock (the journal holds 1,000 bodies) versus 9.5–11.8 GiB for Java. The
10 MiB runs disabled the journal, because 1,000 entries would hold ~10 GB per
server.

Notes:

- Java's peak RSS approaches its default maximum heap (a quarter of the host's
  48 GB). Its 10 MiB runs also varied most between rounds (for example 100–233 RPS
  for templates), consistent with GC pressure near that limit.
- From 1 MiB up, BreakMock's static and template workloads reach about 10 GB/s
  of response data through loopback, which appears to be the shared load
  generator's limit rather than the server's; compare CPU per request there.
- At 10 MiB, gzip responses are 208× faster because BreakMock serves cached
  compressed bytes (bodies up to 16 MiB), while Java compresses every response.

Raw data: [100 KiB](../testdata/reports/benchmark-large-json.json),
[1 MiB](../testdata/reports/benchmark-large-json-1mib.json) and
[10 MiB](../testdata/reports/benchmark-large-json-10mib.json), each with the
source commit and binary and JAR hashes.

## What was slow

| Cost | Where | Fix |
| --- | --- | --- |
| Templates of 64 KiB or more were never cached. Each request re-lexed the whole body with raymond's regexp lexer, which is roughly quadratic (≈14 ms per 100 KiB). | `template.go`, `template_plan.go` | Both template caches are now limited by total cached source size (64 MiB, 1,024 entries), not by the size of one template. |
| Every full-evaluator render ran `strings.Contains(source, helper)` for ~70 helper names, scanning the body ~70 times per request. | `renderTemplateContext` | Each cached template records which helper names its source mentions on the first render and reuses that set. |
| `jsonBody` was re-encoded on every request. The stored `json.RawMessage` was validated, compacted and then copied, although `DecodeDocument` already stores the compact wire form. | `ServeHTTP` | The stored bytes are served directly. |
| Gzip (zlib through cgo) re-compressed the same static body on every request. | `ServeHTTP` | Bodies that are not rendered as templates use a cache of compressed output, keyed by content (1 KiB–16 MiB bodies, 64 MiB budget, cleared when full). A changed file or mapping gives a new key, so a stale compressed body is never served. |
| Each journaled response was copied to a string and to base64 (≈2.3× the body size) on the request path. | `Store.RecordServed` | One owned copy is kept. `body` and `bodyAsBase64` are encoded when the journal is read (`journalText`, `journalBase64`). |
| `bodyFileName` called `os.OpenRoot` and opened the file on every request. Under concurrency these opens contend in the kernel (≈24 µs per request with 14 workers). | `resolveBodyEntity` | File contents are cached. A cached body is used only while a `stat` of its path reports the same file, size, mtime **and ctime**. Cache misses still read through `os.Root`, so paths cannot escape `__files`. |
| The body was copied 3–4 times per templated request: string→bytes, the entity's text conversion, Handlebars' `fmt.Sprintf("%s")` and `[]byte` conversion, and `Buffer.String()`. | `entity.go`, `internal/handlebars` | Read-only views and a `strings.Builder` replace these copies. Allocation for the full evaluator fell from 784 KB to 123 KB per request. |

The last row matters most for the full Handlebars evaluator with the journal
off. There the live heap is only a few MiB, so at `GOGC=100` the collector runs
after every few requests. Before the copy reduction, optimized BreakMock reached
only 0.78× Java's throughput on `templateHelper`; after it, BreakMock was faster
than Java (1.34× in the final run above).

Two GC policies were also measured: a fixed `GOGC=400` and a dynamic minimum heap
goal. Neither was adopted. Both recovered throughput, but RSS rose from ~40 MiB to
370–830 MiB, and the dynamic goal was unstable while the heap was growing.
Deployments that prefer throughput to memory can still set `GOGC` or
`GOMEMLIMIT`.

Ctime cannot be set by `cp -p`, `rsync -t` or `touch`, so a same-size in-place
rewrite with a restored mtime is still detected. `TestBodyFileChangesAreServed`
covers this case, an atomic rename, deletion and a symlink that is retargeted
outside `__files`. That test fails if the ctime comparison is removed. Body files
are not cached on platforms other than Linux and macOS, because ctime is not
available there.

## Compatibility verification

- `python3 scripts/verify.py --jar wiremock.jar` makes 20 differential suite runs
  against the pinned JAR (19 suites, one also without a journal), on the final
  code. All 20 pass. The binary-mismatch diagnostic, whose Java output contains a
  random JVM object identity, is compared with that token masked (see
  [COMPATIBILITY.md](COMPATIBILITY.md#byte-comparison-boundaries)).
- In the benchmark, every served response was compared byte-for-byte with Java's.
- Journal events were also compared with Java's (ids, timings and client details
  removed). Static responses matched. Two differences remain, and both exist
  unchanged in BreakMock before this work:
  - For templated responses, Java journals the rendered body in
    `responseDefinition`; BreakMock journals the template source.
  - For gzip responses, Java journals the uncompressed body without
    `Content-Encoding`; BreakMock journals the compressed bytes.
- `scripts/template_differential.py --pad 80000 --repeat 3` reruns all 429
  template cases inside 80 KB templates, 3 requests each: 429/429 identical to
  Java. `scripts/dynamic_template_differential.py` (238 checks) confirms against
  Java that responses still change with every request and every edit:
  per-request values, random and time helpers, mapping, body file and jsonBody
  edits (with gzip), and journal entries under concurrency. The same checks fail
  identically on BreakMock before this work, so no regression was found.
- `go test -race ./...` passes. The cgo-free build's tests also pass (gzip cases
  are skipped because that build cannot produce WireMock's gzip bytes).

## In-process benchmark

`BenchmarkLargeJSONResponse` (in `pkg/breakmock`) calls `ServeHTTP` directly with a
discarding writer. This isolates server work from networking. Median of 5 runs:

| Workload | Journal | Before µs/op | Optimized µs/op | Speedup | Before B/op | Optimized B/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| jsonBody | off | 141.3 | 2.0 | 71× | 248,466 | 6,666 |
| body | off | 13.1 | 2.0 | 7× | 113,350 | 6,669 |
| bodyFileName | off | 34.7 | 3.9 | 9× | 114,191 | 7,456 |
| templateBody | off | 13,908 | 14.4 | 967× | 780,593 | 113,993 |
| templateFile | off | 13,872 | 16.0 | 868× | 780,570 | 114,935 |
| templateHelper | off | 13,723 | 20.4 | 672× | 784,350 | 123,299 |
| jsonBodyGzip | off | 499.0 | 3.9 | 128× | 359,156 | 6,724 |
| jsonBody | on | 179.0 | 8.4 | 21× | 721,350 | 120,312 |
| body | on | 53.0 | 8.4 | 6× | 611,842 | 120,312 |
| bodyFileName | on | 79.7 | 11.0 | 7× | 612,668 | 121,097 |
| templateBody | on | 13,971 | 19.1 | 733× | 1,270,250 | 227,536 |
| templateFile | on | 14,155 | 21.7 | 651× | 1,270,237 | 228,482 |
| templateHelper | on | 14,072 | 26.0 | 540× | 1,271,948 | 235,971 |
| jsonBodyGzip | on | 491.2 | 8.3 | 59× | 377,026 | 20,455 |

## Workloads

Each workload serves the same deterministic order listing (~100 KiB, with nested
objects, numbers, `é` and `<`), as follows:

- `jsonBody`, `body`, `bodyFileName`: the static document inline as JSON, as a
  string, or from `__files`.
- `templateBody`, `templateFile`: the document with
  `{{request.query.customer}}` and `{{request.headers.X-Request-Id}}`. This takes
  the optimized straight-line template plan.
- `templateHelper`: a POST that uses `{{jsonPath request.body '$.customer'}}`.
  This runs the full Handlebars evaluator.
- `jsonBodyGzip`: `jsonBody` with `Accept-Encoding: gzip`.

## Reproduce

```sh
# In-process (both checkouts need json_response_benchmark_test.go).
go test ./pkg/breakmock -run '^$' -bench 'LargeJSONResponse/.*/100KiB' -count=5

# End to end. List Java first so its responses are the byte reference.
# Add --size 1048576 or --size 10485760 --journal disabled for the scaling runs.
curl -fL https://repo.maven.apache.org/maven2/org/wiremock/wiremock-standalone/4.0.0-beta.40/wiremock-standalone-4.0.0-beta.40.jar -o /tmp/wiremock.jar
git worktree add /tmp/bm-baseline 51ac179
(cd /tmp/bm-baseline && go build -trimpath -o /tmp/breakmock-baseline ./cmd/breakmock)
go build -trimpath -o /tmp/breakmock-optimized ./cmd/breakmock
python3 scripts/json_benchmark.py --binary java=/tmp/wiremock.jar \
  --binary go-baseline=/tmp/breakmock-baseline --binary go-optimized=/tmp/breakmock-optimized \
  --rounds 3 --duration 5s --warmup 2s --journal-check report \
  --output testdata/reports/benchmark-large-json.json
```

`--journal-check report` records the two known journal differences instead of
stopping the run. Between BreakMock builds, use the default `strict` mode.

Raw data: [end-to-end report](../testdata/reports/benchmark-large-json.json)
(including executable and JAR hashes, every round and the journal differences)
and [Go benchmark output](../testdata/reports/benchmark-large-json-go.txt).
