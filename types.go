// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import "net/http"

// Mapping preserves the WireMock JSON representation, including extension fields.
type Mapping = map[string]any

// Request captures the bytes and metadata used for matching and journaling.
type Request struct {
	UnmatchedReport      string
	UnmatchedContentType string
	ProcessTimeMillis    int64
	AddedDelayMillis     int64
	ClientIP             string
	PathParams           map[string]string
	Method               string
	URL                  string
	AbsoluteURL          string
	Headers              http.Header
	HeaderOrder          []string
	Body                 []byte
}
