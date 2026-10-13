package breakmock

import (
	"bytes"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"
)

func TestJavaDiagnosticGolden(t *testing.T) {
	s, e := NewServer(Options{RootDir: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	s.Store.AddMapping(Mapping{"id": "11111111-1111-4111-8111-111111111111", "request": map[string]any{"method": "GET", "url": "/expected"}, "response": map[string]any{"status": 200}})
	expected, e := os.ReadFile("../../testdata/diagnostics/url-mismatch.txt")
	if e != nil {
		t.Fatal(e)
	}
	actual := s.unmatchedBody(Request{Method: "GET", URL: "/actual"}, 119)
	if !bytes.Equal(expected, actual) {
		t.Fatalf("Java mismatch:\n%s\nGo:\n%s", expected, actual)
	}
	expected, e = os.ReadFile("../../testdata/diagnostics/near-miss.json")
	if e != nil {
		t.Fatal(e)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/__admin/near-misses/request", bytes.NewBufferString(`{"method":"GET","url":"/actual","headers":{}}`))
	s.ServeHTTP(response, request)
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), expected) {
		t.Fatalf("near miss %d Java:\n%s\nGo:\n%s", response.Code, expected, response.Body.Bytes())
	}
}
func TestTemplateErrorHasExactPlainBody(t *testing.T) {
	s, _ := NewServer(Options{RootDir: t.TempDir()})
	w := httptest.NewRecorder()
	s.writeTemplateError(w, &TemplateExecutionError{Message: "1:0: sample"}, Mapping{"id": "example"}, &Request{})
	if w.Code != 500 || w.Body.String() != "[ERROR] 1:0: sample" || w.Header().Get("Content-Type") != "text/plain" || w.Header().Get("Matched-Stub-Id") != "example" {
		t.Fatalf("%v %s", w.Result(), w.Body.String())
	}
}

func TestBinaryMismatchUsesJavaByteArrayIdentity(t *testing.T) {
	shape := regexp.MustCompile(`^\[B@[1-9a-f][0-9a-f]{0,7}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		identity := javaByteArrayIdentity()
		if !shape.MatchString(identity) {
			t.Fatalf("identity %q does not have Java's byte[] toString shape", identity)
		}
		seen[identity] = true
	}
	if len(seen) < 45 {
		t.Fatalf("identities should vary per call like JVM identity hashes, got %d distinct of 50", len(seen))
	}
}
