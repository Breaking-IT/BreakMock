package breakmock

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminJSONJacksonFormatting(t *testing.T) {
	got, err := marshalAdminJSON(map[string]any{"mappings": []Mapping{{"id": "x", "request": map[string]any{"url": "/", "method": "ANY"}, "response": map[string]any{"status": 200}}}, "meta": map[string]any{"total": 1}})
	want := "{\n  \"mappings\" : [ {\n    \"id\" : \"x\",\n    \"request\" : {\n      \"url\" : \"/\",\n      \"method\" : \"ANY\"\n    },\n    \"response\" : {\n      \"status\" : 200\n    }\n  } ],\n  \"meta\" : {\n    \"total\" : 1\n  }\n}"
	if err != nil || string(got) != want {
		t.Fatalf("got %s err %v", got, err)
	}
}
func TestAdminPreservesUserMapOrder(t *testing.T) {
	s := NewStore()
	h := NewAdminHandler(s, t.TempDir())
	raw := `{"id":"11111111-1111-4111-8111-111111111111","request":{"url":"/","headers":{"Z":{"equalTo":"z"},"A":{"equalTo":"a"}}},"response":{"status":200},"metadata":{"z":1,"a":2}}`
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/__admin/mappings", strings.NewReader(raw)))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	body := w.Body.String()
	if strings.Index(body, `"Z"`) > strings.Index(body, `"A"`) || strings.Index(body, `"z" : 1`) > strings.Index(body, `"a" : 2`) {
		t.Fatal("source order lost", body)
	}
	if strings.HasSuffix(body, "\n") {
		t.Fatal("extra newline")
	}
	adminCall(t, h, "POST", "/mappings/save", "", 200)
	adminCall(t, h, "POST", "/mappings/reset", "", 200)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/__admin/mappings/11111111-1111-4111-8111-111111111111", nil))
	body = w.Body.String()
	if strings.Index(body, `"Z"`) > strings.Index(body, `"A"`) {
		t.Fatal("disk reload lost order", body)
	}
}
func TestJournalHeaderOrderAndSpelling(t *testing.T) {
	s := NewStore()
	s.RecordServed(Request{Method: "GET", URL: "/x?z=1&a=2", Headers: http.Header{"X-Z": []string{"z"}, "X-A": []string{"a"}}, HeaderOrder: []string{"x-z", "X-A"}}, nil, 200, http.Header{"X-Z": []string{"z"}, "X-A": []string{"a"}}, nil, []string{"x-z", "X-A"})
	h := NewAdminHandler(s, t.TempDir())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/__admin/requests", nil))
	body := w.Body.String()
	if strings.Contains(body, `"X-Z"`) || strings.Index(body, `"x-z"`) > strings.Index(body, `"X-A"`) {
		t.Fatal(body)
	}
	if strings.Index(body, `"z" : {`) > strings.Index(body, `"a" : {`) {
		t.Fatal("query order lost", body)
	}
}
func TestHeaderCasingKeepsFirstSpelling(t *testing.T) {
	headers := map[string]any{"X-Duplicate": []string{"first", "second"}}
	restoreHeaderCasing(headers, []string{"x-duplicate", "X-DUPLICATE", "x-duplicate"})
	if _, ok := headers["x-duplicate"]; !ok || len(headers) != 1 {
		t.Fatal(headers)
	}
}
