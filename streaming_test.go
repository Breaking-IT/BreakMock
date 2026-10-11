// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func newStreamingTestServer(t *testing.T, kind string) (*Server, *httptest.Server) {
	return newStreamingTestServerOptions(t, kind, Options{RootDir: t.TempDir()})
}
func newStreamingTestServerOptions(t *testing.T, kind string, options Options) (*Server, *httptest.Server) {
	t.Helper()
	s, e := NewServer(options)
	if e != nil {
		t.Fatal(e)
	}
	flag := "openWebsocketChannel"
	if kind == "sse" {
		flag = "openSseChannel"
	}
	_, e = s.Store.AddMapping(Mapping{"request": map[string]any{"url": "/stream"}, "response": map[string]any{flag: true}})
	if e != nil {
		t.Fatal(e)
	}
	ts := httptest.NewUnstartedServer(s)
	ts.Listener = WrapWireListener(ts.Listener, true)
	ts.Config.ConnContext = HeaderOrderConnContext
	ts.Start()
	t.Cleanup(func() {
		for _, ch := range s.Store.Messaging().Channels() {
			ch.Close()
		}
		ts.Close()
	})
	return s, ts
}
func awaitStreamingChannel(t *testing.T, s *Server) *MessageChannel {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		channels := s.Store.Messaging().Channels()
		if len(channels) > 0 {
			return channels[0]
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("channel was not registered")
	return nil
}
func TestStreamingWebSocketConcurrentSendAndClose(t *testing.T) {
	s, ts := newStreamingTestServer(t, "websocket")
	conn, _, e := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/stream", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	channel := awaitStreamingChannel(t, s)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if err := channel.Send(map[string]any{"body": "hello"}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for i := 0; i < 40; i++ {
		kind, b, e := conn.ReadMessage()
		if e != nil || kind != websocket.TextMessage || string(b) != "hello" {
			t.Fatalf("read %d %q %v", kind, b, e)
		}
	}
	wg.Wait()
	if e = conn.WriteMessage(websocket.BinaryMessage, []byte{0, 1, 255}); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for len(s.Store.Messaging().Events()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	events := s.Store.Messaging().Events()
	if len(events) == 0 {
		t.Fatal("incoming message not recorded")
	}
	channel.Close()
	_, _, e = conn.ReadMessage()
	if !websocket.IsCloseError(e, 1000) {
		t.Fatalf("close %v", e)
	}
}
func TestStreamingSSEFramingAndLifecycle(t *testing.T) {
	s, ts := newStreamingTestServer(t, "sse")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/stream", nil)
	response, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.Header.Get("Content-Type") != "text/event-stream;charset=UTF-8" {
		t.Fatal(response.Header)
	}
	reader := bufio.NewReader(response.Body)
	initial := make([]byte, 8)
	if _, e = io.ReadFull(reader, initial); e != nil || string(initial) != ": ok\r\n\r\n" {
		t.Fatalf("initial %q %v", initial, e)
	}
	channel := awaitStreamingChannel(t, s)
	message := map[string]any{"body": "a\nb\n", "headers": map[string]any{"event": "update", "id": "42", "comment": "note", "retry": "500"}}
	if e = channel.Send(message); e != nil {
		t.Fatal(e)
	}
	want := sseFrame(message)
	got := make([]byte, len(want))
	if _, e = io.ReadFull(reader, got); e != nil || string(got) != want {
		t.Fatalf("frame %q %v", got, e)
	}
	channel.Close()
	if _, e = reader.ReadByte(); e != io.EOF {
		t.Fatalf("SSE close: %v", e)
	}
	deadline := time.Now().Add(time.Second)
	for len(s.Store.Messaging().Channels()) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(s.Store.Messaging().Channels()) != 0 {
		t.Fatal("closed channel leaked")
	}
	if channel.Send(map[string]any{"body": "late"}) == nil {
		t.Fatal("closed channel accepted message")
	}
}
func TestSSEExactFrameBytes(t *testing.T) {
	got := sseFrame(map[string]any{"body": "one\rtwo\r\n\n", "headers": map[string]any{"EVENT": []any{"first", "second"}, "comment": "bad\ninjection", "id": "", "retry": "0"}})
	want := "id: \r\nevent: first\r\nretry: 0\r\ndata: one\r\ndata: two\r\ndata:\r\ndata:\r\n\r\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestStreamingCompressedMessageLimit(t *testing.T) {
	_, ts := newStreamingTestServerOptions(t, "websocket", Options{RootDir: t.TempDir(), WebSocketOptionsConfigured: true, WebSocketMaxTextMessageSize: 8, WebSocketMaxBinaryMessageSize: 12})
	dialer := websocket.Dialer{EnableCompression: true}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.EnableWriteCompression(true)
	if err = conn.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", 10000))); err != nil {
		t.Fatal(err)
	}
	_, _, err = conn.ReadMessage()
	closeErr, ok := err.(*websocket.CloseError)
	if !ok || closeErr.Code != 1009 || closeErr.Text != streamingMessageLimitReason("Text", 8192, 8) {
		t.Fatalf("compressed size limit: %v", err)
	}
}
func TestStreamingExplicitZeroLimits(t *testing.T) {
	_, ts := newStreamingTestServerOptions(t, "websocket", Options{RootDir: t.TempDir(), WebSocketOptionsConfigured: true})
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = conn.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", 10000))); err != nil {
		t.Fatal(err)
	}
	conn.SetPongHandler(func(s string) error { return io.EOF })
	if err = conn.WriteControl(websocket.PingMessage, []byte("alive"), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, _, err = conn.ReadMessage()
	if err != io.EOF {
		t.Fatalf("zero limit rejected text: %v", err)
	}
}
