# Embedded Toxiproxy

The Go executable embeds [Toxiproxy v2.12.0](https://github.com/Shopify/toxiproxy/tree/v2.12.0)
for TCP fault injection. One process owns the WireMock-compatible HTTP server,
its administration API, the Toxiproxy management API, and configured TCP proxies.
There is no second executable or separate Toxiproxy management port.

The CLI exposes the management API by default, with an empty proxy collection.
It creates no TCP proxy listeners and applies no bandwidth, delay or connection
faults until proxies and toxics are configured. Requests sent directly to
BreakMock's HTTP port do not pass through Toxiproxy. To inject network faults,
point your application at a configured proxy's listen port.

Set `--enable-toxiproxy=false` or `"toxiproxy": {"enabled": false}` to disable
the extension entirely, including its additional admin namespace. WireMock
mappings, response templates, journals and other admin routes retain their
existing behavior.

## Run the example

From the repository root:

```sh
go build -trimpath -o bin/breakmock ./cmd/breakmock
./bin/breakmock --config examples/toxiproxy/breakmock.json
```

The [example configuration](../examples/toxiproxy/breakmock.json) starts HTTP on
`127.0.0.1:8080` and a TCP proxy on `127.0.0.1:8081`, forwarding to that HTTP
listener with 100 milliseconds of downstream latency. Both ports must be free.
The [mapping](../examples/toxiproxy/mappings/hello.json) uses the normal WireMock
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

## Chaos testing recipes

Start BreakMock normally and create a proxy when you need network faults:

```sh
./bin/breakmock --port 8080 --bind-address 127.0.0.1
```

In another terminal, create an initially fault-free proxy to the mock server:

```sh
curl -X POST http://127.0.0.1:8080/__admin/toxiproxy/proxies \
  -H 'Content-Type: application/json' \
  -d '{"name":"chaos","listen":"127.0.0.1:8666","upstream":"127.0.0.1:8080","enabled":true}'
```

Load your usual WireMock mappings on port 8080 and configure the application
under test to call port 8666. Direct traffic to port 8080 remains unaffected.
Creating a proxy adds TCP forwarding; it imposes no artificial bandwidth limit
or delay until a toxic is added. The proxy can also target an external TCP
service, such as a database, rather than BreakMock.

Add one scenario at a time by POSTing its JSON body to
`/__admin/toxiproxy/proxies/chaos/toxics`:

| Scenario | Toxic JSON | What to check |
| --- | --- | --- |
| Latency and jitter | `{"name":"fault","type":"latency","stream":"downstream","attributes":{"latency":200,"jitter":50}}` | Deadlines and retries under variable delay (milliseconds) |
| Slow download | `{"name":"fault","type":"bandwidth","stream":"downstream","attributes":{"rate":64}}` | Streaming and backpressure at 64 KB/s |
| Slow upload | `{"name":"fault","type":"bandwidth","stream":"upstream","attributes":{"rate":32}}` | Upload handling at 32 KB/s |
| Stalled response | `{"name":"fault","type":"timeout","stream":"downstream","attributes":{"timeout":1000}}` | Dropped response data followed by connection closure after 1000 ms |
| Connection reset | `{"name":"fault","type":"reset_peer","stream":"downstream","attributes":{"timeout":0}}` | Immediate TCP reset and recovery |
| Interrupted response | `{"name":"fault","type":"limit_data","stream":"downstream","attributes":{"bytes":1024}}` | Connection closure after 1024 bytes in that direction |

For example:

```sh
curl -X POST http://127.0.0.1:8080/__admin/toxiproxy/proxies/chaos/toxics \
  -H 'Content-Type: application/json' \
  -d '{"name":"fault","type":"bandwidth","stream":"downstream","toxicity":1,"attributes":{"rate":64}}'

# Remove this fault while keeping the proxy available.
curl -X DELETE http://127.0.0.1:8080/__admin/toxiproxy/proxies/chaos/toxics/fault

# Stop proxy forwarding and close its connections/listener.
curl -X POST http://127.0.0.1:8080/__admin/toxiproxy/proxies/chaos \
  -H 'Content-Type: application/json' -d '{"enabled":false}'

# Remove the proxy when the scenario is finished.
curl -X DELETE http://127.0.0.1:8080/__admin/toxiproxy/proxies/chaos
```

`upstream` means client to target; `downstream` means target to client. Multiple
toxics can be combined, but remove the previous `fault` before using the next
recipe with the same name. `toxicity` defaults to 1; a value such as 0.1 selects
connections/stream links probabilistically, not exactly 10% of HTTP requests.
Connection pooling therefore affects observed fault frequency. Network faults
operate on TCP bytes, including HTTP headers or TLS records; a 1024-byte limit
is not a 1024-byte application response-body limit. Use WireMock mappings for
application-level error statuses such as HTTP 500.

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

`toxiproxy.enabled` defaults to `true` in the CLI. `toxiproxy.proxies` is an optional array;
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

Go applications import `github.com/Breaking-IT/BreakMock/pkg/breakmock` to embed
`breakmock.NewServer` and explicitly pass
`Options.Toxiproxy` with `Enabled: true` (the library zero value is disabled) and must
call `Server.Close()` after stopping their HTTP server to release proxy resources.
The CLI handles that lifecycle automatically.

## Licenses

BreakMock remains Apache-2.0. Toxiproxy retains Shopify's MIT license and
attribution in [third_party/toxiproxy/LICENSE](../third_party/toxiproxy/LICENSE).
Distributed notices are bundled in [third_party/NOTICES.txt](../third_party/NOTICES.txt)
and are embedded into the executable. `breakmock --licenses` prints the Apache
license, project notices and third-party notices without starting listeners.
The Docker image also carries the license files.
