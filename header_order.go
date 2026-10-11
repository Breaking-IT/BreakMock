// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

const captureMaxHeaderBytes = 1 << 20

type headerOrderContextKey struct{}
type headerOrderState struct {
	mu    sync.Mutex
	ready *sync.Cond
	queue [][]string
	done  bool
}

func newHeaderOrderState() *headerOrderState {
	s := &headerOrderState{}
	s.ready = sync.NewCond(&s.mu)
	return s
}
func (s *headerOrderState) stop() { s.mu.Lock(); s.done = true; s.ready.Broadcast(); s.mu.Unlock() }
func (s *headerOrderState) push(order []string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return false
	}
	if len(s.queue) >= 256 {
		s.done = true
		s.ready.Broadcast()
		return false
	}
	s.queue = append(s.queue, order)
	s.ready.Signal()
	return true
}
func (s *headerOrderState) pop() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.queue) == 0 && !s.done {
		s.ready.Wait()
	}
	if len(s.queue) == 0 {
		return nil
	}
	v := s.queue[0]
	s.queue[0] = nil
	s.queue = s.queue[1:]
	return v
}

// WrapHeaderOrderListener captures HTTP/1 request header spelling and order.
// net/http remains the authoritative request parser. An independent standard
// http.ReadRequest parser consumes a bounded tee to track framing and metadata.
// Pair with HeaderOrderConnContext and call RequestHeaderOrder exactly once at
// the start of EVERY request, including administration requests.
func WrapHeaderOrderListener(listener net.Listener) net.Listener {
	return &headerOrderListener{Listener: listener}
}

type headerOrderListener struct{ net.Listener }

func (l *headerOrderListener) Accept() (net.Conn, error) {
	conn, e := l.Listener.Accept()
	if e != nil {
		return nil, e
	}
	reader, writer := io.Pipe()
	state := newHeaderOrderState()
	c := &headerOrderConn{Conn: conn, writer: writer, state: state}
	go captureRequestHeaderOrders(reader, state)
	return c, nil
}

type headerOrderConn struct {
	net.Conn
	writer *io.PipeWriter
	state  *headerOrderState
	once   sync.Once
}

func (c *headerOrderConn) Read(p []byte) (int, error) {
	n, e := c.Conn.Read(p)
	if n > 0 {
		_, _ = c.writer.Write(p[:n])
	}
	if e != nil {
		// net/http uses temporary expired read deadlines to interrupt its
		// background keep-alive read. A timeout must not end capture forever.
		if timeout, ok := e.(net.Error); !ok || !timeout.Timeout() {
			_ = c.writer.CloseWithError(e)
		}
	}
	return n, e
}
func (c *headerOrderConn) Close() error {
	c.once.Do(func() { c.state.stop(); _ = c.writer.Close() })
	return c.Conn.Close()
}
func (c *headerOrderConn) SetLinger(seconds int) error {
	if conn, ok := c.Conn.(interface{ SetLinger(int) error }); ok {
		return conn.SetLinger(seconds)
	}
	return errors.New("underlying connection does not support SetLinger")
}

// HeaderOrderConnContext is suitable for http.Server.ConnContext.
func HeaderOrderConnContext(ctx context.Context, conn net.Conn) context.Context {
	if c, ok := conn.(*headerOrderConn); ok {
		return context.WithValue(ctx, headerOrderContextKey{}, c.state)
	}
	return ctx
}

// RequestHeaderOrder consumes this request's wire-order metadata. Without the
// listener integration (e.g. httptest.NewRecorder), it returns nil.
func RequestHeaderOrder(request *http.Request) []string {
	if state, ok := request.Context().Value(headerOrderContextKey{}).(*headerOrderState); ok {
		return state.pop()
	}
	return nil
}

type captureHeaderReader struct {
	source    io.Reader
	capturing bool
	bytes     []byte
}

func (r *captureHeaderReader) Read(p []byte) (int, error) {
	n, e := r.source.Read(p)
	if r.capturing && n > 0 {
		if len(r.bytes)+n > captureMaxHeaderBytes {
			return 0, errors.New("header capture limit exceeded")
		}
		r.bytes = append(r.bytes, p[:n]...)
	}
	return n, e
}
func captureRequestHeaderOrders(reader *io.PipeReader, state *headerOrderState) {
	defer reader.Close()
	defer state.stop()
	tap := &captureHeaderReader{source: reader}
	buffer := bufio.NewReader(tap)
	for {
		tap.bytes = tap.bytes[:0]
		if n := buffer.Buffered(); n > 0 {
			pending, _ := buffer.Peek(n)
			tap.bytes = append(tap.bytes, pending...)
		}
		tap.capturing = true
		request, e := http.ReadRequest(buffer)
		tap.capturing = false
		if e != nil {
			return
		}
		order := parseWireHeaderOrder(tap.bytes)
		if !state.push(order) {
			return
		}
		// Do not retain large header storage over an idle keep-alive connection.
		if cap(tap.bytes) > 65536 {
			tap.bytes = nil
		}
		if request.Body != nil {
			_, e = io.Copy(io.Discard, request.Body)
			_ = request.Body.Close()
			if e != nil {
				return
			}
		}
	}
}
func parseWireHeaderOrder(raw []byte) []string {
	lines := strings.Split(string(raw), "\n")
	order := []string{}
	seen := map[string]bool{}
	for _, line := range lines[1:] {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			break
		}
		if line[0] == ' ' || line[0] == '\t' {
			continue
		}
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.ToLower(name)
		if !seen[key] {
			seen[key] = true
			order = append(order, name)
		}
	}
	return order
}
