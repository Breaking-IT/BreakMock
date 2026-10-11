// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

type capturedHeaderTestRequest struct {
	order []string
	body  string
}

func newHeaderCaptureTestServer(t *testing.T) (*httptest.Server, chan capturedHeaderTestRequest) {
	t.Helper()
	got := make(chan capturedHeaderTestRequest, 8)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order := RequestHeaderOrder(r)
		body, _ := io.ReadAll(r.Body)
		got <- capturedHeaderTestRequest{order, string(body)}
		w.Header().Set("Content-Length", "2")
		_, _ = w.Write([]byte("OK"))
	}))
	server.Listener = WrapHeaderOrderListener(server.Listener)
	server.Config.ConnContext = HeaderOrderConnContext
	server.Start()
	t.Cleanup(server.Close)
	return server, got
}
func TestHeaderOrderCapturePipelining(t *testing.T) {
	s, got := newHeaderCaptureTestServer(t)
	conn, e := net.Dial("tcp", s.Listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	body := strings.Repeat("body", 5000)
	raw := fmt.Sprintf("POST /first HTTP/1.1\r\nx-last: a\r\nHost: example.test\r\nX-First: b\r\nx-LAST: c\r\nContent-Length: %d\r\n\r\n%sGET /second HTTP/1.1\r\nZebra: 1\r\nHost: example.test\r\nAlpha: 2\r\nConnection: close\r\n\r\n", len(body), body)
	if _, e = io.WriteString(conn, raw); e != nil {
		t.Fatal(e)
	}
	reader := bufio.NewReader(conn)
	for i := 0; i < 2; i++ {
		response, e := http.ReadResponse(reader, nil)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}
	first, second := <-got, <-got
	if !reflect.DeepEqual(first.order, []string{"x-last", "Host", "X-First", "Content-Length"}) || first.body != body {
		t.Fatalf("first request: %+v", first.order)
	}
	if !reflect.DeepEqual(second.order, []string{"Zebra", "Host", "Alpha", "Connection"}) {
		t.Fatalf("second request: %+v", second.order)
	}
}
func TestHeaderOrderCaptureChunkedAndTrailers(t *testing.T) {
	s, got := newHeaderCaptureTestServer(t)
	conn, e := net.Dial("tcp", s.Listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, e = io.WriteString(conn, "POST /one HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\nTrailer: X-Trailer\r\n\r\n3\r\nabc\r\n2\r\nde\r\n0\r\nX-Trailer: done\r\n\r\nGET /two HTTP/1.1\r\nAfter: trailer\r\nHost: x\r\nConnection: close\r\n\r\n")
	if e != nil {
		t.Fatal(e)
	}
	reader := bufio.NewReader(conn)
	for i := 0; i < 2; i++ {
		r, e := http.ReadResponse(reader, nil)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
	}
	first, second := <-got, <-got
	if first.body != "abcde" || !reflect.DeepEqual(first.order, []string{"Host", "Transfer-Encoding", "Trailer"}) {
		t.Fatalf("first=%+v", first)
	}
	if !reflect.DeepEqual(second.order, []string{"After", "Host", "Connection"}) {
		t.Fatalf("second=%+v", second)
	}
}
func TestHeaderOrderCaptureExpectContinue(t *testing.T) {
	s, got := newHeaderCaptureTestServer(t)
	conn, e := net.Dial("tcp", s.Listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(conn, "POST / HTTP/1.1\r\nHost: x\r\nExpect: 100-continue\r\nContent-Length: 4\r\nConnection: close\r\n\r\n")
	reader := bufio.NewReader(conn)
	r, e := http.ReadResponse(reader, nil)
	if e != nil || r.StatusCode != 100 {
		t.Fatalf("continue=%v %v", r, e)
	}
	_, _ = io.WriteString(conn, "body")
	r, e = http.ReadResponse(reader, nil)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = io.Copy(io.Discard, r.Body)
	_ = r.Body.Close()
	v := <-got
	if v.body != "body" || !reflect.DeepEqual(v.order, []string{"Host", "Expect", "Content-Length", "Connection"}) {
		t.Fatalf("got=%+v", v)
	}
}
func TestHeaderOrderWithoutListener(t *testing.T) {
	if got := RequestHeaderOrder(httptest.NewRequest("GET", "/", nil)); got != nil {
		t.Fatal(got)
	}
}

func TestHeaderOrderCaptureSequentialKeepAlive(t *testing.T) {
	s, got := newHeaderCaptureTestServer(t)
	conn, e := net.Dial("tcp", s.Listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	for i := 0; i < 5; i++ {
		_, e = fmt.Fprintf(conn, "POST /%d HTTP/1.1\r\nX-Sequence: %d\r\nHost: example.test\r\nContent-Length: 4\r\n\r\nbody", i, i)
		if e != nil {
			t.Fatal(e)
		}
		response, e := http.ReadResponse(reader, nil)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		v := <-got
		if !reflect.DeepEqual(v.order, []string{"X-Sequence", "Host", "Content-Length"}) {
			t.Fatalf("request%d: %+v", i, v)
		}
	}
}
