// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
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
				Body         string `json:"body"`
				BodyAsBase64 string `json:"bodyAsBase64"`
			} `json:"response"`
		} `json:"requests"`
	}
	if err = json.Unmarshal(admin.Body.Bytes(), &journal); err != nil || len(journal.Requests) != 2 {
		t.Fatal(err, admin.Body.String())
	}
	// Newest first: the gzip response, then the plain response.
	for i, served := range [][]byte{compressed.Body.Bytes(), plain.Body.Bytes()} {
		logged := journal.Requests[i].Response
		if logged.BodyAsBase64 != base64.StdEncoding.EncodeToString(served) || (i == 1 && logged.Body != body) {
			t.Fatalf("logged response %d differs from served bytes", i)
		}
	}
	if !strings.Contains(admin.Body.String(), `"body" : "{\"name\":\"Zoë <b>\"`) {
		t.Fatal("logged text body is not serialized with admin escaping")
	}
}
