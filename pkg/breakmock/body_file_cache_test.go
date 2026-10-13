// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestBodyFileChangesAreServed(t *testing.T) {
	root := t.TempDir()
	files := filepath.Join(root, "__files")
	os.MkdirAll(files, 0o755)
	path := filepath.Join(files, "body.json")
	os.WriteFile(path, []byte(`{"v":1}`), 0o644)
	os.WriteFile(filepath.Join(root, "outside.json"), []byte(`{"secret":1}`), 0o644)
	s, err := NewServer(Options{RootDir: root, DisableJournal: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, mapping := range []string{
		`{"request":{"url":"/body"},"response":{"status":200,"bodyFileName":"body.json"}}`,
		`{"request":{"url":"/link"},"response":{"status":200,"bodyFileName":"link.json"}}`,
	} {
		if _, err = s.Store.AddMappingDocument([]byte(mapping)); err != nil {
			t.Fatal(err)
		}
	}
	get := func(url string) (int, string) {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		return w.Code, w.Body.String()
	}
	expect := func(want string) {
		t.Helper()
		for i := 0; i < 2; i++ {
			if code, body := get("/body"); code != 200 || body != want {
				t.Fatalf("got %d %q, want %q", code, body, want)
			}
		}
	}
	expect(`{"v":1}`)

	// Same size, rewritten in place, with the modification time restored.
	info, _ := os.Stat(path)
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte(`{"v":2}`), 0o644)
	os.Chtimes(path, info.ModTime(), info.ModTime())
	expect(`{"v":2}`)

	// Atomically replaced by rename.
	os.WriteFile(path+".tmp", []byte(`{"v":3}`), 0o644)
	os.Rename(path+".tmp", path)
	expect(`{"v":3}`)

	// Removed: served as WireMock's missing file error.
	os.Remove(path)
	if code, _ := get("/body"); code != 500 {
		t.Fatalf("removed file: status %d", code)
	}
	os.WriteFile(path, []byte(`{"v":4}`), 0o644)
	expect(`{"v":4}`)

	// A link that later escapes __files is rejected, not served from the cache.
	if runtime.GOOS == "windows" {
		return
	}
	link := filepath.Join(files, "link.json")
	if err = os.Symlink("body.json", link); err != nil {
		t.Skip(err)
	}
	if code, body := get("/link"); code != 200 || body != `{"v":4}` {
		t.Fatalf("link inside root: %d %q", code, body)
	}
	os.Remove(link)
	os.Symlink(filepath.Join(root, "outside.json"), link)
	if code, body := get("/link"); code != 500 {
		t.Fatalf("escaping link served %d %q", code, body)
	}
}
