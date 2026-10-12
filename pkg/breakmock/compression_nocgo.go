//go:build !cgo

// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import "fmt"

// Refuse gzip rather than silently use a compressor with different output bytes.
func wireGzip([]byte) ([]byte, error) {
	return nil, fmt.Errorf("strict WireMock gzip requires a cgo build linked with zlib")
}
func wireZlibVersion() string { return "unavailable (CGO_ENABLED=0)" }
