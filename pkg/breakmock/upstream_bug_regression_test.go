// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestXPathBooleanPredicates(t *testing.T) {
	for _, tc := range []struct {
		expression string
		want       bool
	}{
		{"false()", false}, {"true()", true}, {"1 = 2", false}, {"count(//planet) = 99", false},
		{"count(//planet) = 2", true}, {"contains('abc','z')", false}, {"boolean(//missing)", false},
		{"//planet", true}, {"//missing", false}, {"0", false}, {"1", true}, {"number('invalid')", false}, {"''", false}, {"'value'", true},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			got := matchXPath(tc.expression, nil, `<solar-system><planet/><planet/></solar-system>`)
			if got != tc.want {
				t.Fatalf("match %q = %v, want %v", tc.expression, got, tc.want)
			}
			object := map[string]any{"expression": tc.expression}
			if got := matchXPath(object, nil, `<solar-system><planet/><planet/></solar-system>`); got != tc.want {
				t.Fatalf("object predicate = %v, want %v", got, tc.want)
			}
			if distance := xpathMatchDistance(object, nil, `<solar-system><planet/><planet/></solar-system>`); (distance == 0) != tc.want {
				t.Fatalf("object distance %v disagrees with match %v", distance, tc.want)
			}
		})
	}
	if !matchXPath(map[string]any{"expression": "false()", "equalTo": "false"}, nil, "<root/>") {
		t.Fatal("explicit primitive value matcher must still work")
	}
}

func TestJSONPathAbsentDistinguishesEmptyArray(t *testing.T) {
	for _, tc := range []struct {
		path, body string
		want       bool
	}{
		{"$.foo", `{}`, true}, {"$.foo", `{"foo":[]}`, false}, {"$.foo", `{"foo":[1]}`, false},
		{"$.foo", `{"foo":{}}`, false}, {"$.foo", `{"foo":""}`, false},
		{"$.foo[*]", `{"foo":[]}`, true}, {"$.foo[0]", `{"foo":[]}`, true},
		{"$.foo[?(@.active==true)]", `{"foo":[{"active":false}]}`, true},
	} {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			got := matchJSONPath(map[string]any{"expression": tc.path, "absent": true}, tc.body)
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			distance := jsonPathMatchDistance(map[string]any{"expression": tc.path, "absent": true}, tc.body)
			if (distance == 0) != tc.want {
				t.Fatalf("distance %v disagrees with match %v", distance, tc.want)
			}
		})
	}
}

func TestToJSONNullIsValidJSON(t *testing.T) {
	for _, source := range []string{`{"field": {{toJson missing}} }`, `{"field": {{toJson parameters.value}} }`} {
		got, err := RenderTemplate(source, Request{URL: "/"}, map[string]any{"value": nil})
		if err != nil {
			t.Fatal(err)
		}
		if got != `{"field": null }` || !json.Valid([]byte(got)) {
			t.Fatalf("invalid null serialization: %q", got)
		}
	}
}

func TestAdminDocsTrailingSlash(t *testing.T) {
	s, err := NewServer(Options{RootDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, method := range []string{"GET", "HEAD"} {
		a, b := httptest.NewRecorder(), httptest.NewRecorder()
		s.ServeHTTP(a, httptest.NewRequest(method, "/__admin/docs", nil))
		s.ServeHTTP(b, httptest.NewRequest(method, "/__admin/docs/", nil))
		if b.Code != 200 || a.Body.String() != b.Body.String() || a.Header().Get("Content-Type") != b.Header().Get("Content-Type") {
			t.Fatalf("%s slash route differs: %d", method, b.Code)
		}
		if method == "GET" && !strings.Contains(b.Body.String(), `href="/__admin/docs/swagger"`) {
			t.Fatal("spec link must remain absolute")
		}
	}
}

func TestScenarioStateOpenAPI(t *testing.T) {
	w := httptest.NewRecorder()
	if !serveAdminAsset(w, httptest.NewRequest("GET", "/__admin/docs/swagger", nil)) {
		t.Fatal("missing spec")
	}
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	raw := spec.Paths["/__admin/scenarios/{name}/state"]["put"]
	if len(raw) == 0 {
		t.Fatal("scenario state PUT is missing from OpenAPI")
	}
	var operation struct {
		Responses  map[string]any `json:"responses"`
		Parameters []struct {
			Name     string
			In       string
			Required bool
		}
	}
	if err := json.Unmarshal(raw, &operation); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"200", "404", "422"} {
		if operation.Responses[status] == nil {
			t.Errorf("missing documented %s response", status)
		}
	}
	if len(operation.Parameters) != 1 || operation.Parameters[0].Name != "name" || operation.Parameters[0].In != "path" || !operation.Parameters[0].Required {
		t.Fatal("missing required scenario name")
	}
}

func TestCorrectedBugsThroughHTTP(t *testing.T) {
	s, err := NewServer(Options{RootDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mappings := []Mapping{
		{"request": map[string]any{"method": "POST", "url": "/xpath", "bodyPatterns": []any{map[string]any{"matchesXPath": "1 = 2"}}}, "response": map[string]any{"body": "wrong match"}},
		{"request": map[string]any{"method": "POST", "url": "/absent", "bodyPatterns": []any{map[string]any{"matchesJsonPath": map[string]any{"expression": "$.foo", "absent": true}}}}, "response": map[string]any{"body": "missing"}},
		{"request": map[string]any{"method": "GET", "url": "/null"}, "response": map[string]any{"body": `{"field": {{toJson missing}} }`, "transformers": []any{"response-template"}}},
	}
	for _, mapping := range mappings {
		if _, err := s.Store.AddMapping(mapping); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
		response           string
	}{
		{"POST", "/xpath", "<root/>", 404, ""},
		{"POST", "/absent", `{"foo":[]}`, 404, ""},
		{"POST", "/absent", `{}`, 200, "missing"},
		{"GET", "/null", "", 200, `{"field": null }`},
	} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != tc.status || (tc.response != "" && w.Body.String() != tc.response) {
			t.Errorf("%s %s: got %d %q", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
