// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestFixedRateAndBodyValidation(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "bmbench")
	if out, err := exec.Command("go", "build", "-o", binary, "main.go").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	var received atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1); _, _ = w.Write([]byte("ok")) }))
	defer server.Close()
	for _, expected := range []string{"ok", "wrong"} {
		received.Store(0)
		out, err := exec.Command(binary, "--url", server.URL, "--rate", "100", "--duration", "200ms", "--concurrency", "2", "--expected-body", expected).Output()
		if (err == nil) != (expected == "ok") {
			t.Fatalf("body %q: unexpected exit %v: %s", expected, err, out)
		}
		var report struct{ Requests, Errors, Dropped int64 }
		if err := json.Unmarshal(out, &report); err != nil {
			t.Fatal(err)
		}
		if report.Requests != 20 || received.Load() != 20 || report.Dropped != 0 {
			t.Fatalf("unexpected delivery: %+v received %d", report, received.Load())
		}
		wantErrors := int64(0)
		if expected != "ok" {
			wantErrors = 20
		}
		if report.Errors != wantErrors {
			t.Fatalf("errors = %d, want %d", report.Errors, wantErrors)
		}
	}
}

func TestRequestBodyHeadersAndExpectedFile(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "bmbench")
	if out, err := exec.Command("go", "build", "-o", binary, "main.go").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write([]byte(r.Method + " " + r.Header.Get("X-Test") + " " + string(body)))
	}))
	defer server.Close()
	request, expected := filepath.Join(dir, "request.json"), filepath.Join(dir, "expected")
	os.WriteFile(request, []byte(`{"a":1}`), 0o644)
	os.WriteFile(expected, []byte(`POST yes {"a":1}`), 0o644)
	out, err := exec.Command(binary, "--url", server.URL, "--rate", "50", "--duration", "200ms", "--concurrency", "2", "--method", "POST", "--header", "X-Test: yes", "--body-file", request, "--expected-body-file", expected).Output()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var report struct {
		Requests, Errors int64
		ResponseBytes    int64 `json:"response_bytes"`
	}
	if err := json.Unmarshal(out, &report); err != nil || report.Requests != 10 || report.Errors != 0 || report.ResponseBytes != 10*16 {
		t.Fatalf("unexpected report %s", out)
	}
}
