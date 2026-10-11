// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
)

type proxyOrderKey struct{}
type proxyOrderState struct {
	mu    sync.Mutex
	data  []byte
	names []string
	done  bool
}

func (s *proxyOrderState) observe(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	// Only response headers are retained. Body bytes in the same read are discarded.
	for len(p) > 0 && !s.done {
		s.data = append(s.data, p[0])
		p = p[1:]
		if len(s.data) > 1<<20 {
			s.done = true
			s.data = nil
			return
		}
		if !bytes.HasSuffix(s.data, []byte("\r\n\r\n")) {
			continue
		}
		lines := strings.Split(string(s.data), "\r\n")
		status := strings.Fields(lines[0])
		if len(status) > 1 && strings.HasPrefix(status[1], "1") && status[1] != "101" {
			s.data = nil
			continue
		}
		for _, line := range lines[1:] {
			if name, _, ok := strings.Cut(line, ":"); ok {
				s.names = append(s.names, name)
			}
		}
		s.done = true
		s.data = nil
	}
}

type proxyOrderConn struct {
	net.Conn
	mu    sync.Mutex
	state *proxyOrderState
}

func (c *proxyOrderConn) Read(p []byte) (int, error) {
	n, e := c.Conn.Read(p)
	c.mu.Lock()
	s := c.state
	c.mu.Unlock()
	if s != nil {
		s.observe(p[:n])
	}
	return n, e
}
func (c *proxyOrderConn) set(s *proxyOrderState) { c.mu.Lock(); c.state = s; c.mu.Unlock() }

type proxyOrderTransport struct{ base *http.Transport }

func (t *proxyOrderTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	state := &proxyOrderState{}
	ctx := context.WithValue(r.Context(), proxyOrderKey{}, state)
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		if c, ok := info.Conn.(*proxyOrderConn); ok {
			c.set(state)
		}
	}})
	return t.base.RoundTrip(r.WithContext(ctx))
}
func newProxyTransport() http.RoundTripper {
	dialer := &net.Dialer{}
	base := &http.Transport{Proxy: http.ProxyFromEnvironment, DisableCompression: true, MaxIdleConns: 100, MaxIdleConnsPerHost: 100}
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		c, e := dialer.DialContext(ctx, network, address)
		if e != nil {
			return nil, e
		}
		return &proxyOrderConn{Conn: c}, nil
	}
	base.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		c, e := (&tls.Dialer{NetDialer: dialer}).DialContext(ctx, network, address)
		if e != nil {
			return nil, e
		}
		return &proxyOrderConn{Conn: c}, nil
	}
	return &proxyOrderTransport{base: base}
}
func proxyResponseHeaderOrder(r *http.Response) []string {
	if r.Request == nil {
		return nil
	}
	state, _ := r.Request.Context().Value(proxyOrderKey{}).(*proxyOrderState)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return append([]string(nil), state.names...)
}
