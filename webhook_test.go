package breakmock

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebhookTemplatesAndJournal(t *testing.T) {
	got := make(chan Request, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- Request{Method: r.Method, URL: r.URL.String(), Headers: r.Header, Body: body}
		w.WriteHeader(202)
		w.Write([]byte("accepted"))
	}))
	defer target.Close()
	store := NewStore()
	req := Request{Method: "GET", URL: "/original", AbsoluteURL: "http://localhost/original", Headers: http.Header{"X-Id": []string{"hello"}}}
	mapping := Mapping{"request": map[string]any{}, "response": map[string]any{}, "serveEventListeners": []any{map[string]any{"name": "webhook", "parameters": map[string]any{"method": "POST", "url": target.URL + "/{{parameters.suffix}}", "suffix": "callback", "headers": map[string]any{"X-Original": "{{originalRequest.path}}"}, "body": "{{originalRequest.headers.X-Id}}", "delay": map[string]any{"type": "fixed", "milliseconds": 30.0}}}}}
	id := store.RecordServed(req, mapping, 200, nil, nil)
	start := time.Now()
	DispatchWebhooks(store, id, req, mapping, t.TempDir())
	select {
	case r := <-got:
		if r.Method != "POST" || r.URL != "/callback" || string(r.Body) != "hello" || r.Headers.Get("X-Original") != "/original" {
			t.Fatalf("unexpected callback %#v", r)
		}
		if time.Since(start) < 25*time.Millisecond {
			t.Fatal("delay ignored")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callback not received")
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		events := store.journalSnapshot()
		if len(events[0].event["subEvents"].([]any)) == 2 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("response subevent missing")
}
func TestWebhookLegacyGETOmitsBody(t *testing.T) {
	got := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { b, _ := io.ReadAll(r.Body); got <- string(b) }))
	defer target.Close()
	m := Mapping{"postServeActions": map[string]any{"webhook": map[string]any{"url": target.URL, "method": "GET", "body": "ignored"}}}
	DispatchWebhooks(NewStore(), "", Request{}, m, "")
	select {
	case body := <-got:
		if body != "" {
			t.Fatalf("GET body %q", body)
		}
	case <-time.After(time.Second):
		t.Fatal("no callback")
	}
}

func TestWebhookMissingMethodMatchesV4Error(t *testing.T) {
	store := NewStore()
	req := Request{Method: "GET", URL: "/trigger"}
	mapping := Mapping{"request": map[string]any{}, "response": map[string]any{}, "serveEventListeners": []any{map[string]any{"name": "webhook", "parameters": map[string]any{"url": "http://127.0.0.1:1/callback"}}}}
	id := store.RecordServed(req, mapping, 200, nil, nil)
	DispatchWebhooks(store, id, req, mapping, "")
	events := store.journalSnapshot()[0].event["subEvents"].([]any)
	if len(events) != 1 {
		t.Fatal(events)
	}
	event := events[0].(map[string]any)
	if event["type"] != "ERROR" || event["timeOffsetNanos"] != nil {
		t.Fatal(event)
	}
	want := `Exception thrown while configuring webhook: Cannot invoke "String.indexOf(String)" because "content" is null`
	if event["data"].(map[string]any)["message"] != want {
		t.Fatal(event)
	}
}
