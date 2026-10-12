# Optimized Go compatibility validation

Validated implementation commit `7176b12` against pinned WireMock `4.0.0-beta.40`.
`go test -race ./...` and `go vet ./...` pass (logs one directory above).

`verify.py` runs 19 suite invocations. Sixteen pass, and three report existing compatibility gaps. No newly failing cases were found.

Passing coverage: fixture 9, basic 30, matchers 434, templates 429, admin 130, runtime 46, gzip 99, matcher distance 140, webhooks 7, faults 32, entities 34, messaging 79, streaming 51 strict plus 2 lifecycle, status 17, and streaming journal 20. Journal concurrency passes 16 per-server invariants, two exact admin comparisons, and one journal comparison after the documented dynamic-scalar substitutions.

Existing failures remain explicit:

- Diagnostics: 40/41. Binary mismatch output contains a changing JVM object identity. No new normalization or exception was added.
- Message waits: 4/7. Count waits fail to cap existing results; single waits incorrectly honor count; zero-timeout count waits fail to cap results.
- Disabled-journal message waits: 0/2. Go returns HTTP 200 instead of Java's HTTP 500 and error body.

The exact archived preoptimization Go binary independently reproduces all five message-wait failures. See `baseline-message-waits/`. Production wait logic is unchanged by optimization.

The journal suite preserves property order and whitespace, fixed IDs, and response bodies. Only generated event IDs, logged timestamps/ports, measured timings, and response Date are substituted. Concurrent completion order is checked per server rather than compared across independent servers. The suite retains caps, complete capture below capacity, retained membership, unique IDs across resets, sequential eviction order, and completed scenario visibility.

These results establish no regression in the exercised corpus. They do not establish universal byte-for-byte compatibility. The overall verification command intentionally exits nonzero because the known failures remain.
