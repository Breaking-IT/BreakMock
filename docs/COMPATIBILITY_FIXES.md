# Message-wait fixes and the remaining strict-byte blocker

Validated on 2026-10-12 against the unchanged, checksum-pinned Java WireMock
4.0.0-beta.40. Implementation commit: `16eb357`.

**The five previously failing Go message-wait cases are fixed. Both full local
differential runs now pass 18 of 19 suite invocations.** The sole failing suite
in each language is diagnostics, at 40/41: the binary mismatch response contains
a JVM object-identity hash. CI cannot honestly be green under the current strict
comparison against independently running, unmodified Java instances.

## Implemented fixes

Go now caps count-wait results, ignores `count` for single-message waits, returns
the correct disabled-journal 500 response, defaults omitted timeouts to zero and
takes a final journal snapshot when a wait expires. Both implementations now
match Java's negative-count error wording for positive and nonpositive timeouts;
disabled-journal errors take precedence over invalid counts.

The shared message-wait suite was expanded from 7 to **15 enabled-journal cases**
and from 2 to **5 disabled-journal cases**, all passing in Go and Rust. The new
cases cover zero/omitted/negative counts, omitted timeouts and error precedence.
New negative-count error tests send the same fixed `Host` header to both servers
and compare the complete status and response body, without substitutions.

Existing assertions, scalar substitutions and timing bounds were retained.
`scripts/diagnostics_differential.py` and the CI workflow are unchanged. No test
was skipped, converted to an expected failure, or made nonblocking.

Go's full race tests and vet pass. Rust's full unit suite, formatting check and
Clippy pass. Per-suite reports and logs are in
[testdata/reports/compatibility-fixes](../testdata/reports/compatibility-fixes).

[Linux CI for the implementation commit](https://github.com/Breaking-IT/BreakMock/actions/runs/38214730011)
confirms the same result: both implementations pass 18/19 suite invocations,
including 15/15 enabled and 5/5 disabled message waits. Each diagnostics suite is
40/41, with only the binary identity case failing. Go race/vet/container and Rust
formatting/unit/Clippy steps pass. CI remains **red**, with its strict failure
preserved. [Saved CI evidence](../testdata/reports/compatibility-fixes/linux-ci/summary.json)
and the [assertion audit](../testdata/reports/compatibility-fixes/assertion-audit.json)
document that no existing comparisons were weakened.

## Why the last assertion cannot be fixed in a native implementation

For the same binary matcher and request, Java emits lines such as:

```text
YWJj | [B@6391b640 <<<<< Body does not match
YWJj | [B@276fd996 <<<<< Body does not match
```

The diagnostic's actual value is a freshly allocated Java byte array.
In the pinned source, `wiremock-core/src/main/java/com/github/tomakehurst/wiremock/verification/diff/Diff.java:451`
creates it with `formattedBody.getBytes(UTF_8)`, and `DiffLine.java:105` calls its
inherited `toString()`. That produces `[B@` followed by the private JVM object
identity, not a hash defined by the HTTP request content. Thread scheduling and
allocation state affect the value.

A fresh **Java-versus-Java** probe used two ordinary Java processes, the exact
pinned JAR, identical fixed-ID mappings and eight identical requests per process:

- Only **2 of 8** corresponding response pairs matched byte for byte.
- **0 of 7** adjacent responses matched in either individual Java process.
- Every response was a 404; the full bodies and SHA-256 hashes are retained.

See [the complete self-comparison](../testdata/reports/compatibility-fixes/java-self-comparison.json).
The JVM-option environment variables were unset, as recorded in the manifest.
No alternate hash mode or patched reference was used.

Reproduce (a nonzero exit is expected when Java differs from Java):

```sh
python3 scripts/probe_binary_diagnostic.py \
  --jar /tmp/breakmock-wiremock-4.0.0-beta.40.jar \
  --output /tmp/java-self-comparison.json
```

An independent Go or Rust process cannot derive another process's private object
identity from these requests. Printing an invented identity, copying the oracle's
response, forcing constant JVM identities, masking the value, or changing the
reference to pass the test would not satisfy the requested contract. The assertion
therefore remains a visible failure in both implementations. Go and Rust's binary
diagnostic rendering has not been disguised as matching Java.

Resolving this blocker requires a changed reference contract—for example, an
upstream Java fix that prints a deterministic binary value. That would require
explicit agreement to target the corrected reference, followed by a full rerun.
It has not been substituted here.

## Reproduce full verification

```sh
go test -race ./...
go vet ./...
cargo test --locked --all-targets --manifest-path rust/Cargo.toml
cargo fmt --manifest-path rust/Cargo.toml --all -- --check
cargo clippy --locked --all-targets --manifest-path rust/Cargo.toml
python3 scripts/verify.py --jar /tmp/breakmock-wiremock-4.0.0-beta.40.jar --output-dir /tmp/go-compat
python3 scripts/verify.py --jar /tmp/breakmock-wiremock-4.0.0-beta.40.jar --candidate-language rust --output-dir /tmp/rust-compat
```

Both differential commands remain nonzero because the unchanged binary diagnostic
assertion fails. All other suites pass; this remains a bounded corpus rather than
a claim of compatibility with every possible WireMock request or extension.
