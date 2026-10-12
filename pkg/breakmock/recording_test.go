package breakmock

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecordingProxyAndReplay(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"from":"upstream"}`))
	}))
	s, err := NewServer(Options{RootDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	h := NewAdminHandler(s.Store, s.options.RootDir)
	spec, _ := json.Marshal(map[string]any{"targetBaseUrl": upstream.URL, "persist": false})
	adminCall(t, h, "POST", "/recordings/start", string(spec), 200)
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/capture?a=1", nil))
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body)
		}
	}
	v := adminCall(t, h, "POST", "/recordings/stop", "", 200)
	if len(v["mappings"].([]any)) != 2 {
		t.Fatal(v)
	}
	upstream.Close()
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/capture?a=1", nil))
		if w.Code != 202 || w.Body.String() != `{"from":"upstream"}` {
			t.Fatal(w.Code, w.Body)
		}
	}
	adminCall(t, h, "POST", "/recordings/stop", "", 400)
}
func TestSnapshotFilteringAndIDs(t *testing.T) {
	s := NewStore()
	m, _ := s.AddMapping(Mapping{"request": map[string]any{"url": "/a"}, "response": map[string]any{"status": 200}})
	s.RecordServed(Request{URL: "/a", Method: "POST", Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"a":1}`)}, m, 201, http.Header{"Content-Type": []string{"text/plain"}}, []byte("ok"))
	h := NewAdminHandler(s, t.TempDir())
	v := adminCall(t, h, "POST", "/recordings/snapshot", `{"persist":false}`, 200)
	if len(v["mappings"].([]any)) != 0 {
		t.Fatal("local stub should be filtered")
	}
	v = adminCall(t, h, "POST", "/recordings/snapshot", `{"persist":false,"filters":{"allowNonProxied":true},"outputFormat":"ids"}`, 200)
	ids := v["ids"].([]any)
	if len(ids) != 1 {
		t.Fatal(v)
	}
	got := s.GetMapping(ids[0].(string))
	req := got["request"].(map[string]any)
	if !MatchRequest(req, Request{URL: "/a", Method: "POST", Body: []byte(`{"extra":true,"a":1}`)}) {
		t.Fatal(req)
	}
}
func TestRecordingUnregisteredTransformersAreIgnored(t *testing.T) {
	h := NewAdminHandler(NewStore(), t.TempDir())
	r := httptest.NewRequest("POST", "/__admin/recordings/start", strings.NewReader(`{"transformers":["custom"]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	b, _ := io.ReadAll(w.Result().Body)
	if w.Code != 200 {
		t.Fatal(w.Code, string(b))
	}
}

func TestSnapshotExtractedBodyRetainsBytes(t *testing.T) {
	root := t.TempDir()
	s := NewStore()
	m, _ := s.AddMapping(Mapping{"request": map[string]any{"url": "/extract"}, "response": map[string]any{"status": 200}})
	s.RecordServed(Request{URL: "/extract", Method: "GET"}, m, 200, http.Header{"Content-Type": []string{"text/plain"}}, []byte("é😀"))
	h := NewAdminHandler(s, root)
	v := adminCall(t, h, "POST", "/recordings/snapshot", `{"persist":false,"filters":{"allowNonProxied":true},"extractBodyCriteria":{"textSizeThreshold":2}}`, 200)
	mapping := v["mappings"].([]any)[0].(map[string]any)
	response := mapping["response"].(map[string]any)
	filename, ok := response["bodyFileName"].(string)
	if !ok {
		t.Fatal("UTF16 threshold must count supplementary character as two units", response)
	}
	r := httptest.NewRequest("GET", "/__admin/files/"+filename, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "é😀" {
		t.Fatalf("extracted bytes %d %q", w.Code, w.Body.String())
	}
}
