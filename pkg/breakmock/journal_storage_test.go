// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestJournalWrapReconfigurationAndSnapshotIsolation(t *testing.T) {
	s := NewStore()
	s.ConfigureJournal(true, 3)
	add := func(n int) string { return s.RecordServed(Request{URL: fmt.Sprint(n)}, nil, 200, nil, nil) }
	check := func(want ...string) {
		t.Helper()
		entries := s.journalSnapshot()
		got := make([]string, len(entries))
		for i, e := range entries {
			got[i] = e.request.URL
		}
		if !slices.Equal(got, want) {
			t.Fatalf("journal URLs %v, want %v", got, want)
		}
	}
	for i := 0; i < 8; i++ {
		add(i)
	}
	check("7", "6", "5")
	old := s.journalSnapshot()
	id := old[1].event["id"].(string)
	s.AppendSubEvent(id, "ERROR", map[string]any{"message": "test"})
	if len(old[1].event["subEvents"].([]any)) != 0 {
		t.Fatal("subevent mutated prior snapshot")
	}
	if len(s.journalSnapshot()[1].event["subEvents"].([]any)) != 1 {
		t.Fatal("wrapped subevent missing")
	}
	s.removeEvents(map[string]bool{id: true})
	check("7", "5")
	add(8)
	add(9)
	check("9", "8", "7")
	s.ConfigureJournal(true, 5)
	add(10)
	add(11)
	check("11", "10", "9", "8", "7")
	s.ConfigureJournal(true, 2)
	// Reconfiguration historically applies the lower cap on the next insertion.
	check("11", "10", "9", "8", "7")
	add(12)
	add(13)
	check("13", "12")
	s.ConfigureJournal(true, 0)
	add(14)
	add(15)
	check("15", "14", "13", "12")
	s.ConfigureJournal(false, 3)
	if add(16) != "" {
		t.Fatal("disabled journal returned event ID")
	}
	check()
	s.ConfigureJournal(true, 3)
	add(17)
	add(18)
	add(19)
	add(20)
	s.ResetRequests()
	add(21)
	check("21")
	if old[0].request.URL != "7" || old[2].request.URL != "5" {
		t.Fatal("overwrite mutated prior snapshot")
	}
}

func TestJournalServesLoggedResponseBodies(t *testing.T) {
	s, err := NewServer(Options{RootDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat(`{"name":"Zoë <b>","n":1},`, 100)
	mapping, _ := json.Marshal(map[string]any{"request": map[string]any{"urlPath": "/logged"}, "response": map[string]any{"status": 200, "headers": map[string]any{"Content-Type": "application/json"}, "body": body}})
	if _, err = s.Store.AddMappingDocument(mapping); err != nil {
		t.Fatal(err)
	}
	if !gzipAvailable() {
		t.Skip("gzip responses require a cgo build")
	}
	plain := httptest.NewRecorder()
	s.ServeHTTP(plain, httptest.NewRequest("GET", "/logged", nil))
	compressedRequest := httptest.NewRequest("GET", "/logged?gzip", nil)
	compressedRequest.Header.Set("Accept-Encoding", "gzip")
	compressed := httptest.NewRecorder()
	s.ServeHTTP(compressed, compressedRequest)
	if plain.Body.String() != body || compressed.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("unexpected served responses")
	}
	admin := httptest.NewRecorder()
	s.ServeHTTP(admin, httptest.NewRequest("GET", "/__admin/requests", nil))
	var journal struct {
		Requests []struct {
			Response struct {
				Body         string            `json:"body"`
				BodyAsBase64 string            `json:"bodyAsBase64"`
				Headers      map[string]string `json:"headers"`
			} `json:"response"`
		} `json:"requests"`
	}
	if err = json.Unmarshal(admin.Body.Bytes(), &journal); err != nil || len(journal.Requests) != 2 {
		t.Fatal(err, admin.Body.String())
	}
	// Newest first: the gzip response, then the plain response. As in WireMock,
	// whose journal records the body before Jetty compresses it, both log the
	// uncompressed body and the gzip response logs no Content-Encoding.
	for i, logged := range []struct {
		Body         string            `json:"body"`
		BodyAsBase64 string            `json:"bodyAsBase64"`
		Headers      map[string]string `json:"headers"`
	}{journal.Requests[0].Response, journal.Requests[1].Response} {
		if logged.BodyAsBase64 != base64.StdEncoding.EncodeToString([]byte(body)) || logged.Body != body {
			t.Fatalf("logged response %d is not the uncompressed body", i)
		}
		if _, ok := logged.Headers["Content-Encoding"]; ok {
			t.Fatalf("logged response %d keeps the server's Content-Encoding", i)
		}
	}
	if !strings.Contains(admin.Body.String(), `"body" : "{\"name\":\"Zoë <b>\"`) {
		t.Fatal("logged text body is not serialized with admin escaping")
	}
}

func TestJournalLogsRenderedResponseDefinition(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "__files"), 0o755)
	os.WriteFile(filepath.Join(root, "__files", "t.txt"), []byte("File {{request.query.a}}"), 0o644)
	os.WriteFile(filepath.Join(root, "__files", "s.txt"), []byte("Static file"), 0o644)
	s, err := NewServer(Options{RootDir: root})
	if err != nil {
		t.Fatal(err)
	}
	tpl := `"transformers":["response-template"]`
	// Expected definitions captured from WireMock 4.0.0-beta.40.
	for _, c := range []struct{ name, response, want string }{
		{"inline body", `{"body":"Hi {{request.query.a}}",` + tpl + `}`, `{"status":200,"body":"Hi AA","transformers":["response-template"]}`},
		{"headers", `{"body":"x","headers":{"X-A":"{{request.query.a}}","X-M":["{{request.query.a}}","two"]},` + tpl + `}`, `{"status":200,"body":"x","headers":{"X-A":"AA","X-M":["AA","two"]},"transformers":["response-template"]}`},
		{"jsonBody", `{"jsonBody":{"v":"{{request.query.a}}"},` + tpl + `}`, `{"status":200,"body":"{\"v\":\"AA\"}","transformers":["response-template"]}`},
		{"jsonBody unrendered", `{"jsonBody":{"v":1},` + tpl + `}`, `{"status":200,"jsonBody":{"v":1},"transformers":["response-template"]}`},
		{"bodyFileName", `{"bodyFileName":"t.txt",` + tpl + `}`, `{"status":200,"body":"File AA","transformers":["response-template"]}`},
		{"bodyFileName unrendered", `{"bodyFileName":"s.txt",` + tpl + `}`, `{"status":200,"bodyFileName":"s.txt","transformers":["response-template"]}`},
		{"file templating disabled", `{"bodyFileName":"t.txt",` + tpl + `,"transformerParameters":{"disableBodyFileTemplating":true}}`, `{"status":200,"bodyFileName":"t.txt","transformers":["response-template"],"transformerParameters":{"disableBodyFileTemplating":true}}`},
		{"not templated", `{"body":"Hi {{request.query.a}}"}`, `{"status":200,"body":"Hi {{request.query.a}}"}`},
		{"execution error", `{"body":"{{math 1 '/' 0}}",` + tpl + `}`, `{"status":500,"body":"[ERROR] 1:2: java.lang.ArithmeticException: / by zero","headers":{"Content-Type":"text/plain"}}`},
	} {
		mapping := `{"id":"7d5a1f5e-0000-4000-8000-000000000001","request":{"urlPath":"/p"},"response":` + c.response + `}`
		s.Store.ResetMappings()
		s.Store.ResetRequests()
		if _, err := s.Store.AddMappingDocument([]byte(mapping)); err != nil {
			t.Fatal(c.name, err)
		}
		s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/p?a=AA", nil))
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/__admin/requests", nil))
		var journal struct {
			Requests []struct {
				ResponseDefinition json.RawMessage `json:"responseDefinition"`
			} `json:"requests"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &journal); err != nil || len(journal.Requests) != 1 {
			t.Fatal(c.name, err, w.Body.String())
		}
		var got, want any
		json.Unmarshal(journal.Requests[0].ResponseDefinition, &got)
		json.Unmarshal([]byte(c.want), &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: responseDefinition %s, want %s", c.name, journal.Requests[0].ResponseDefinition, c.want)
		}
	}
}
