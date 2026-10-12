// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"os"
	"strings"
	"testing"
)

func TestEmbeddedToxiproxyLicense(t *testing.T) {
	want, err := os.ReadFile("../../third_party/toxiproxy/LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(LicenseNotices(), string(want)) {
		t.Fatal("standalone executable must retain the complete upstream Toxiproxy MIT notice")
	}
}

func TestEmbeddedNoticesMatchCanonicalFiles(t *testing.T) {
	for _, path := range []string{"../../LICENSE", "../../NOTICE.txt", "../../third_party/NOTICES.txt"} {
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(LicenseNotices(), string(want)) {
			t.Fatalf("embedded notices differ from %s; run python3 scripts/update_license_bundle.py", path)
		}
	}
}
