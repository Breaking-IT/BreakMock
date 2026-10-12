// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"net/http"
	"testing"
)

func BenchmarkJournalRecordServed(b *testing.B) {
	s := NewStore()
	s.ConfigureJournal(true, 1000)
	m, _ := s.AddMapping(Mapping{"request": map[string]any{"method": "GET", "urlPath": "/bench"}, "response": map[string]any{"status": 200, "body": "Hello, benchmark!"}})
	r := Request{Method: "GET", URL: "/bench?name=Ada", AbsoluteURL: "http://localhost:8080/bench?name=Ada", Headers: http.Header{"Host": {"localhost:8080"}, "User-Agent": {"benchmark"}}, HeaderOrder: []string{"Host", "User-Agent"}, ClientIP: "127.0.0.1"}
	headers := http.Header{"Content-Type": {"text/plain"}}
	body := []byte("Hello, benchmark!")
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.RecordServed(r, m, 200, headers, body)
		}
	})
}
