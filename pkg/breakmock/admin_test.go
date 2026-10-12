package breakmock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func adminCall(t *testing.T, h http.Handler, method, path, body string, status int) map[string]any {
	t.Helper()
	r := httptest.NewRequest(method, "/__admin"+path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s status=%d body=%s", method, path, w.Code, w.Body.String())
	}
	var v map[string]any
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
	}
	return v
}
func TestAdminMappingCRUDAndPersistence(t *testing.T) {
	s := NewStore()
	h := NewAdminHandler(s, t.TempDir())
	m := adminCall(t, h, "POST", "/mappings", `{"request":{"method":"GET","url":"/a"},"response":{"status":201,"body":"hello"}}`, 201)
	id := m["id"].(string)
	adminCall(t, h, "GET", "/mappings/"+id, "", 200)
	v := adminCall(t, h, "GET", "/mappings", "", 200)
	if len(v["mappings"].([]any)) != 1 {
		t.Fatal(v)
	}
	adminCall(t, h, "PUT", "/mappings/"+id, `{"request":{"method":"GET","url":"/b"},"response":{"status":202}}`, 200)
	adminCall(t, h, "POST", "/mappings/save", "", 200)
	adminCall(t, h, "DELETE", "/mappings/"+id, "", 200)
	adminCall(t, h, "GET", "/mappings/"+id, "", 404)
	adminCall(t, h, "POST", "/mappings/reset", "", 200)
	if got := s.GetMapping(id); got != nil {
		t.Fatal("deleted persisted mapping returned", got)
	}
}
func TestAdminJournalFindCountRemove(t *testing.T) {
	s := NewStore()
	h := NewAdminHandler(s, "")
	s.Record(Request{Method: "GET", URL: "/a"}, nil, 404)
	s.Record(Request{Method: "POST", URL: "/a"}, nil, 404)
	v := adminCall(t, h, "POST", "/requests/count", `{"method":"GET","url":"/a"}`, 200)
	if v["count"] != float64(1) {
		t.Fatal(v)
	}
	v = adminCall(t, h, "POST", "/requests/find", `{"method":"POST"}`, 200)
	if len(v["requests"].([]any)) != 1 {
		t.Fatal(v)
	}
	adminCall(t, h, "POST", "/requests/remove", `{"method":"POST"}`, 200)
	v = adminCall(t, h, "GET", "/requests", "", 200)
	if len(v["requests"].([]any)) != 1 {
		t.Fatal(v)
	}
	adminCall(t, h, "DELETE", "/requests", "", 200)
	if len(s.journalSnapshot()) != 0 {
		t.Fatal("journal not reset")
	}
}
func TestStorePriorityAndConcurrentAccess(t *testing.T) {
	s := NewStore()
	for i := 0; i < 3; i++ {
		_, err := s.AddMapping(Mapping{"id": fmt.Sprint(i), "priority": float64(5 - i), "request": map[string]any{}, "response": map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if s.Mappings()[0]["id"] != "2" {
		t.Fatal("priority order")
	}
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				s.Record(Request{Method: "GET", URL: "/"}, nil, 404)
				s.SetScenarioState("state", fmt.Sprint(n))
				_ = s.ScenarioState("state")
				_ = s.Mappings()
				_ = s.journalSnapshot()
			}
		}(n)
	}
	wg.Wait()
	if len(s.journalSnapshot()) != 240 {
		t.Fatal("lost journal records")
	}
}
func TestJournalLimitsAndDisable(t *testing.T) {
	s := NewStore()
	s.ConfigureJournal(true, 2)
	for i := 0; i < 5; i++ {
		s.Record(Request{URL: fmt.Sprint(i)}, nil, 404)
	}
	if got := s.journalSnapshot(); len(got) != 2 || got[0].request.URL != "4" {
		t.Fatal(got)
	}
	s.ConfigureJournal(false, 0)
	s.Record(Request{}, nil, 404)
	if len(s.journalSnapshot()) != 0 {
		t.Fatal("disabled journal recorded")
	}
}
func TestImportIgnoreAndDelete(t *testing.T) {
	s := NewStore()
	h := NewAdminHandler(s, "")
	adminCall(t, h, "POST", "/mappings/import", `{"mappings":[{"id":"a","request":{},"response":{"status":201}},{"id":"b","request":{},"response":{"status":202}}]}`, 200)
	adminCall(t, h, "POST", "/mappings/import", `{"mappings":[{"id":"a","request":{},"response":{"status":500}}],"importOptions":{"duplicatePolicy":"IGNORE","deleteAllNotInImport":true}}`, 200)
	if s.GetMapping("b") != nil || s.GetMapping("a")["response"].(map[string]any)["status"] != float64(201) {
		t.Fatal(s.Mappings())
	}
}
func TestAdminMetadataAndUnmatched(t *testing.T) {
	s := NewStore()
	h := NewAdminHandler(s, t.TempDir())
	m := adminCall(t, h, "POST", "/mappings", `{"request":{"url":"/a"},"response":{},"metadata":{"suite":"a"}}`, 201)
	adminCall(t, h, "POST", "/mappings", `{"request":{"url":"/b"},"response":{},"metadata":{"suite":"b"}}`, 201)
	s.Record(Request{URL: "/a", Method: "GET"}, m, 200)
	v := adminCall(t, h, "GET", "/mappings/unmatched", "", 200)
	if len(v["mappings"].([]any)) != 1 {
		t.Fatal(v)
	}
	v = adminCall(t, h, "POST", "/mappings/find-by-metadata", `{"matchesJsonPath":{"expression":"$.suite","equalTo":"a"}}`, 200)
	if len(v["mappings"].([]any)) != 1 {
		t.Fatal(v)
	}
	adminCall(t, h, "POST", "/requests/remove-by-metadata", `{"equalToJson":"{\"suite\":\"a\"}"}`, 200)
	if len(s.journalSnapshot()) != 0 {
		t.Fatal("metadata remove failed")
	}
	adminCall(t, h, "POST", "/mappings/remove-by-metadata", `{"equalToJson":"{\"suite\":\"b\"}"}`, 200)
	if len(s.Mappings()) != 1 {
		t.Fatal(s.Mappings())
	}
}
func TestAdminFilesAndTraversal(t *testing.T) {
	h := NewAdminHandler(NewStore(), t.TempDir())
	for _, method := range []string{"PUT", "GET", "DELETE"} {
		r := httptest.NewRequest(method, "/__admin/files/sub/a.bin", strings.NewReader("hello"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s %d %s", method, w.Code, w.Body)
		}
		if method == "GET" && w.Body.String() != "hello" {
			t.Fatal(w.Body)
		}
	}
	r := httptest.NewRequest("PUT", "/__admin/files/../../outside", strings.NewReader("oops"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
func TestAdminResetReloadsDiskAndClearsRequests(t *testing.T) {
	dir := t.TempDir()
	s := NewStore()
	h := NewAdminHandler(s, dir)
	m := adminCall(t, h, "POST", "/mappings", `{"request":{"url":"/persisted"},"response":{"status":200}}`, 201)
	adminCall(t, h, "POST", "/mappings/save", "", 200)
	s.Record(Request{Method: "GET", URL: "/persisted"}, m, 200)
	adminCall(t, h, "POST", "/mappings", `{"request":{"url":"/temporary"},"response":{"status":200}}`, 201)
	adminCall(t, h, "POST", "/reset", "", 200)
	if len(s.Mappings()) != 1 || len(s.journalSnapshot()) != 0 {
		t.Fatal("reset did not restore persisted mappings/clear journal")
	}
	if s.Mappings()[0]["persistent"] != true {
		t.Fatal("save did not mark persistent")
	}
	adminCall(t, h, "DELETE", "/mappings", "", 200)
	adminCall(t, h, "POST", "/reset", "", 200)
	if len(s.Mappings()) != 0 {
		t.Fatal("delete all did not delete disk mappings")
	}
}
func TestEditAndSavePreserveMappingPrecedence(t *testing.T) {
	s := NewStore()
	h := NewAdminHandler(s, t.TempDir())
	a := adminCall(t, h, "POST", "/mappings", `{"request":{"url":"/same"},"response":{"body":"old"}}`, 201)
	b := adminCall(t, h, "POST", "/mappings", `{"request":{"url":"/same"},"response":{"body":"new"}}`, 201)
	adminCall(t, h, "PUT", "/mappings/"+a["id"].(string), `{"request":{"url":"/same"},"response":{"body":"edited"}}`, 200)
	if s.Mappings()[0]["id"] != b["id"] {
		t.Fatal("edit promoted old mapping")
	}
	adminCall(t, h, "POST", "/mappings/save", "", 200)
	if s.Mappings()[0]["id"] != b["id"] {
		t.Fatal("save changed precedence")
	}
}
func TestRemovePersistedBulkMappingPreservesOthers(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "mappings"), 0755); err != nil {
		t.Fatal(err)
	}
	raw := `{"mappings":[{"id":"a","request":{"url":"/a"},"response":{"status":200}},{"id":"b","request":{"url":"/b"},"response":{"status":200},"metadata":{"z":1,"a":2}}]}`
	file := filepath.Join(dir, "mappings", "bulk.json")
	if err := os.WriteFile(file, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	s := NewStore()
	h := NewAdminHandler(s, dir)
	adminCall(t, h, "POST", "/mappings/reset", "", 200)
	adminCall(t, h, "DELETE", "/mappings/a", "", 200)
	adminCall(t, h, "POST", "/mappings/reset", "", 200)
	if s.GetMapping("a") != nil || s.GetMapping("b") == nil {
		t.Fatal("wrong persisted mappings retained")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(string(b), `"z" : 1`) > strings.Index(string(b), `"a" : 2`) {
		t.Fatal("unrelated metadata order changed", string(b))
	}
	adminCall(t, h, "POST", "/mappings/remove-by-metadata", `{"equalToJson":"{\"z\":1,\"a\":2}"}`, 200)
	adminCall(t, h, "POST", "/mappings/reset", "", 200)
	if len(s.Mappings()) != 0 {
		t.Fatal("metadata removed mapping resurrected")
	}
}
func TestBoundedJournalKeepsNewestAfterManyWritesAndRemoval(t *testing.T) {
	s := NewStore()
	s.ConfigureJournal(true, 3)
	for i := 0; i < 1000; i++ {
		s.Record(Request{URL: fmt.Sprint(i)}, nil, 200)
	}
	entries := s.journalSnapshot()
	if len(entries) != 3 || entries[0].request.URL != "999" || entries[2].request.URL != "997" {
		t.Fatal(entries)
	}
	s.removeEvents(map[string]bool{entries[1].event["id"].(string): true})
	s.Record(Request{URL: "1000"}, nil, 200)
	entries = s.journalSnapshot()
	if len(entries) != 3 || entries[0].request.URL != "1000" || entries[1].request.URL != "999" || entries[2].request.URL != "997" {
		t.Fatal(entries)
	}
}
