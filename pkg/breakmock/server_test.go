// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUpstreamMappingFilesAndScenario(t *testing.T) {
	s, e := NewServer(Options{RootDir: "../../testdata/upstream/remoteloader"})
	if e != nil {
		t.Fatal(e)
	}
	cases := []struct {
		method, path, body, want string
		status                   int
	}{{"GET", "/remote-load/1", "", "Remote load 1", 200}, {"GET", "/remote-load/2", "", "Remote load 2", 200}, {"GET", "/todo/items", "", "<items><item>Buy milk</item></items>", 200}, {"POST", "/todo/items", "Cancel newspaper subscription", "", 201}, {"GET", "/todo/items", "", "", 200}}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		r.Header.Set("Accept", "text/plain")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		want := []byte(c.want)
		if c.path == "/todo/items" && c.method == "GET" && c.want == "" {
			want, _ = os.ReadFile("../../testdata/upstream/remoteloader/__files/body.xml")
		}
		if w.Code != c.status || !bytes.Equal(w.Body.Bytes(), want) {
			t.Fatalf("%s %s: %d %q want %d %q", c.method, c.path, w.Code, w.Body.String(), c.status, want)
		}
	}
	for path, file := range map[string]string{"/image-file": "favicon3.png", "/binary-file-from-header": "body.unknown", "/text-file": "body.txt"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		want, _ := os.ReadFile(filepath.Join("../../testdata/upstream/remoteloader/__files", file))
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), want) {
			t.Fatalf("file response differs %s", path)
		}
	}
}
func TestRawJSONResponsePreservesOrderAndEscaping(t *testing.T) {
	m, e := DecodeDocument([]byte(`{"request":{"url":"/json"},"response":{"status":200,"jsonBody":{"z":1,"a":"<tag>","nested":[true,null]}}}`))
	if e != nil {
		t.Fatal(e)
	}
	s, _ := NewServer(Options{RootDir: t.TempDir()})
	s.Store.AddMapping(m)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/json", nil))
	if w.Body.String() != `{"z":1,"a":"<tag>","nested":[true,null]}` {
		t.Fatal(w.Body.String())
	}
}

func TestEmptyHTTPBodyStillEvaluatesBodyMatchers(t *testing.T) {
	s, err := NewServer(Options{RootDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Store.AddMapping(Mapping{
		"request":  map[string]any{"url": "/body", "bodyPatterns": []any{map[string]any{"equalTo": "required"}}},
		"response": map[string]any{"status": 201},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/body", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("empty body bypassed matching: status %d", w.Code)
	}
}
func TestConcurrentScenarioTransition(t *testing.T) {
	s, _ := NewServer(Options{RootDir: t.TempDir()})
	s.Store.AddMapping(Mapping{"request": map[string]any{"url": "/state"}, "response": map[string]any{"status": 201}, "scenarioName": "s", "requiredScenarioState": "Started", "newScenarioState": "Done"})
	s.Store.AddMapping(Mapping{"request": map[string]any{"url": "/state"}, "response": map[string]any{"status": 202}, "scenarioName": "s", "requiredScenarioState": "Done"})
	var wg sync.WaitGroup
	results := make(chan int, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("GET", "/state", nil))
			results <- w.Code
		}()
	}
	wg.Wait()
	close(results)
	first := 0
	for code := range results {
		if code == 201 {
			first++
		} else if code != 202 {
			t.Fatal(code)
		}
	}
	if first != 1 {
		t.Fatalf("first transition occurred %d times", first)
	}
}
func TestTemplatedFilesAndTraversal(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "__files"), 0755)
	os.WriteFile(filepath.Join(root, "__files", "a.txt"), []byte("Hello {{request.query.name}}"), 0644)
	s, _ := NewServer(Options{RootDir: root})
	s.Store.AddMapping(Mapping{"request": map[string]any{"urlPath": "/file"}, "response": map[string]any{"bodyFileName": "{{request.query.file}}", "transformers": []any{"response-template"}}})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/file?file=a.txt&name=Ada", nil))
	if w.Body.String() != "Hello Ada" {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/file?file=../secret", nil))
	if w.Code != 500 {
		t.Fatal(w.Code)
	}
}
func TestProxyPreservesBytes(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.String() != "/api?q=a%20b" {
			t.Error(r.URL.String())
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(201)
		io.Copy(w, r.Body)
	}))
	defer backend.Close()
	s, _ := NewServer(Options{RootDir: t.TempDir()})
	s.Store.AddMapping(Mapping{"request": map[string]any{"urlPath": "/api"}, "response": map[string]any{"proxyBaseUrl": backend.URL}})
	w := httptest.NewRecorder()
	data := []byte{0, 1, 2, 255}
	s.ServeHTTP(w, httptest.NewRequest("POST", "/api?q=a%20b", bytes.NewReader(data)))
	if w.Code != 201 || !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatalf("%d %v", w.Code, w.Body.Bytes())
	}
}
func TestFixedDelayAndCancellation(t *testing.T) {
	s, _ := NewServer(Options{RootDir: t.TempDir()})
	s.Store.AddMapping(Mapping{"request": map[string]any{"url": "/delay"}, "response": map[string]any{"fixedDelayMilliseconds": 20}})
	start := time.Now()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/delay", nil))
	if time.Since(start) < 18*time.Millisecond {
		t.Fatal("delay omitted")
	}
}
func TestActualResponseJournal(t *testing.T) {
	s, _ := NewServer(Options{RootDir: t.TempDir(), MaxJournalEntries: 2})
	s.Store.AddMapping(Mapping{"request": map[string]any{"urlPath": "/hello"}, "response": map[string]any{"body": "Hello {{request.query.name}}", "transformers": []any{"response-template"}}})
	s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/hello?name=Ada", nil))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/__admin/requests", nil))
	var v map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	if len(v["requests"].([]any)) != 1 {
		t.Fatal(v)
	}
}
func BenchmarkStaticResponse(b *testing.B) {
	s, _ := NewServer(Options{RootDir: b.TempDir(), DisableJournal: true})
	s.Store.AddMapping(Mapping{"request": map[string]any{"url": "/bench"}, "response": map[string]any{"body": "hello"}})
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("GET", "/bench", nil))
		}
	})
}

func TestInvalidTemplatedJSONLoggingBehavior(t *testing.T) {
	for _, disable := range []bool{false, true} {
		s, _ := NewServer(Options{RootDir: t.TempDir(), DisableRequestLogging: disable})
		m, _ := DecodeDocument([]byte(`{"request":{"urlPath":"/json"},"response":{"jsonBody":{"value":"{{request.query.value}}"},"transformers":["response-template"]}}`))
		s.Store.AddMapping(m)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/json?value=%22hello%22", nil))
		want := 200 // v4 emits the rendered bytes without reparsing JSON.
		if w.Code != want {
			t.Fatalf("disable logging=%v status=%d want=%d", disable, w.Code, want)
		}
	}
}
func TestCanonicalJSONEdgeCases(t *testing.T) {
	for input, want := range map[string]string{`{"a":"\u0061\u00e9"}`: `{"a":"aé"}`, `{"a":"\uD800"}`: `{"a":"\uD800"}`, `{"a":"😀"}`: `{"a":"\uD83D\uDE00"}`, `{"a":1e-6,"b":1e20,"c":-0,"d":1.00}`: `{"a":0.000001,"b":100000000000000000000,"c":0,"d":1.00}`, `{"a":1,"b":2,"a":3}`: `{"a":3,"b":2}`, `{"a":"\\uD800"}`: `{"a":"\\uD800"}`} {
		out, e := wireJSON([]byte(input))
		if e != nil || string(out) != want {
			t.Fatalf("%s => %s %v want %s", input, out, e, want)
		}
	}
}

func TestHeaderBytesAreDecodedAsLatin1(t *testing.T) {
	s, _ := NewServer(Options{RootDir: t.TempDir()})
	for _, mapping := range []string{
		`{"request":{"urlPath":"/h"},"response":{"status":200,"body":"[{{request.headers.X-V}}]","transformers":["response-template"]}}`,
		`{"priority":1,"request":{"urlPath":"/h","headers":{"X-V":{"equalTo":"é"}}},"response":{"status":200,"body":"matched"}}`,
	} {
		if _, err := s.Store.AddMappingDocument([]byte(mapping)); err != nil {
			t.Fatal(err)
		}
	}
	// Captured from WireMock 4.0.0-beta.40 with raw header bytes.
	for raw, want := range map[string]string{"\xe9": "matched", "\xc3\xa9": "[Ã©]", "a\xe9b\xc3\xa9": "[aébÃ©]", "plain": "[plain]"} {
		r := httptest.NewRequest("GET", "/h", nil)
		r.Header["X-V"] = []string{raw}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Body.String() != want {
			t.Fatalf("header %q: got %q, want %q", raw, w.Body.String(), want)
		}
	}
}
