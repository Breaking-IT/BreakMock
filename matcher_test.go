// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestMatchRequest(t *testing.T) {
	tests := []struct {
		name, pattern string
		request       Request
		want          bool
	}{
		{"exact URL", `{"method":"GET","url":"/a?q=1"}`, Request{Method: "GET", URL: "/a?q=1"}, true},
		{"exact URL query significant", `{"url":"/a"}`, Request{URL: "/a?q=1"}, false},
		{"path ignores query", `{"urlPath":"/a"}`, Request{URL: "/a?q=1"}, true},
		{"full regex", `{"urlPattern":"/a"}`, Request{URL: "/ab"}, false},
		{"method mismatch", `{"method":"POST"}`, Request{Method: "GET", URL: "/"}, false},
		{"template and query", `{"urlPathTemplate":"/users/{id}","pathParameters":{"id":{"matches":"[0-9]+"}},"queryParameters":{"q":{"equalTo":"a b"}}}`, Request{URL: "/users/42?q=a+b"}, true},
		{"header case and alternatives", `{"headers":{"x-thing":{"equalTo":"yes"}}}`, Request{URL: "/", Headers: http.Header{"X-Thing": []string{"no", "yes"}}}, true},
		{"absent header", `{"headers":{"X-Thing":{"absent":true}}}`, Request{URL: "/"}, true},
		{"includes query", `{"queryParameters":{"q":{"includes":[{"equalTo":"1"},{"equalTo":"2"}]}}}`, Request{URL: "/?q=2&q=3&q=1"}, true},
		{"exact query count", `{"queryParameters":{"q":{"hasExactly":[{"equalTo":"1"}]}}}`, Request{URL: "/?q=1&q=2"}, false},
		{"basic auth", `{"basicAuthCredentials":{"username":"a","password":"b"}}`, Request{URL: "/", Headers: http.Header{"Authorization": []string{"Basic YTpi"}}}, true},
		{"cookie", `{"cookies":{"session":{"equalTo":"123"}}}`, Request{URL: "/", Headers: http.Header{"Cookie": []string{"session=123; x=y"}}}, true},
		{"form", `{"formParameters":{"a":{"equalTo":"hello world"}}}`, Request{URL: "/", Body: []byte("a=hello+world")}, true},
		{"JSON semantic", `{"bodyPatterns":[{"equalToJson":"{\"a\":[1,2],\"b\":true}","ignoreArrayOrder":true,"ignoreExtraElements":true}]}`, Request{URL: "/", Body: []byte(`{"c":3,"b":true,"a":[2,1]}`)}, true},
		{"JSON duplicate array", `{"bodyPatterns":[{"equalToJson":"[1,1]","ignoreArrayOrder":true}]}`, Request{URL: "/", Body: []byte(`[1,2]`)}, false},
		{"JSON nested", `{"bodyPatterns":[{"equalToJson":"{\"a\":{\"b\":1}}"}]}`, Request{URL: "/", Body: []byte(`{"a":{"b":1}}`)}, true},
		{"JSONPath filter", `{"bodyPatterns":[{"matchesJsonPath":"$.things[?(@.id == 2)]"}]}`, Request{URL: "/", Body: []byte(`{"things":[{"id":1},{"id":2}]}`)}, true},
		{"JSONPath nested", `{"bodyPatterns":[{"matchesJsonPath":{"expression":"$.name","contains":"ali"}}]}`, Request{URL: "/", Body: []byte(`{"name":"alice"}`)}, true},
		{"JSONPath missing", `{"bodyPatterns":[{"matchesJsonPath":"$.missing"}]}`, Request{URL: "/", Body: []byte(`{}`)}, false},
		{"JSONPath empty", `{"bodyPatterns":[{"matchesJsonPath":"$.things"}]}`, Request{URL: "/", Body: []byte(`{"things":[]}`)}, false},
		{"XML", `{"bodyPatterns":[{"equalToXml":"<a x='1' y='2'><b>hello</b></a>"}]}`, Request{URL: "/", Body: []byte(`<a y="2" x="1"> <b>hello</b> </a>`)}, true},
		{"XPath", `{"bodyPatterns":[{"matchesXPath":{"expression":"/a/b/text()","equalTo":"hello"}}]}`, Request{URL: "/", Body: []byte(`<a><b>hello</b></a>`)}, true},
		{"logical body", `{"bodyPatterns":[{"and":[{"contains":"hello"},{"not":{"contains":"bad"}}]}]}`, Request{URL: "/", Body: []byte("hello world")}, true},
		{"unsupported extension", `{"customMatcher":{"name":"java-extension"}}`, Request{URL: "/"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p map[string]any
			if err := json.Unmarshal([]byte(tt.pattern), &p); err != nil {
				t.Fatal(err)
			}
			if got := MatchRequest(p, tt.request); got != tt.want {
				t.Errorf("got %v want %v", got, tt.want)
			}
		})
	}
}
func TestMatchJSONPlaceholders(t *testing.T) {
	for _, tt := range []struct {
		want, got any
		matches   bool
	}{{"${json-unit.any-number}", float64(2), true}, {"${json-unit.any-number}", "2", false}, {"${json-unit.regex}[a-z]+", "abc", true}, {"${json-unit.any-boolean}", false, true}} {
		if matchJSON(tt.want, tt.got, false, false) != tt.matches {
			t.Errorf("%v vs %v", tt.want, tt.got)
		}
	}
}

func TestAdvancedMatchers(t *testing.T) {
	tests := []struct {
		name          string
		pattern       map[string]any
		value         string
		present, want bool
	}{
		{"lookahead", map[string]any{"matches": "a(?=b)b"}, "ab", true, true},
		{"backreference", map[string]any{"matches": `(a)\1`}, "aa", true, true},
		{"missing negative regex", map[string]any{"doesNotMatch": "abc"}, "", false, true},
		{"missing negative contains", map[string]any{"doesNotContain": "abc"}, "", false, true},
		{"schema required", map[string]any{"matchesJsonSchema": `{"type":"object","required":["id"]}`}, `{"id":1}`, true, true},
		{"schema missing", map[string]any{"matchesJsonSchema": `{"type":"object","required":["id"]}`}, `{}`, true, false},
		{"XPath namespace", map[string]any{"matchesXPath": "/n:a/n:b", "xPathNamespaces": map[string]any{"n": "urn:foo"}}, `<a xmlns="urn:foo"><b>yes</b></a>`, true, true},
		{"XPath nested XML", map[string]any{"matchesXPath": map[string]any{"expression": "/a/b", "equalToXml": "<b>yes</b>"}}, `<a><b>yes</b></a>`, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchValue(tt.pattern, tt.value, tt.present); got != tt.want {
				t.Errorf("got %v want %v", got, tt.want)
			}
		})
	}
}
func BenchmarkMatchRequestExact(b *testing.B) {
	pattern := map[string]any{"method": "GET", "url": "/test"}
	request := Request{Method: "GET", URL: "/test"}
	for b.Loop() {
		if !MatchRequest(pattern, request) {
			b.Fatal("no match")
		}
	}
}
func BenchmarkMatchRequestRegex(b *testing.B) {
	pattern := map[string]any{"method": "GET", "urlPattern": "/users/[0-9]+"}
	request := Request{Method: "GET", URL: "/users/123"}
	for b.Loop() {
		if !MatchRequest(pattern, request) {
			b.Fatal("no match")
		}
	}
}

func TestRegressionMatcherSemantics(t *testing.T) {
	for _, tc := range []struct {
		pattern, value string
		want           bool
	}{
		{`{"absent":false}`, "", true},
		{`{"equalToJson":"{\"a\":\"${json-unit.ignore-element}\"}"}`, `{}`, true},
		{`{"matchesJsonPath":{"expression":"$.a","equalTo":"b"}}`, `{"a":["a","b"]}`, true},
		{`{"matchesJsonPath":{"expression":"$.a[*]","equalToJson":"[1,2]"}}`, `{"a":[1,2]}`, true},
		{`{"matchesJsonPath":{"expression":"$.a","absent":true}}`, `{}`, true},
		{`{"equalToXml":"<a><b>1</b><c>2</c></a>"}`, `<a><c>2</c><b>1</b></a>`, true},
		{`{"equalToXml":"<a>${xmlunit.ignore}</a>","enablePlaceholders":true}`, `<a>123</a>`, true},
		{`{"equalToXml":"<a><![CDATA[hello]]></a>"}`, `<a>hello</a>`, false},
		{`{"matchesJsonSchema":{"type":"string"}}`, `123`, true},
		{`{"equalToDateTime":"2026-01-01T00:00:00Z","actualFormat":"unix"}`, `1767225600`, true},
		{`{"equalToDateTime":"2026-01-01T00:00:00Z","truncateActual":"first hour of day"}`, `2026-01-01T19:23:45Z`, true},
		{`{"matchesXPath":{"expression":"/a","equalTo":"<a>\n  <b>yes</b>\n</a>\n"}}`, `<a><b>yes</b></a>`, true},
	} {
		var pattern map[string]any
		if err := json.Unmarshal([]byte(tc.pattern), &pattern); err != nil {
			t.Fatal(err)
		}
		if got := matchValue(pattern, tc.value, tc.value != ""); got != tc.want {
			t.Errorf("pattern %s got %v want %v", tc.pattern, got, tc.want)
		}
	}
}
