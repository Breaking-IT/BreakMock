# Corrections to upstream WireMock bugs

BreakMock corrects these confirmed bugs by default. These are intentional
behavior differences from the pinned Java WireMock 4.0.0-beta.40 reference,
not claims that every response remains byte-identical to Java.

| Upstream issue | Correction |
| --- | --- |
| [#3510](https://github.com/wiremock/wiremock/issues/3510) | A scalar `matchesXPath` predicate uses XPath boolean conversion: false, zero, NaN and empty strings do not match. Node-set matching and explicitly nested value matchers retain their behavior. |
| [#3159](https://github.com/wiremock/wiremock/issues/3159) | `matchesJsonPath` with `absent` does not match a present empty array selected by a definite path such as `$.foo`. Empty wildcard/filter selections and missing properties still count as absent. Near-miss distance agrees with the corrected match. |
| [#3282](https://github.com/wiremock/wiremock/issues/3282) | `toJson` emits the literal `null` for null/missing input, producing valid JSON when used as a value. Existing non-null serialization is unchanged. |
| [#3332](https://github.com/wiremock/wiremock/issues/3332) | GET and HEAD on `/__admin/docs/` work like `/__admin/docs`, including absolute links to the specification and Swagger UI. |
| [#3319](https://github.com/wiremock/wiremock/issues/3319) | The bundled OpenAPI specification documents `PUT /__admin/scenarios/{name}/state`, its optional body/reset behavior, and its 200/404/422 responses. The endpoint implementation is unchanged. |

## Compatibility and migration

Mappings that relied on false XPath expressions matching, or an empty array being
absent, will select differently. Templates that relied on null `toJson` input
rendering no characters now receive `null`. Use an explicit conditional when an
empty output is required. There is no legacy-bug compatibility switch.

Changing the served specification necessarily changes its response bytes. The
original upstream asset hash is retained in `internal/adminassets/PROVENANCE.json`,
with the modified specification hash recorded separately.

The existing live Java differential scripts are unchanged: they still report
these differences as failures, without filtering cases, normalizing the changed
output, or rewriting the Java reference. The pre-existing JVM byte-array identity
diagnostic also remains a strict mismatch. Therefore strict parity CI is not
expected to be green solely because these correctness fixes pass.

Captured Java matcher fixture results are retained. Two cases now also record
an explicit `breakmockMatches: false` expectation for #3159, which Go unit tests
assert. The live Java comparison continues to compare actual server results.

Regression tests in `pkg/breakmock/upstream_bug_regression_test.go` cover positive
and negative predicates, empty-array versus empty-selection semantics, valid null
JSON, HTTP routing, documentation links and the OpenAPI operation. Run:

```sh
go test -race ./...
go vet ./...
python3 scripts/verify.py --jar /tmp/wiremock.jar --output-dir /tmp/compatibility
```

## Validation results

Go unit/race tests, vet, all 11 Toxiproxy executable integration checks and the
Docker build pass. The full unchanged Java suite passes 15 of 19 invocations:
matcher has two intentional empty-array differences, templates have two null
serialization differences, admin has the updated specification, and diagnostics
retains the existing JVM-identity mismatch. The final matcher/distance rerun
confirms the same two differences and no distance-suite failures.
See [the validation summary](../testdata/reports/upstream-bug-fixes/validation.json).
