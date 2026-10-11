// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
// Embedded upstream asset provenance and third-party licenses accompany the files.
package adminassets

import "embed"

//go:embed doc-index.html swagger assets NOTICE.txt jquery-LICENSE.txt PROVENANCE.json
var Files embed.FS
