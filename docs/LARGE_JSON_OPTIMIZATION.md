# Large JSON responses: optimization and Java comparison

Earlier benchmarks served 17-byte bodies, so per-request costs that grow with body
size never showed up. This pass benchmarks ~100 KiB JSON documents served seven
ways: inline `jsonBody`, inline `body`, `bodyFileName`, response templates and
gzip. It found and removed seven such costs, then compared the result with
WireMock 4.0.0-beta.40 in the same run.

In this run, the optimized BreakMock served **1.6–11.8× the throughput of Java
WireMock**. Server CPU per request was **55–96% lower**, p99 latency **57–96% lower**
and peak RSS **89–99% lower**, on all 14 workload/journal combinations. Before
this work, BreakMock was **26–48× slower than Java** for large templated bodies.

Measured on 2026-10-12, Apple M4 Pro (14 logical CPUs), macOS arm64, Go 1.27.1,
OpenJDK 27 (Homebrew). The JAR's SHA-256 matches `testdata/reference.json`. Every
response from both Go builds was **byte-identical to Java's response**,
gzip-compressed bodies included; the load generator checked every response.

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
| off | jsonBody | 10,960 | 21,305 | 56,844 | 5.19× |
| off | body | 27,077 | 36,893 | 54,782 | 2.02× |
| off | bodyFileName | 14,999 | 28,796 | 55,246 | 3.68× |
| off | templateBody | 23,452 | 492 | 36,983 | 1.58× |
| off | templateFile | 18,415 | 480 | 35,299 | 1.92× |
| off | templateHelper | 20,976 | 485 | 33,122 | 1.58× |
| off | jsonBodyGzip | 9,008 | 13,971 | 106,265 | 11.80× |
| 1,000 | jsonBody | 8,480 | 28,763 | 56,241 | 6.63× |
| 1,000 | body | 25,279 | 40,879 | 53,600 | 2.12× |
| 1,000 | bodyFileName | 14,658 | 33,903 | 53,859 | 3.67× |
| 1,000 | templateBody | 21,403 | 651 | 52,622 | 2.46× |
| 1,000 | templateFile | 17,212 | 649 | 52,144 | 3.03× |
| 1,000 | templateHelper | 19,749 | 649 | 51,037 | 2.58× |
| 1,000 | jsonBodyGzip | 8,820 | 15,204 | 83,269 | 9.44× |

Server CPU per completed request, µs (all server threads, excluding the load
generator; mean of 3 rounds; lower is better):

| Journal | Workload | Java | BreakMock before | BreakMock optimized | Optimized vs Java |
| --- | --- | ---: | ---: | ---: | ---: |
| off | jsonBody | 932.2 | 339.1 | 80.0 | −91% |
| off | body | 325.1 | 151.2 | 85.7 | −74% |
| off | bodyFileName | 681.8 | 248.6 | 89.3 | −87% |
| off | templateBody | 385.7 | 23,144.5 | 147.3 | −62% |
| off | templateFile | 540.9 | 23,968.5 | 158.0 | −71% |
| off | templateHelper | 395.2 | 23,968.7 | 176.3 | −55% |
| off | jsonBodyGzip | 1,202.8 | 689.9 | 48.0 | −96% |
| 1,000 | jsonBody | 1,091.9 | 301.2 | 96.9 | −91% |
| 1,000 | body | 364.1 | 159.7 | 100.2 | −72% |
| 1,000 | bodyFileName | 685.7 | 251.0 | 102.0 | −85% |
| 1,000 | templateBody | 433.8 | 18,503.5 | 108.5 | −75% |
| 1,000 | templateFile | 588.8 | 18,682.9 | 114.4 | −81% |
| 1,000 | templateHelper | 435.0 | 18,553.3 | 119.1 | −73% |
| 1,000 | jsonBodyGzip | 1,213.5 | 680.4 | 65.3 | −95% |

p99 latency, ms (mean of 3 rounds; lower is better):

| Journal | Workload | Java | BreakMock before | BreakMock optimized |
| --- | --- | ---: | ---: | ---: |
| off | jsonBody | 18.24 | 4.85 | 1.16 |
| off | body | 5.03 | 2.54 | 1.12 |
| off | bodyFileName | 13.95 | 3.27 | 1.19 |
| off | templateBody | 5.96 | 194.93 | 2.55 |
| off | templateFile | 9.84 | 200.66 | 2.68 |
| off | templateHelper | 7.60 | 197.24 | 2.99 |
| off | jsonBodyGzip | 25.01 | 7.72 | 0.90 |
| 1,000 | jsonBody | 35.15 | 3.61 | 1.35 |
| 1,000 | body | 5.79 | 2.49 | 1.33 |
| 1,000 | bodyFileName | 14.52 | 2.92 | 1.40 |
| 1,000 | templateBody | 6.99 | 134.12 | 1.64 |
| 1,000 | templateFile | 9.96 | 133.52 | 1.72 |
| 1,000 | templateHelper | 8.20 | 130.23 | 1.84 |
| 1,000 | jsonBodyGzip | 24.92 | 8.07 | 1.27 |

Peak sampled RSS, MiB (highest across rounds; lower is better). Each server
process runs every workload in sequence, so later workloads include earlier
growth. Java ran with default JVM heap settings:

| Journal | Java | BreakMock before | BreakMock optimized |
| --- | ---: | ---: | ---: |
| off | 2,088–3,831 | 71–91 | 34–42 |
| 1,000 | 3,761–5,788 | 1,065–1,083 | 413–453 |

With a 1,000-entry journal, BreakMock keeps ≈400 MiB because the journal holds
1,000 copies of ~100 KiB bodies.

Notes:

- The static workloads reach about 54–57k RPS, which moves ≈5.3 GB/s through
  loopback. At that point the shared load generator and kernel appear to be the
  limit, so use the CPU table to compare those workloads.
- Java got the same 2 s warmup per workload as Go. Its JIT also warmed up during
  earlier workloads in the same process. Java's throughput and CPU varied by up
  to 24% between rounds; the full per-round values are in the raw report.
- These are same-host observations of one mock workload shape. They are not
  capacity guarantees for other hardware, JVM settings or mappings.

## Scaling with body size

The same workloads at ~1 MiB and ~10 MiB, Java and current BreakMock, measured
on 2026-10-13 with the same settings (3 rounds, 2 s warmup, 5 s measurement, 32
connections). Every Go response was again byte-identical to Java's. The 100 KiB
columns come from the run above. BreakMock's advantage **grows with body size**:
per request, Java's CPU rises roughly in proportion to the body, while BreakMock
serves cached bytes and renders templates in one pass.

Throughput, requests per second, journal disabled (Go ÷ Java in brackets):

| Workload | 100 KiB Java | 100 KiB Go | 1 MiB Java | 1 MiB Go | 10 MiB Java | 10 MiB Go |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| jsonBody | 10,960 | 56,844 (5.2×) | 1,137 | 10,429 (9.2×) | 62 | 887 (14.4×) |
| body | 27,077 | 54,782 (2.0×) | 3,204 | 10,347 (3.2×) | 212 | 845 (4.0×) |
| bodyFileName | 14,999 | 55,246 (3.7×) | 3,183 | 10,289 (3.2×) | 272 | 875 (3.2×) |
| Template | 23,452 | 36,983 (1.6×) | 2,505 | 10,843 (4.3×) | 106 | 814 (7.6×) |
| Template file | 18,415 | 35,299 (1.9×) | 2,639 | 10,615 (4.0×) | 137 | 822 (6.0×) |
| Template + jsonPath | 20,976 | 33,122 (1.6×) | 2,522 | 10,949 (4.3×) | 135 | 881 (6.5×) |
| jsonBody, gzip | 9,008 | 106,265 (11.8×) | 920 | 72,925 (79.3×) | 73 | 13,211 (180.1×) |

Server CPU per request, µs, journal disabled (Go saving in brackets):

| Workload | 100 KiB Java | 100 KiB Go | 1 MiB Java | 1 MiB Go | 10 MiB Java | 10 MiB Go |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| jsonBody | 932 | 80 (−91%) | 8,322 | 270 (−97%) | 119,622 | 1,923 (−98%) |
| body | 325 | 86 (−74%) | 2,923 | 272 (−91%) | 35,047 | 1,995 (−94%) |
| bodyFileName | 682 | 89 (−87%) | 2,327 | 279 (−88%) | 23,340 | 2,059 (−91%) |
| Template | 386 | 147 (−62%) | 3,597 | 502 (−86%) | 63,099 | 2,605 (−96%) |
| Template file | 541 | 158 (−71%) | 3,739 | 515 (−86%) | 64,829 | 3,018 (−95%) |
| Template + jsonPath | 395 | 176 (−55%) | 3,649 | 507 (−86%) | 68,544 | 2,811 (−96%) |
| jsonBody, gzip | 1,203 | 48 (−96%) | 10,920 | 76 (−99%) | 126,076 | 564 (−99.6%) |

Peak sampled RSS, MiB, journal disabled (highest across rounds):

| Size | Java | Go |
| --- | ---: | ---: |
| 100 KiB | 2,088–3,831 | 34–42 |
| 1 MiB | 3,419–5,358 | 59–143 |
| 10 MiB | 9,775–12,665 | 433–1,157 |

With a 1,000-entry journal at 1 MiB, BreakMock delivered 4.3–91× Java's
throughput and used 89–99% less CPU per request; peak RSS was 2.7–4.2 GiB for
BreakMock (the journal holds 1,000 bodies) versus 8.8–11.8 GiB for Java. The
10 MiB runs disabled the journal, because 1,000 entries would hold ~10 GB per
server.

Notes:

- Java's peak RSS approaches its default maximum heap (a quarter of the host's
  48 GB). Its 10 MiB runs also varied most between rounds (for example 41–157 RPS
  for templates), consistent with GC pressure near that limit.
- From 1 MiB up, BreakMock's static and template workloads reach about 10 GB/s
  of response data through loopback, which appears to be the shared load
  generator's limit rather than the server's; compare CPU per request there.
- At 10 MiB, gzip responses are 180× faster because BreakMock serves cached
  compressed bytes (bodies up to 16 MiB), while Java compresses every response.

Raw data: [1 MiB](../testdata/reports/benchmark-large-json-1mib.json) and
[10 MiB](../testdata/reports/benchmark-large-json-10mib.json).

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
only 0.78× Java's throughput on `templateHelper`; after it, 1.58×.

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
