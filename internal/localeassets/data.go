// Package localeassets holds generated observable reference-JDK locale data.
package localeassets

import _ "embed"

// DateLocales contains observable locale data generated using OpenJDK27.
// The historical generator is retained on the codex/rust-port branch at
// scripts/GenerateDateLocales.java; no JDK is needed to load this data.
// Locale conventions can differ across JDK/CLDR versions.
//
//go:embed java-date-locales.json.gz
var DateLocales []byte
