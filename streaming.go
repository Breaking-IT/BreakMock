// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
// SSE framing follows WireMock 4.0.0-beta.40 JettySseSession (Apache-2.0,
// Copyright 2025-2026 Thomas Akehurst).
package breakmock

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Breaking-IT/BreakMock/internal/websocket"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

func (s *Server) serveStreaming(w http.ResponseWriter, r *http.Request, req Request, response map[string]any, matched Mapping) bool {
	if response["openWebsocketChannel"] == true {
		s.serveWebSocket(w, r, req, matched)
		return true
	}
	if response["openSseChannel"] == true {
		s.serveSSE(w, r, req, matched, response)
		return true
	}
	return false
}
func (s *Server) serveWebSocket(w http.ResponseWriter, r *http.Request, req Request, matched Mapping) {
	if r.Method != "GET" || r.Header.Get("Sec-WebSocket-Version") != "13" || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		w.WriteHeader(200)
		return
	}
	if r.Header.Get("Sec-WebSocket-Key") == "" {
		streamingHandshakeError(w, r, "Missing request header 'Sec-WebSocket-Key'")
		return
	}
	DisableRequestHeaderCapture(r)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }, EnableCompression: true, ReadBufferSize: 4096, WriteBufferSize: 65536, AllowNonStandardChallengeKey: true, ProtocolError: streamingProtocolError, Error: func(w http.ResponseWriter, r *http.Request, status int, reason error) {
		streamingHandshakeError(w, r, "Bad Request")
	}}
	var conn *websocket.Conn
	var channel *MessageChannel
	ready := make(chan struct{})
	messages := s.Store.Messaging()
	var writes sync.Mutex
	send := func(message map[string]any) error {
		<-ready
		if conn == nil {
			return fmt.Errorf("websocket upgrade failed")
		}
		kind := websocket.TextMessage
		data := []byte(bmString(message["body"]))
		if encoded, ok := message["base64Body"].(string); ok {
			kind = websocket.BinaryMessage
			var err error
			data, err = base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return err
			}
		}
		writes.Lock()
		defer writes.Unlock()
		writer, err := conn.NextWriter(kind)
		if err != nil {
			return err
		}
		for len(data) > 0 {
			n := len(data)
			if n > 65536 {
				n = 65536
			}
			if _, err = writer.Write(data[:n]); err != nil {
				return err
			}
			data = data[n:]
		}
		return writer.Close()
	}
	var closing sync.Once
	closeChannel := func() {
		closing.Do(func() {
			<-ready
			if conn == nil {
				return
			}
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Closed by server"), time.Now().Add(time.Second))
			_ = conn.Close()
		})
	}

	upgrader.OnConnect = func(c *websocket.Conn) {
		conn = c
		channel = messages.RegisterChannel("", "websocket", req, send, closeChannel)
	}
	idle := s.options.WebSocketIdleTimeout
	if !s.options.WebSocketOptionsConfigured && idle == 0 {
		idle = 300000
	}
	upgraded, e := upgrader.Upgrade(streamingUpgradeWriter{ResponseWriter: w, idle: time.Duration(idle) * time.Millisecond}, r, nil)
	close(ready)
	if channel != nil {
		defer messages.UnregisterChannel(channel.ID)
	}
	if e != nil {
		return
	}
	_ = upgraded
	if capture, ok := w.(*servedResponseWriter); ok {
		capture.status = 101
		capture.wroteHeader = true
		capture.startedSending = time.Now()
	}
	defer conn.Close()
	textLimit, binaryLimit := s.options.WebSocketMaxTextMessageSize, s.options.WebSocketMaxBinaryMessageSize
	if !s.options.WebSocketOptionsConfigured && textLimit == 0 {
		textLimit = 10485760
	}
	if !s.options.WebSocketOptionsConfigured && binaryLimit == 0 {
		binaryLimit = 10485760
	}
	conn.SetMessageReadLimits(textLimit, binaryLimit)
	conn.SetCloseHandler(func(code int, text string) error { closeChannel(); return nil })
	for {
		kind, reader, err := conn.NextReader()
		var data []byte
		if err == nil {
			limit := textLimit
			label := "Text"
			if kind == websocket.BinaryMessage {
				limit, label = binaryLimit, "Binary"
			}
			if limit > 0 {
				// Bound decompressed messages as well as on-wire frame lengths.
				// Jetty inflates in 8 KiB chunks and reports the total at the
				// chunk that crosses the configured message limit.
				var chunk [8192]byte
				for {
					n, readErr := reader.Read(chunk[:])
					size := int64(len(data)) + int64(n)
					if size > limit {
						reason := streamingMessageLimitReason(label, size, limit)
						_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1009, reason), time.Now().Add(time.Second))
						return
					}
					data = append(data, chunk[:n]...)
					if readErr != nil {
						if readErr != io.EOF {
							err = readErr
						}
						break
					}
				}
			} else {
				data, err = io.ReadAll(reader)
			}
		}
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1001, "Connection Idle Timeout"), time.Now().Add(time.Second))
			}
			return
		}
		message := map[string]any{}
		switch kind {
		case websocket.TextMessage:
			if !utf8.Valid(data) {
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInvalidFramePayloadData, "Invalid UTF-8"), time.Now().Add(time.Second))
				return
			}
			message["body"] = string(data)
		case websocket.BinaryMessage:
			message["base64Body"] = base64.StdEncoding.EncodeToString(data)
		default:
			continue
		}
		if err := messages.Incoming(channel.ID, message); err != nil {
			reason := err.Error()
			if _, ok := err.(*wireEntityError); ok {
				if index := strings.Index(reason, ": "); index >= 0 {
					reason = reason[index+2:]
				}
			}
			if len(reason) > 123 {
				reason = reason[:123]
				for !utf8.ValidString(reason) {
					reason = reason[:len(reason)-1]
				}
			}
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1011, reason), time.Now().Add(time.Second))
			return
		}
	}
}
func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request, req Request, matched Mapping, response map[string]any) {
	// Jetty's streaming branch bypasses normal response headers and status.
	if headers, ok := response["headers"].(map[string]any); ok {
		for name := range headers {
			w.Header().Del(name)
		}
	}
	w.Header().Set("Content-Type", "text/event-stream;charset=UTF-8")
	w.Header().Set("Connection", "close")
	w.Header().Del("Content-Length")
	// Stream frames are message journal events, not the HTTP response body. Avoid
	// retaining an unbounded copy in the HTTP request journal's capture buffer.
	output := w
	if captured, ok := w.(*servedResponseWriter); ok {
		output = captured.ResponseWriter
	}
	var mu sync.Mutex
	closed := false
	done := make(chan struct{})
	closeChannel := func() {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			closed = true
			close(done)
		}
	}
	send := func(message map[string]any) error {
		frame := sseFrame(message)
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return fmt.Errorf("SSE channel is closed")
		}
		if _, e := output.Write([]byte(frame)); e != nil {
			closed = true
			close(done)
			return e
		}
		if err := http.NewResponseController(output).Flush(); err != nil {
			closed = true
			close(done)
			return err
		}
		return nil
	}
	messages := s.Store.Messaging()
	channel := messages.RegisterChannel("", "sse", req, send, closeChannel)
	defer messages.UnregisterChannel(channel.ID)
	defer closeChannel()
	mu.Lock()
	w.WriteHeader(http.StatusOK)
	_, e := output.Write([]byte(": ok\r\n\r\n"))
	if f, ok := output.(http.Flusher); ok {
		f.Flush()
	}
	mu.Unlock()
	if e != nil {
		return
	}
	// Jetty retains an SSE channel after peer disconnect until a later write
	// discovers the closed socket or the channel is explicitly deleted. Keep
	// ownership of ResponseWriter for that lifetime; never write after return.
	<-done
}
func sseFrame(message map[string]any) string {
	var b strings.Builder
	headers, _ := message["headers"].(map[string]any)
	for _, pair := range [][2]string{{"comment", ": "}, {"id", "id: "}, {"event", "event: "}, {"retry", "retry: "}} {
		var v any
		for k, value := range headers {
			if strings.EqualFold(k, pair[0]) {
				v = value
				break
			}
		}
		if values, ok := v.([]any); ok {
			if len(values) == 0 {
				v = nil
			} else {
				v = values[0]
			}
		}
		if values, ok := v.([]string); ok {
			if len(values) == 0 {
				v = nil
			} else {
				v = values[0]
			}
		}
		if v == nil {
			continue
		}
		value := bmString(v)
		if strings.ContainsAny(value, "\r\n") {
			continue
		}
		b.WriteString(pair[1])
		b.WriteString(value)
		b.WriteString("\r\n")
	}
	data := bmString(message["body"])
	if encoded, ok := message["base64Body"].(string); ok {
		data = encoded
	}
	data = strings.ReplaceAll(strings.ReplaceAll(data, "\r\n", "\n"), "\r", "\n")
	for _, line := range strings.Split(data, "\n") {
		b.WriteString("data:")
		if line != "" {
			b.WriteByte(' ')
			b.WriteString(line)
		}
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	return b.String()
}

func streamingMessageLimitReason(name string, length, limit int64) string {
	printer := message.NewPrinter(language.Make(strings.ReplaceAll(templateDefaultLocale(), "_", "-")))
	return fmt.Sprintf("%s message too large: %s > %s", name, printer.Sprintf("%d", length), printer.Sprintf("%d", limit))
}
func streamingProtocolError(frameType int, reason string) (int, string) {
	switch {
	case strings.HasPrefix(reason, "read limit "):
		var length, limit int64
		fmt.Sscanf(reason, "read limit %d %d", &length, &limit)
		name := "Text"
		if frameType == 2 {
			name = "Binary"
		}
		return 1009, streamingMessageLimitReason(name, length, limit)
	case strings.Contains(reason, "bad MASK"):
		return 1002, "Client MUST mask all frames (RFC-6455: Section 5.1)"
	case strings.Contains(reason, "RSV1 set"):
		return 1002, "RSV1 not allowed to be set"
	case strings.Contains(reason, "RSV2 set"):
		return 1002, "RSV2 not allowed to be set"
	case strings.Contains(reason, "RSV3 set"):
		return 1002, "RSV3 not allowed to be set"
	case strings.HasPrefix(reason, "bad opcode "):
		return 1002, "Unknown opcode: " + strings.TrimPrefix(reason, "bad opcode ")
	case reason == "FIN not set on control":
		names := map[int]string{8: "CLOSE", 9: "PING", 10: "PONG"}
		return 1002, "Fragmented Control Frame [" + names[frameType] + "]"
	case reason == "one byte close payload":
		return 1002, "Invalid CLOSE payload"
	}
	return 1002, reason
}
func streamingHandshakeError(w http.ResponseWriter, r *http.Request, message string) {
	escape := func(v string) string {
		return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;").Replace(v)
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	uri := escape(scheme + "://" + r.Host + r.URL.RequestURI())
	msg := escape(message)
	body := "<html>\n<head>\n<meta http-equiv=\"Content-Type\" content=\"text/html;charset=ISO-8859-1\"/>\n<title>Error 400 " + msg + "</title>\n</head>\n<body>\n<h2>HTTP ERROR 400 " + msg + "</h2>\n<table>\n<tr><th>URI:</th><td>" + uri + "</td></tr>\n<tr><th>STATUS:</th><td>400</td></tr>\n<tr><th>MESSAGE:</th><td>" + msg + "</td></tr>\n</table>\n\n</body>\n</html>\n"
	w.Header().Set("Content-Type", "text/html;charset=iso-8859-1")
	w.Header().Set("Cache-Control", "must-revalidate,no-cache,no-store")
	w.WriteHeader(400)
	_, _ = w.Write([]byte(body))
}

type streamingIdleConn struct {
	net.Conn
	idle time.Duration
}

func (c *streamingIdleConn) Read(p []byte) (int, error) {
	if c.idle > 0 {
		_ = c.Conn.SetDeadline(time.Now().Add(c.idle))
	}
	return c.Conn.Read(p)
}
func (c *streamingIdleConn) Write(p []byte) (int, error) {
	if c.idle > 0 {
		_ = c.Conn.SetDeadline(time.Now().Add(c.idle))
	}
	return c.Conn.Write(p)
}

type streamingUpgradeWriter struct {
	http.ResponseWriter
	idle time.Duration
}

func (w streamingUpgradeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buffer, err := w.ResponseWriter.(http.Hijacker).Hijack()
	if err != nil {
		return nil, nil, err
	}
	return &streamingIdleConn{Conn: conn, idle: w.idle}, buffer, nil
}
