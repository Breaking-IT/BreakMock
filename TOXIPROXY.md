# Embedded Toxiproxy

The Go executable embeds [Toxiproxy v2.12.0](https://github.com/Shopify/toxiproxy/tree/v2.12.0)
for TCP fault injection. One process owns the WireMock-compatible HTTP server,
its administration API, the Toxiproxy management API, and configured TCP proxies.
There is no second executable or separate Toxiproxy management port.

Toxiproxy is opt-in. Existing invocations keep their current behavior; enable it
with `--enable-toxiproxy` or `"toxiproxy": {"enabled": true}` in a configuration
file. WireMock mappings, response templates, request journals and administration
remain separate from proxy management.

## Run the example

From the repository root:

```sh
go build -trimpath -o bin/breakmock ./cmd/breakmock
./bin/breakmock --config examples/toxiproxy/breakmock.json
```

The [example configuration](examples/toxiproxy/breakmock.json) starts HTTP on
`127.0.0.1:8080` and a TCP proxy on `127.0.0.1:8081`, forwarding to that HTTP
listener with 100 milliseconds of downstream latency. Both ports must be free.
The [mapping](examples/toxiproxy/mappings/hello.json) uses the normal WireMock
response-template transformer.

In another terminal:

```sh
# Direct request and proxied request return the same body: Hello Ada.
curl 'http://127.0.0.1:8080/hello?name=Ada'
curl 'http://127.0.0.1:8081/hello?name=Ada'

# Management always uses the shared HTTP listener.
curl http://127.0.0.1:8080/__admin/toxiproxy/proxies

# Remove the startup latency toxic without changing the mapping.
curl -X DELETE \
  http://127.0.0.1:8080/__admin/toxiproxy/proxies/mock-api/toxics/response-latency

# Add a downstream bandwidth limit (rate is KB/s in Toxiproxy).
curl -X POST \
  http://127.0.0.1:8080/__admin/toxiproxy/proxies/mock-api/toxics \
  -H 'Content-Type: application/json' \
  -d '{"name":"slow-download","type":"bandwidth","stream":"downstream","toxicity":1,"attributes":{"rate":100}}'
```

A proxy's `upstream` can instead target another reachable TCP service. Application
clients connect to its `listen` address to receive the configured faults. Each
proxy needs a dedicated TCP listen endpoint, even though all management shares
the BreakMock HTTP listener. Toxiproxy operates on TCP connections and streams;
its `toxicity` probability is not an HTTP request error percentage, especially
when clients reuse connections. HTTP response statuses and bodies belong in
BreakMock mappings.

## Shared configuration

`--config FILE` reads one JSON object with `server` and `toxiproxy` sections.
Unknown configuration fields, malformed/trailing JSON, invalid ports and negative
journal limits are rejected. The server fields map to existing CLI options:

| JSON field under `server` | CLI option | Default |
| --- | --- | --- |
| `port` | `--port` | `8080` |
| `bindAddress` | `--bind-address` | `0.0.0.0` |
| `rootDir` | `--root-dir` | `.` |
| `globalResponseTemplating` | `--global-response-templating` | `false` |
| `localResponseTemplating` | `--local-response-templating` | `true` |
| `disableRequestLogging` | `--disable-request-logging` | `false` |
| `disableResponseTemplating` | `--disable-response-templating` | `false` |
| `noRequestJournal` | `--no-request-journal` | `false` |
| `maxRequestJournalEntries` | `--max-request-journal-entries` | `0` (unlimited) |
| `websocketIdleTimeout` | `--websocket-idle-timeout` | `300000` ms |
| `websocketMaxTextMessageSize` | `--websocket-max-text-message-size` | `10485760` bytes |
| `websocketMaxBinaryMessageSize` | `--websocket-max-binary-message-size` | `10485760` bytes |

Explicit CLI options override file values, including `false` and `0`, regardless
of their position relative to `--config`. For example,
`--enable-toxiproxy=false` disables the embedded component despite a file setting
of `true`; `--max-request-journal-entries=0` restores an unlimited journal.
Changing the HTTP port does not rewrite a proxy's configured upstream address.

A relative `server.rootDir` resolves against the configuration file's directory.
The omitted default `.` resolves there too. An explicit `--root-dir` resolves
relative to the process working directory, as it did before configuration files
were supported.

`toxiproxy.enabled` defaults to `false`. `toxiproxy.proxies` is an optional array;
each element requires `name`, `listen` and `upstream`, accepts `enabled` (default
`true`), and optionally contains a `toxics` array using native Toxiproxy toxic
objects. A disabled component starts no proxies, even when its array is present.
An enabled component with no proxies starts with an empty management API.
Invalid or unavailable startup proxies fail startup and release already-created
listeners. Runtime API changes are in memory: they do not rewrite the JSON file
and do not persist across process restarts.

## Management API and lifecycle

Native Toxiproxy routes are mounted under `/__admin/toxiproxy`:

| Method | Path after `/__admin/toxiproxy` | Purpose |
| --- | --- | --- |
| GET | `/version` | Embedded Toxiproxy version |
| GET, POST | `/proxies` | List or create proxies |
| GET, POST, PATCH, DELETE | `/proxies/{name}` | Inspect, update or delete a proxy |
| GET, POST | `/proxies/{name}/toxics` | List or create toxics |
| GET, POST, PATCH, DELETE | `/proxies/{name}/toxics/{toxic}` | Inspect, update or delete a toxic |
| POST | `/populate` | Populate native proxy definitions |
| POST | `/reset` | Enable existing proxies and remove their toxics |

JSON payloads, toxic types and defaults follow the pinned
[native Toxiproxy API](https://github.com/Shopify/toxiproxy/tree/v2.12.0#http-api).
Toxiproxy administration is excluded from WireMock request journaling and stub
matching. The added namespace is active only when Toxiproxy is enabled.

Reset operations are isolated: WireMock `/__admin/reset` does not reset proxies;
Toxiproxy `/__admin/toxiproxy/reset` does not change WireMock mappings or request
journals. Toxiproxy reset does not reload startup toxics from the file.

`SIGINT`, `SIGTERM` and WireMock `POST /__admin/shutdown` trigger graceful HTTP
shutdown, followed by closure of proxy listeners and active proxy connections.
An in-progress TCP dial to an unreachable upstream can delay proxy shutdown
until the operating system's connection timeout; the HTTP shutdown deadline
does not bound that native Toxiproxy cleanup.

The API is unauthenticated, like the existing WireMock admin API; the example
binds management and proxy listeners to loopback. Bind addresses are configured
separately for the HTTP server and each proxy.

Go applications embedding `breakmock.NewServer` pass `Options.Toxiproxy` and must
call `Server.Close()` after stopping their HTTP server to release proxy resources.
The CLI handles that lifecycle automatically.

## Licenses

BreakMock remains Apache-2.0. Toxiproxy retains Shopify's MIT license and
attribution in [third_party/toxiproxy/LICENSE](third_party/toxiproxy/LICENSE).
Distributed notices are bundled in [third_party/NOTICES.txt](third_party/NOTICES.txt)
and are embedded into the executable. `breakmock --licenses` prints the Apache
license, project notices and third-party notices without starting listeners.
The Docker image also carries the license files.
