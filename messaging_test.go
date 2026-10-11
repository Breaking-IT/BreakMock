package breakmock

import (
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
