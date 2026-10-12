// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"os"
	"strings"
	"testing"
)

func TestEmbeddedToxiproxyLicense(t *testing.T) {
	want, err := os.ReadFile("third_party/toxiproxy/LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(LicenseNotices(), string(want)) {
		t.Fatal("standalone executable must retain the complete upstream Toxiproxy MIT notice")
	}
}
