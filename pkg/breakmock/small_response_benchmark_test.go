// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// BenchmarkSmallResponse mirrors scripts/benchmark.py: a 17-byte static body and
// a small template, with the journal disabled or capped at 1,000 entries.
func BenchmarkSmallResponse(b *testing.B) {
	workloads := map[string]string{
		"static":   `{"request":{"method":"GET","urlPath":"/bench"},"response":{"status":200,"body":"Hello, benchmark!","headers":{"Content-Type":"text/plain"}}}`,
		"template": `{"request":{"method":"GET","urlPath":"/bench"},"response":{"status":200,"body":"Hello {{request.query.name}}: {{math 2 '+' 3}}","transformers":["response-template"],"headers":{"Content-Type":"text/plain"}}}`,
	}
	for _, journal := range []bool{false, true} {
		for _, name := range []string{"static", "template"} {
			b.Run(fmt.Sprintf("%s/journal=%v", name, journal), func(b *testing.B) {
				s, _ := NewServer(Options{RootDir: b.TempDir(), DisableJournal: !journal, MaxJournalEntries: 1000})
				if _, err := s.Store.AddMappingDocument([]byte(workloads[name])); err != nil {
					b.Fatal(err)
				}
				request := func() *http.Request {
					r := httptest.NewRequest("GET", "/bench?name=Ada", nil)
					r.Header.Set("User-Agent", "Go-http-client/1.1")
					r.Header.Set("Accept-Encoding", "identity")
					return r
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					s.ServeHTTP(&discardResponseWriter{header: http.Header{}}, request())
				}
			})
		}
	}
}
