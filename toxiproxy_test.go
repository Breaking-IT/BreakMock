// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func newToxiTestServer(t *testing.T, options ToxiproxyOptions) *Server {
	t.Helper()
	s, err := NewServer(Options{RootDir: t.TempDir(), Toxiproxy: options})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func toxiRequest(s *Server, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func TestEmbeddedToxiproxyRoutesAndResetIsolation(t *testing.T) {
	s := newToxiTestServer(t, ToxiproxyOptions{Enabled: true})
	if w := toxiRequest(s, "POST", "/__admin/mappings", `{"request":{"method":"GET","url":"/hello"},"response":{"body":"wiremock"}}`); w.Code != 201 {
		t.Fatalf("mapping: %d %s", w.Code, w.Body)
	}
	proxy := `{"name":"test","listen":"127.0.0.1:0","upstream":"127.0.0.1:1","enabled":false}`
	if w := toxiRequest(s, "POST", "/__admin/toxiproxy/proxies", proxy); w.Code != 201 {
		t.Fatalf("proxy: %d %s", w.Code, w.Body)
	}
	if w := toxiRequest(s, "POST", "/__admin/toxiproxy/proxies/test/toxics", `{"name":"lag","type":"latency","attributes":{"latency":1}}`); w.Code != 200 {
		t.Fatalf("toxic: %d %s", w.Code, w.Body)
	}
	if w := toxiRequest(s, "POST", "/__admin/reset", ""); w.Code != 200 {
		t.Fatalf("wiremock reset: %d", w.Code)
	}
	if w := toxiRequest(s, "GET", "/__admin/toxiproxy/proxies/test/toxics", ""); !strings.Contains(w.Body.String(), `"lag"`) {
		t.Fatalf("wiremock reset removed toxic: %s", w.Body)
	}
	if w := toxiRequest(s, "POST", "/__admin/mappings", `{"request":{"method":"GET","url":"/hello"},"response":{"body":"wiremock"}}`); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	if w := toxiRequest(s, "POST", "/__admin/toxiproxy/reset", ""); w.Code != 204 {
		t.Fatalf("toxi reset: %d %s", w.Code, w.Body)
	}
	if w := toxiRequest(s, "GET", "/hello", ""); w.Body.String() != "wiremock" {
		t.Fatalf("toxi reset removed mapping: %s", w.Body)
	}
	if w := toxiRequest(s, "GET", "/__admin/toxiproxy/proxies/test/toxics", ""); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("reset toxics: %s", w.Body)
	}
}
func TestEmbeddedToxiproxyDisabledPreservesAdminBytes(t *testing.T) {
	a := newToxiTestServer(t, ToxiproxyOptions{})
	b := newToxiTestServer(t, ToxiproxyOptions{Enabled: false, Proxies: []ToxiproxyProxyConfig{{Name: "ignored"}}})
	for _, path := range []string{"/__admin/toxiproxy/proxies", "/__admin/toxiproxy", "/__admin/toxiproxy-extra"} {
		x, y := toxiRequest(a, "GET", path, ""), toxiRequest(b, "GET", path, "")
		if x.Code != y.Code || x.Body.String() != y.Body.String() {
			t.Fatalf("disabled route changed: %s", path)
		}
	}
}
func TestEmbeddedToxiproxyForwardAndClose(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("exact upstream bytes\x00\xff")) }))
	defer upstream.Close()
	s := newToxiTestServer(t, ToxiproxyOptions{Enabled: true, Proxies: []ToxiproxyProxyConfig{{Name: "relay", Listen: "127.0.0.1:0", Upstream: strings.TrimPrefix(upstream.URL, "http://"), Toxics: []json.RawMessage{json.RawMessage(`{"type":"latency","attributes":{"latency":1}}`)}}}})
	p, err := s.toxiproxy.api.Collection.Get("relay")
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + p.Listen)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(b) != "exact upstream bytes\x00\xff" {
		t.Fatalf("forward %q %v", b, err)
	}
	conn, err := net.DialTimeout("tcp", p.Listen, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Ensure an active link exists before shutdown by completing a request on it.
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: test\r\n\r\n")
	conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4096)
	if _, err = conn.Read(buf); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	for {
		_, err = conn.Read(buf)
		if err != nil {
			break
		}
	}
	if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("active proxy connection survived Close")
	}
	if c, err := net.DialTimeout("tcp", p.Listen, time.Second); err == nil {
		c.Close()
		t.Fatal("listener survived Close")
	}
	if w := toxiRequest(s, "POST", "/__admin/toxiproxy/proxies", `{"name":"late","listen":"127.0.0.1:0","upstream":"127.0.0.1:1"}`); w.Code != 503 {
		t.Fatalf("creation after Close: %d", w.Code)
	}
}
func TestEmbeddedToxiproxyStartupRollback(t *testing.T) {
	for _, kind := range []string{"missing", "toxic", "duplicate", "bind"} {
		t.Run(kind, func(t *testing.T) {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := l.Addr().String()
			l.Close()
			configs := []ToxiproxyProxyConfig{{Name: "first", Listen: address, Upstream: "127.0.0.1:1"}, {Name: "second", Listen: "127.0.0.1:0", Upstream: "127.0.0.1:1"}}
			switch kind {
			case "missing":
				configs[1].Name = ""
			case "toxic":
				configs[1].Toxics = []json.RawMessage{json.RawMessage(`{"type":"nonexistent"}`)}
			case "duplicate":
				configs[1].Name = "first"
			case "bind":
				configs[1].Listen = address
			}
			s, err := NewServer(Options{RootDir: t.TempDir(), Toxiproxy: ToxiproxyOptions{Enabled: true, Proxies: configs}})
			if err == nil {
				s.Close()
				t.Fatal("invalid startup accepted")
			}
			l, err = net.Listen("tcp", address)
			if err != nil {
				t.Fatalf("startup leaked listener: %v", err)
			}
			l.Close()
		})
	}
}

func TestEmbeddedToxiproxyInstanceIsolationAndDisabledProxy(t *testing.T) {
	disabled := false
	config := ToxiproxyOptions{Enabled: true, Proxies: []ToxiproxyProxyConfig{{Name: "same", Listen: "127.0.0.1:0", Upstream: "127.0.0.1:1", Enabled: &disabled}}}
	a, b := newToxiTestServer(t, config), newToxiTestServer(t, config)
	for _, s := range []*Server{a, b} {
		w := toxiRequest(s, "GET", "/__admin/toxiproxy/proxies/same", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
			t.Fatalf("disabled config: %d %s", w.Code, w.Body)
		}
	}
	if w := toxiRequest(a, "DELETE", "/__admin/toxiproxy/proxies/same", ""); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := toxiRequest(b, "GET", "/__admin/toxiproxy/proxies/same", ""); w.Code != 200 {
		t.Fatalf("instance state leaked: %d", w.Code)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if w := toxiRequest(b, "GET", "/__admin/toxiproxy/version", ""); w.Code != 200 {
		t.Fatalf("instance close leaked: %d", w.Code)
	}
}

func TestEmbeddedToxiproxyConcurrentManagement(t *testing.T) {
	s := newToxiTestServer(t, ToxiproxyOptions{Enabled: true})
	if w := toxiRequest(s, "POST", "/__admin/toxiproxy/proxies", `{"name":"race","listen":"127.0.0.1:0","upstream":"127.0.0.1:1","enabled":false}`); w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var wg sync.WaitGroup
	for worker := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 40 {
				var w *httptest.ResponseRecorder
				if worker%2 == 0 {
					w = toxiRequest(s, "PATCH", "/__admin/toxiproxy/proxies/race", fmt.Sprintf(`{"upstream":"127.0.0.1:%d","enabled":false}`, i+1))
				} else {
					w = toxiRequest(s, "GET", "/__admin/toxiproxy/proxies", "")
				}
				if w.Code != 200 {
					t.Errorf("concurrent management: %d %s", w.Code, w.Body)
				}
			}
		}()
	}
	wg.Wait()
}
