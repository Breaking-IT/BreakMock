// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestResponseTemplates(t *testing.T) {
	r := Request{Method: "POST", URL: "/items/42?q=one&q=two", AbsoluteURL: "https://example.test/items/42?q=one&q=two", Headers: http.Header{"X-Name": []string{"Alice", "Bob"}, "Cookie": []string{"session=abc"}}, Body: []byte(`{"name":"A&B","items":[1,2]}`)}
	tests := []struct{ s, w string }{
		{`{{request.method}} {{request.path}} {{request.pathSegments.[1]}} {{request.query.q}} {{request.query.q.[1]}}`, `POST /items/42 42 one two`},
		{`{{request.headers.X-Name}} {{request.headers.x-name.last}} {{request.cookies.session}}`, `Alice Bob abc`},
		{`{{jsonPath request.body '$.name'}}|{{{jsonPath request.body '$.name'}}}`, `A&B|A&B`},
		{`{{#assign 'name'}}{{upper 'alice'}}{{/assign}}{{name}}`, `ALICE`},
		{`{{val (jsonPath request.body '$.items') assign='items'}}{{#each items}}{{this}}{{/each}}`, `12`},
		{`{{#parseJson 'obj'}}{"a":"hello"}{{/parseJson}}{{obj.a}}`, `hello`},
		{`{{math 2 '+' 3}} {{#each (range 1 3)}}{{this}}{{/each}}`, `5 123`},
		{`{{base64 'hello'}} {{base64 'aGVsbG8=' decode=true}}`, `aGVsbG8= hello`},
		{`{{regexExtract 'abc123' '([0-9]+)'}}`, `123`},
		{`{{date (parseDate '2020-02-03' format='yyyy-MM-dd') format='yyyy/MM/dd'}}`, `2020/02/03`},
		{`{{val missing default='fallback'}}`, `fallback`},
		{`{{#if (contains 'abc' 'b')}}yes{{else}}no{{/if}}`, `yes`},
		{`{{xPath '<a><b>value</b></a>' '/a/b'}}`, "<b>value</b>\n"},
	}
	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			got, e := RenderTemplate(tt.s, r, nil)
			if e != nil || got != tt.w {
				t.Fatalf("got %q, error %v; want %q", got, e, tt.w)
			}
		})
	}
}
func TestTemplateRandomAndErrors(t *testing.T) {
	s, e := RenderTemplate(`{{randomValue length=100 type='NUMERIC'}}`, Request{URL: "/"}, nil)
	if e != nil || len(s) != 100 || strings.Trim(s, "0123456789") != "" {
		t.Fatalf("random: %q %v", s, e)
	}
	for _, src := range []string{`{{randomInt lower=5 upper=5}}`, `{{#if}}`} {
		if _, e := RenderTemplate(src, Request{URL: "/"}, nil); e == nil {
			t.Fatalf("expected error for %s", src)
		}
	}
}
func TestTemplateAssignmentIsolation(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, e := RenderTemplate(`{{val parameters.name assign='name'}}{{name}}`, Request{URL: "/"}, map[string]any{"name": "local"})
			if e != nil || got != "local" {
				t.Errorf("got %q %v", got, e)
			}
		}()
	}
	wg.Wait()
}

func BenchmarkRenderTemplate(b *testing.B) {
	r := Request{Method: "GET", URL: "/bench?name=Ada", AbsoluteURL: "http://localhost/bench?name=Ada"}
	for i := 0; i < b.N; i++ {
		_, err := RenderTemplate("Hello {{request.query.name}}: {{math 2 '+' 3}}", r, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestTemplatePlanMatchesFullEvaluator(t *testing.T) {
	requests := []Request{
		{Method: "GET", URL: "/one/two?q=first&q=last&empty=", AbsoluteURL: "https://example.test:0080/one/two?q=first&q=last&empty=", Headers: http.Header{"X-Test": []string{"a", "b"}, "Cookie": []string{"session=one; session=two"}}, Body: []byte("<tag>&\"\n")},
		{Method: "POST", URL: "/", Body: []byte{}},
	}
	sources := []string{
		"{{request.method}}:{{request.url}}:{{request.absoluteUrl}}:{{request.body}}",
		"{{request.host}}:{{request.port}}:{{request.scheme}}:{{request.baseUrl}}",
		"{{request.path}}:{{request.path.[0]}}:{{request.path.[20]}}:{{request.pathSegments}}:{{request.pathSegments.[1]}}",
		"{{request.query.q}}:{{request.query.q.[1]}}:{{request.query.q.last}}:{{request.query.q.first}}:{{request.query.q.size}}",
		"{{request.query.missing}}:{{request.query.missing.size}}:{{request.query.empty}}",
		"{{request.headers.x-TeSt}}:{{request.headers.X-Test.last}}:{{request.headers.X-Test.size}}:{{request.cookies.session}}",
		"Hello {{request.query.q}}: {{math 2 '+' 3}}",
		"{{! comment }}{{lower 'HELLO'}}/{{math 7 '/' 2}}/{{base64 'hello'}}",
		"{{replace 'a}}b' 'b' 'c'}}", "static text",
	}
	for _, source := range sources {
		t.Run(source, func(t *testing.T) {
			plan := compileTemplatePlan(source)
			if plan == nil {
				t.Fatal("expected plan")
			}
			for _, request := range requests {
				got, e := plan.render(request)
				want, we := renderTemplateFull(source, request, nil)
				if e != nil || we != nil || got != want {
					t.Fatalf("optimized=%q,%v full=%q,%v", got, e, want, we)
				}
			}
		})
	}
	for _, source := range []string{"{{now}}", "{{randomInt}}", "{{randomValue}}", "{{pickRandom 'a' 'b'}}", "{{#assign 'x'}}v{{/assign}}{{x}}", "{{math request.query.n '+' 3}}", "{{#each request.query.q}}{{this}}{{/each}}", "{{~request.body}}", "{{parameters.value}}"} {
		if compileTemplatePlan(source) != nil {
			t.Errorf("unsafe plan for %s", source)
		}
	}
}
