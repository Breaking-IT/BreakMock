// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import "github.com/Breaking-IT/BreakMock/internal/licenseassets"

// LicenseNotices returns the license texts shipped inside the standalone binary.
func LicenseNotices() string {
	return licenseassets.Notices
}
