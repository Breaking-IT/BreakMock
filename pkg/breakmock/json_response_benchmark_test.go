// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// largeJSONDocument returns a deterministic order listing of roughly size bytes.
// When templated, a few fields echo request values, as typical dynamic stubs do.
func largeJSONDocument(size int, template string) string {
	var b strings.Builder
	b.WriteString(`{"customer":`)
	switch template {
	case "plan":
		b.WriteString(`"{{request.query.customer}}"`)
	case "helper":
		b.WriteString(`"{{jsonPath request.body '$.customer'}}"`)
	default:
		b.WriteString(`"C-1001"`)
	}
	b.WriteString(`,"currency":"EUR","orders":[`)
	for i := 0; b.Len() < size; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"status":"SHIPPED","total":%d.%02d,"note":"Fragile é <handle> with care","items":[{"sku":"SKU-%05d","qty":%d,"price":19.95,"tags":["a","b"]},{"sku":"SKU-%05d","qty":1,"price":5.5,"tags":[]}],"address":{"street":"Main %d","city":"Amsterdam","zip":"1011AB"}}`,
			i, i*7%1000, i%100, i, i%5+1, i+1, i)
	}
	b.WriteString(`],"requestId":`)
	if template != "" {
		b.WriteString(`"{{request.headers.X-Request-Id}}"`)
	} else {
		b.WriteString(`"r-1"`)
	}
	b.WriteString(`}`)
	return b.String()
}

type jsonBenchCase struct {
	name     string
	mapping  func(doc string) string
	request  func() *http.Request
	files    bool
	template string
}

// gzipAvailable reports whether this build can produce WireMock's gzip bytes (cgo only).
func gzipAvailable() bool { _, err := wireGzip(nil); return err == nil }

func jsonBenchCases() []jsonBenchCase {
	get := func() *http.Request {
		r := httptest.NewRequest("GET", "/orders?customer=C-1001", nil)
		r.Header.Set("X-Request-Id", "r-1")
		return r
	}
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	cases := []jsonBenchCase{
		{name: "jsonBody", request: get, mapping: func(doc string) string {
			return `{"request":{"method":"GET","urlPath":"/orders"},"response":{"status":200,"headers":{"Content-Type":"application/json"},"jsonBody":` + doc + `}}`
		}},
		{name: "body", request: get, mapping: func(doc string) string {
			return `{"request":{"method":"GET","urlPath":"/orders"},"response":{"status":200,"headers":{"Content-Type":"application/json"},"body":` + quote(doc) + `}}`
		}},
		{name: "bodyFileName", files: true, request: get, mapping: func(string) string {
			return `{"request":{"method":"GET","urlPath":"/orders"},"response":{"status":200,"headers":{"Content-Type":"application/json"},"bodyFileName":"orders.json"}}`
		}},
		{name: "templateBody", template: "plan", request: get, mapping: func(doc string) string {
			return `{"request":{"method":"GET","urlPath":"/orders"},"response":{"status":200,"headers":{"Content-Type":"application/json"},"body":` + quote(doc) + `,"transformers":["response-template"]}}`
		}},
		{name: "templateFile", template: "plan", files: true, request: get, mapping: func(string) string {
			return `{"request":{"method":"GET","urlPath":"/orders"},"response":{"status":200,"headers":{"Content-Type":"application/json"},"bodyFileName":"orders.json","transformers":["response-template"]}}`
		}},
		{name: "templateHelper", template: "helper", request: func() *http.Request {
			r := httptest.NewRequest("POST", "/orders", strings.NewReader(`{"customer":"C-1001"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Request-Id", "r-1")
			return r
		}, mapping: func(doc string) string {
			return `{"request":{"method":"POST","urlPath":"/orders"},"response":{"status":200,"headers":{"Content-Type":"application/json"},"body":` + quote(doc) + `,"transformers":["response-template"]}}`
		}},
		{name: "jsonBodyGzip", request: func() *http.Request {
			r := get()
			r.Header.Set("Accept-Encoding", "gzip")
			return r
		}, mapping: func(doc string) string {
			return `{"request":{"method":"GET","urlPath":"/orders"},"response":{"status":200,"headers":{"Content-Type":"application/json"},"jsonBody":` + doc + `}}`
		}},
	}
	if !gzipAvailable() {
		cases = cases[:len(cases)-1]
	}
	return cases
}

func newJSONBenchServer(tb testing.TB, c jsonBenchCase, size int, journal bool) (*Server, []byte) {
	root := tb.TempDir()
	doc := largeJSONDocument(size, c.template)
	if c.files {
		os.MkdirAll(filepath.Join(root, "__files"), 0o755)
		if e := os.WriteFile(filepath.Join(root, "__files", "orders.json"), []byte(doc), 0o644); e != nil {
			tb.Fatal(e)
		}
	}
	s, e := NewServer(Options{RootDir: root, DisableJournal: !journal, MaxJournalEntries: 1000})
	if e != nil {
		tb.Fatal(e)
	}
	if _, e = s.Store.AddMappingDocument([]byte(c.mapping(doc))); e != nil {
		tb.Fatal(e)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, c.request())
	if w.Code != 200 {
		tb.Fatalf("%s: status %d %s", c.name, w.Code, w.Body.String())
	}
	return s, w.Body.Bytes()
}

// TestLargeJSONResponses pins the body of each benchmark workload so
// optimizations cannot change served bytes.
func TestLargeJSONResponses(t *testing.T) {
	for _, c := range jsonBenchCases() {
		for _, journal := range []bool{false, true} {
			s, first := newJSONBenchServer(t, c, 100<<10, journal)
			expected := []byte(largeJSONDocument(100<<10, ""))
			if strings.HasSuffix(c.name, "Gzip") {
				if !bytes.HasPrefix(first, []byte{0x1f, 0x8b}) {
					t.Fatalf("%s: response not gzip", c.name)
				}
				expected, _ = wireGzip(mustCompactJSON(t, expected))
			} else if c.name == "jsonBody" {
				expected = mustCompactJSON(t, expected)
			}
			if !bytes.Equal(first, expected) {
				t.Fatalf("%s journal=%v: body differs (%d vs %d bytes)", c.name, journal, len(first), len(expected))
			}
			for i := 0; i < 3; i++ {
				w := httptest.NewRecorder()
				s.ServeHTTP(w, c.request())
				if !bytes.Equal(w.Body.Bytes(), first) {
					t.Fatalf("%s: repeated response differs", c.name)
				}
			}
		}
	}
}

// TestLargeJSONResponsesConcurrent renders cached large templates and body files
// from many goroutines, each with its own request values.
func TestLargeJSONResponsesConcurrent(t *testing.T) {
	for _, c := range jsonBenchCases() {
		if c.template == "" && !c.files {
			continue
		}
		s, _ := newJSONBenchServer(t, c, 100<<10, true)
		var wg sync.WaitGroup
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 20; i++ {
					id := fmt.Sprintf("r-%d-%d", g, i)
					r := c.request()
					r.Header.Set("X-Request-Id", id)
					w := httptest.NewRecorder()
					s.ServeHTTP(w, r)
					want := `"requestId":"r-1"}`
					if c.template != "" {
						want = `"requestId":"` + id + `"}`
					}
					if w.Code != 200 || !strings.HasSuffix(w.Body.String(), want) || !strings.HasPrefix(w.Body.String(), `{"customer":"C-1001"`) {
						t.Errorf("%s: unexpected response %d ...%s", c.name, w.Code, w.Body.String()[max(0, w.Body.Len()-40):])
						return
					}
				}
			}()
		}
		wg.Wait()
	}
}

func mustCompactJSON(t *testing.T, data []byte) []byte {
	out, e := wireJSON(data)
	if e != nil {
		t.Fatal(e)
	}
	return out
}

type discardResponseWriter struct{ header http.Header }

func (w *discardResponseWriter) Header() http.Header         { return w.header }
func (w *discardResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *discardResponseWriter) WriteHeader(int)             {}

func BenchmarkLargeJSONResponse(b *testing.B) {
	for _, size := range []int{100 << 10, 1 << 20} {
		for _, journal := range []bool{false, true} {
			for _, c := range jsonBenchCases() {
				name := fmt.Sprintf("%s/%dKiB/journal=%v", c.name, size>>10, journal)
				b.Run(name, func(b *testing.B) {
					s, body := newJSONBenchServer(b, c, size, journal)
					b.SetBytes(int64(len(body)))
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						r := c.request()
						s.ServeHTTP(&discardResponseWriter{header: http.Header{}}, r)
						io.Copy(io.Discard, r.Body)
					}
				})
			}
		}
	}
}
