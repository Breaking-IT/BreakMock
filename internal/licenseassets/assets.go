// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
// Package licenseassets embeds distribution notices into the standalone binary.
package licenseassets

import _ "embed"

// Notices includes the project license, project notice and third-party notices.
// Regenerate with python3 scripts/update_license_bundle.py.
//
//go:embed notices.txt
var Notices string
