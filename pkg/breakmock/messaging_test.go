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
	"time"
)

func TestMessagingConcurrentInitialization(t *testing.T) {
	store := NewStore()
	const workers = 64
	start := make(chan struct{})
	results := make(chan *MessageStore, workers)
	for range workers {
		go func() {
			<-start
			messages := store.Messaging()
			if messages.owner != store || messages.mappings == nil || messages.channels == nil || messages.providers == nil || messages.providerOrders == nil || messages.changed == nil {
				t.Error("partially initialized messaging store")
			}
			results <- messages
		}()
	}
	close(start)
	first := <-results
	for range workers - 1 {
		if messages := <-results; messages != first {
			t.Error("concurrent callers received different messaging stores")
		}
	}
}

func BenchmarkMessagingAccessor(b *testing.B) {
	store := NewStore()
	want := store.Messaging()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if store.Messaging() != want {
				b.Fatal("messaging store changed")
			}
		}
	})
}

func BenchmarkMessagingEmptyHTTPTrigger(b *testing.B) {
	store := NewStore()
	store.Messaging()
	request := Request{Method: "GET", URL: "/bench"}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := store.Messaging().TriggerHTTP(request); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestMessageMappingFilesSingleAndBulk(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "message-mappings")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"single.json": `{"id":"11111111-1111-4111-8111-111111111111","name":"single"}`, "bulk.json": `{"messageMappings":[{"name":"first"},{"name":"second","actions":[{"type":"send","message":{"body":{"data":{"z":1,"a":2}}}}]}]}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	server, err := NewServer(Options{RootDir: root})
	if err != nil {
		t.Fatal(err)
	}

	mappings := server.Store.Messaging().Mappings()
	if len(mappings) != 3 {
		t.Fatal(mappings)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest("GET", "/__admin/message-mappings", nil))
	if !strings.Contains(w.Body.String(), `"z" : 1,`) {
		t.Fatal(w.Body.String())
	}
}
func TestMessageJournalConcurrentWaitAndLimit(t *testing.T) {
	store := NewStore()
	store.ConfigureJournal(true, 5)
	ms := store.Messaging()
	ch := ms.RegisterChannel("channel", "websocket", Request{Method: "GET", URL: "/ws", Headers: http.Header{}}, nil, nil)
	h := NewAdminHandler(store, t.TempDir())
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/__admin/messages/wait", strings.NewReader(`{"pattern":{"body":{"equalTo":"ready"}},"timeoutMillis":2000}`)))
		done <- w
	}()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); ms.Record(ch.ID, "received", map[string]any{"body": "other"}, nil) }()
	}
	wg.Wait()
	ms.Record(ch.ID, "received", map[string]any{"body": "ready"}, nil)
	select {
	case w := <-done:
		if w.Code != 200 || !strings.Contains(w.Body.String(), "ready") {
			t.Fatal(w.Code, w.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait missed journal event")
	}
	if len(ms.Events()) != 5 {
		t.Fatal("journal limit ignored")
	}
	before := ms.Events()
	ms.Record(ch.ID, "received", map[string]any{"body": "after"}, nil)
	if before[0]["message"].(map[string]any)["body"] != "ready" {
		t.Fatal("snapshot mutated")
	}
}

func TestMessageChannelConcurrentUniquenessAndRemoval(t *testing.T) {
	store := NewStore()
	handler := NewAdminHandler(store, t.TempDir())
	provider := httptest.NewRecorder()
	handler.ServeHTTP(provider, httptest.NewRequest("POST", "/__admin/channel-providers", strings.NewReader(`{"name":"p","driverType":"in-memory"}`)))
	if provider.Code != 201 {
		t.Fatal(provider.Code, provider.Body.String())
	}
	var wg sync.WaitGroup
	successes := make(chan bool, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("POST", "/__admin/channels", strings.NewReader(`{"providerName":"p","channelName":"c"}`)))
			successes <- w.Code == 201
			if w.Code != 201 && w.Code != 409 {
				t.Errorf("unexpected status %d", w.Code)
			}
		}()
	}
	wg.Wait()
	close(successes)
	count := 0
	for ok := range successes {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("created %d channels", count)
	}
	messages := store.Messaging()
	for _, channel := range messages.Channels() {
		messages.UnregisterChannel(channel.ID)
	}
	for i := 0; i < 128; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := newID()
			messages.RegisterChannel(id, "websocket", Request{Method: "GET", URL: "/", Headers: http.Header{}}, nil, nil)
			messages.UnregisterChannel(id)
		}()
	}
	wg.Wait()
	messages.mu.RLock()
	defer messages.mu.RUnlock()
	if len(messages.channels) != 0 || len(messages.channelOrder) != 0 {
		t.Fatalf("retained channels=%d order=%d", len(messages.channels), len(messages.channelOrder))
	}
}

func TestDeletedProviderMetadataDoesNotAccumulate(t *testing.T) {
	messages := NewStore().Messaging()
	messages.mu.Lock()
	defer messages.mu.Unlock()
	for i := 0; i < 100; i++ {
		name := fmt.Sprint(i)
		messages.providers[name] = Mapping{"name": name}
		messages.providerOrder = append(messages.providerOrder, name)
		messages.providerOrders[name] = &adminOrder{}
		messages.deleteProviderLocked(name)
	}
	if len(messages.providerOrder) != 0 || len(messages.providerOrders) != 0 || len(messages.providers) != 0 {
		t.Fatal("deleted provider metadata retained")
	}
}

// The Java wait endpoints cap existing results, and only wait-for-count uses count.
func TestMessageWaitExistingEvents(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint, body string
		want                 int
	}{
		{"count cap", "wait-for-count", `{"count":1,"timeoutMillis":1000}`, 1},
		{"zero timeout cap", "wait-for-count", `{"count":1,"timeoutMillis":0}`, 1},
		{"zero count", "wait-for-count", `{"count":0,"timeoutMillis":1000}`, 0},
		{"single ignores count", "wait", `{"count":3,"timeoutMillis":1000}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewStore()
			ms := store.Messaging()
			ch := ms.RegisterChannel("channel", "websocket", Request{Method: "GET", URL: "/ws", Headers: http.Header{}}, nil, nil)
			ms.Record(ch.ID, "received", map[string]any{"body": "first"}, nil)
			ms.Record(ch.ID, "received", map[string]any{"body": "second"}, nil)
			w := httptest.NewRecorder()
			started := time.Now()
			NewAdminHandler(store, t.TempDir()).ServeHTTP(w, httptest.NewRequest("POST", "/__admin/messages/"+tc.endpoint, strings.NewReader(tc.body)))
			if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
				t.Fatalf("existing events unnecessarily waited %s", elapsed)
			}
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var response map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if tc.endpoint == "wait" {
				if response["message"].(map[string]any)["body"] != "second" {
					t.Fatal(response)
				}
			} else {
				events := response["messageServeEvents"].([]any)
				if len(events) != tc.want {
					t.Fatalf("got %d events, want %d", len(events), tc.want)
				}
				if len(events) > 0 && events[0].(map[string]any)["message"].(map[string]any)["body"] != "second" {
					t.Fatal(events)
				}
			}
		})
	}
}

func TestMessageWaitDisabledJournalAndDefaultTimeout(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		for _, endpoint := range []string{"wait", "wait-for-count"} {
			t.Run(fmt.Sprintf("%s/disabled=%t", endpoint, disabled), func(t *testing.T) {
				store := NewStore()
				store.ConfigureJournal(!disabled, 0)
				w := httptest.NewRecorder()
				started := time.Now()
				NewAdminHandler(store, t.TempDir()).ServeHTTP(w, httptest.NewRequest("POST", "/__admin/messages/"+endpoint, strings.NewReader(`{}`)))
				if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
					t.Fatalf("missing timeout unnecessarily waited %s", elapsed)
				}
				if disabled {
					if w.Code != 500 || !strings.Contains(w.Body.String(), "MessageJournalDisabledException") {
						t.Fatal(w.Code, w.Body.String())
					}
				} else if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestMessageWaitNegativeCount(t *testing.T) {
	for _, tc := range []struct {
		timeout int
		detail  string
	}{
		{0, "java.lang.IllegalArgumentException: -1"},
		{-1, "java.lang.IllegalArgumentException: -1"},
		{1000, "java.lang.IllegalArgumentException: fromIndex(0) &gt; toIndex(-1)"},
	} {
		t.Run(fmt.Sprint(tc.timeout), func(t *testing.T) {
			w := httptest.NewRecorder()
			NewAdminHandler(NewStore(), t.TempDir()).ServeHTTP(w, httptest.NewRequest("POST", "/__admin/messages/wait-for-count", strings.NewReader(fmt.Sprintf(`{"count":-1,"timeoutMillis":%d}`, tc.timeout))))
			if w.Code != 500 || !strings.Contains(w.Body.String(), tc.detail) {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

// A client can read the journal as soon as it receives a reply frame. The
// read must still include the reply's "sent" event and the "received" event.
func TestJournalReadDuringReplyIncludesBothEvents(t *testing.T) {
	store := NewStore()
	ms := store.Messaging()
	h := NewAdminHandler(store, t.TempDir())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/__admin/message-mappings", strings.NewReader(`{"trigger":{"type":"message","message":{"body":{"equalTo":"hello"}}},"actions":[{"type":"send","message":{"body":"reply"}}]}`)))
	if w.Code != 201 && w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	read := make(chan []Mapping, 1)
	readDuringSend := false
	send := func(map[string]any) error {
		// The frame is on the wire: the client now reads the journal.
		go func() { read <- ms.Events() }()
		select {
		case events := <-read:
			readDuringSend = true
			read <- events
		case <-time.After(50 * time.Millisecond):
		}
		return nil
	}
	ch := ms.RegisterChannel("channel", "websocket", Request{Method: "GET", URL: "/ws", Headers: http.Header{}}, send, nil)
	if err := ms.Incoming(ch.ID, map[string]any{"body": "hello"}); err != nil {
		t.Fatal(err)
	}
	events := <-read
	types := []string{}
	for _, e := range events {
		types = append(types, bmString(e["eventType"]))
	}
	if readDuringSend || len(events) != 2 || types[0] != "received" || types[1] != "sent" {
		t.Fatalf("journal read completed during the reply (%v) with events %v", readDuringSend, types)
	}
}
