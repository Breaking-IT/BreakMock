// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import _ "embed"

//go:embed LICENSE
var projectLicense string

//go:embed NOTICE.txt
var projectNotice string

//go:embed third_party/NOTICES.txt
var dependencyNotices string

// LicenseNotices returns the license texts shipped inside the standalone binary.
func LicenseNotices() string {
	return projectLicense + "\n" + projectNotice + "\nThird-party notices\n\n" + dependencyNotices
}
