# BreakMock

A Go implementation of WireMock-style HTTP mocking, response templating, and
administration. Built by Breaking-IT as a foundation for API simulation, test data,
and controlled failures.

**Development status:** a compatibility port under active construction, not a
complete drop-in replacement. The reference is **WireMock 3.13.2**, the latest
stable release when this work started. See [COMPATIBILITY.md](COMPATIBILITY.md)
for tested coverage and known gaps. Performance and compatibility are separate
claims; neither is inferred from the implementation language.

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

## Test against the original Java server

```sh
go test -race ./...
go vet ./...
curl -fL https://repo.maven.apache.org/maven2/org/wiremock/wiremock-standalone/3.13.2/wiremock-standalone-3.13.2.jar -o /tmp/wiremock.jar
python3 scripts/verify.py --jar /tmp/wiremock.jar
```

The harness starts isolated Java and Go processes for every suite and checks the
reference version. It reuses unmodified upstream mapping fixtures and sends the
same mappings and requests to both implementations. Application response bodies
are compared as bytes. Admin response bodies are compared with exact formatting and property order,
with only nondeterministic scalar values (generated IDs, timestamps, timings and
local origins) substituted. Gzip response bytes are compared without decompression.
Two invalid-template diagnostic bodies remain known strict-byte failures; their
HTTP status checks are reported separately. This is a
bounded differential corpus, not the entire upstream Java test suite.

## Benchmark

```sh
python3 scripts/benchmark.py --jar /tmp/wiremock.jar --rounds 3 --duration 5s
```

The same Go HTTP load generator drives both servers with 32 persistent clients,
with request journals disabled on both by default. Add `--journal-limit 1000` to
measure both servers with bounded journals. The script validates response bodies,
warms up each workload, alternates server order, and records RPS, latency and
sampled RSS. Results describe this workload and machine; they are not a universal
throughput guarantee. See [BENCHMARKS.md](BENCHMARKS.md).

## License and provenance

Apache License 2.0; see [LICENSE](LICENSE) and [NOTICE.txt](NOTICE.txt).
The compatibility reference is WireMock commit
`88587aa13b4899da080538e1b21bec0da105491e`. Upstream fixtures are retained under
`testdata/upstream/`. The modified Handlebars runtime in `internal/handlebars/`
retains Raymond's MIT license and documents local changes.

BreakMock is independent and is not affiliated with or endorsed by WireMock.
WireMock Cloud's proprietary implementation is not included. Additional test data
and chaos functionality will require independent implementation.
