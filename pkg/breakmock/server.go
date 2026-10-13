// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
// Independently implemented against WireMock 4.0.0-beta.40's public protocol and source.
package breakmock

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Options struct {
	WebSocketOptionsConfigured    bool
	WebSocketIdleTimeout          int64
	WebSocketMaxTextMessageSize   int64
	WebSocketMaxBinaryMessageSize int64
	DisableRequestLogging         bool
	RootDir                       string
	GlobalTemplating              bool
	DisableTemplating             bool
	DisableJournal                bool
	MaxJournalEntries             int
	Toxiproxy                     ToxiproxyOptions
}
type Server struct {
	Store      *Store
	options    Options
	admin      http.Handler
	client     *http.Client
	scenarioMu sync.Mutex
	toxiproxy  *embeddedToxiproxy
}

func NewServer(options Options) (*Server, error) {
	if options.RootDir == "" {
		options.RootDir = "."
	}
	store := NewStore()
	store.Messaging().rootDir = options.RootDir
	store.ConfigureJournal(!options.DisableJournal, options.MaxJournalEntries)
	s := &Server{Store: store, options: options, client: &http.Client{Transport: newProxyTransport(), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	s.admin = NewAdminHandler(store, options.RootDir)
	if err := s.LoadMappings(); err != nil {
		return nil, err
	}
	if options.Toxiproxy.Enabled {
		var err error
		s.toxiproxy, err = newEmbeddedToxiproxy(options.Toxiproxy)
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}
func (s *Server) LoadMappings() error {
	root := filepath.Join(s.options.RootDir, "mappings")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if os.IsNotExist(err) && path == root {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		m, e := DecodeDocument(b)
		if e != nil {
			return fmt.Errorf("mapping %s: %w", path, e)
		}
		if _, ok := m["mappings"]; ok {
			var bulk struct {
				Mappings []json.RawMessage `json:"mappings"`
			}
			if e = json.Unmarshal(b, &bulk); e != nil {
				return e
			}
			for _, raw := range bulk.Mappings {
				if _, e = s.Store.AddMappingDocument(raw); e != nil {
					return e
				}
			}
			return nil
		}
		_, e = s.Store.AddMappingDocument(b)
		return e
	})
	if err != nil {
		return err
	}
	return s.Store.Messaging().LoadMappings(filepath.Join(s.options.RootDir, "message-mappings"))
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	headerOrder := RequestHeaderOrder(r)
	if s.toxiproxy != nil && (r.URL.Path == toxiproxyAPIPrefix || strings.HasPrefix(r.URL.Path, toxiproxyAPIPrefix+"/")) {
		s.toxiproxy.handler.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/__admin" || strings.HasPrefix(r.URL.Path, "/__admin/") {
		s.admin.ServeHTTP(w, r)
		return
	}
	// Preserve a present, empty body for matchers without allocating ReadAll's buffer.
	body := []byte{}
	var err error
	if r.Body != http.NoBody {
		body, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Cannot read request", 400)
			return
		}
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	decodedBody := body
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		gz, e := gzip.NewReader(bytes.NewReader(body))
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		decodedBody, e = io.ReadAll(gz)
		gz.Close()
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
	}
	requestURI := r.URL.RequestURI()
	req := Request{HeaderOrder: headerOrder, Method: r.Method, URL: requestURI, AbsoluteURL: scheme + "://" + r.Host + requestURI, Headers: r.Header.Clone(), Body: decodedBody}
	req.Headers.Set("Host", r.Host)
	decodeLatin1Headers(req.Headers)
	req.ClientIP, _, _ = net.SplitHostPort(r.RemoteAddr)
	var selected Mapping
	var capture *servedResponseWriter
	var processStarted time.Time
	var waited time.Duration
	if !s.options.DisableJournal {
		processStarted = time.Now()
		capture = &servedResponseWriter{ResponseWriter: w, status: 200}
		w = capture
	}
	defer func() {
		eventID := ""
		if capture != nil {
			finished := capture.startedSending
			if finished.IsZero() {
				finished = time.Now()
			}
			req.ProcessTimeMillis = max(int64(0), (finished.Sub(processStarted) - waited).Milliseconds())
			eventID = s.Store.recordServed(req, selected, capture.status, capture.Header(), capture.body.Bytes(), capture.headerOrder)
		}
		if selected != nil {
			DispatchWebhooks(s.Store, eventID, req, selected, s.options.RootDir)
		}
	}()

	// Hold the scenario lock across the complete candidate scan, not one mapping.
	scenarioLocked := false
	// Stateless requests need no scenario lock.
	for _, m := range s.Store.Mappings() {
		pattern, _ := m["request"].(map[string]any)
		if !MatchRequest(pattern, req) {
			continue
		}
		name := bmString(m["scenarioName"])
		if name != "" {
			if !scenarioLocked {
				s.scenarioMu.Lock()
				scenarioLocked = true
			}
			required := bmString(m["requiredScenarioState"])
			if required != "" && s.Store.ScenarioState(name) != required {
				continue
			}
		}
		selected = m
		if name != "" {
			if next := bmString(m["newScenarioState"]); next != "" {
				s.Store.SetScenarioState(name, next)
			}
		}
		break
	}
	if scenarioLocked {
		s.scenarioMu.Unlock()
	}
	if selected == nil {
		if e := s.Store.Messaging().TriggerHTTP(req); e != nil {
			writeJettyError(w, r, 500, e.Error())
			return
		}
		out := s.unmatchedBody(req, diagnosticWidth(r))
		req.UnmatchedReport = string(out)
		req.UnmatchedContentType = "text/plain"
		w.Header().Set("Content-Type", "text/plain")
		if acceptsGzip(r.Header.Get("Accept-Encoding")) && gzipResponseAllowed(r, 404, w.Header()) {
			if compressed, err := wireGzip(out); err == nil {
				out = compressed
				w.Header().Set("Content-Encoding", "gzip")
			}
		}
		w.WriteHeader(404)
		w.Write(out)
		return
	}
	pattern, _ := selected["request"].(map[string]any)
	if tpl := bmString(pattern["urlPathTemplate"]); tpl != "" {
		req.PathParams, _ = matchPathTemplate(tpl, r.URL.EscapedPath())
	}
	if e := s.Store.Messaging().TriggerHTTP(req, selected); e != nil {
		writeJettyError(w, r, 500, e.Error())
		return
	}
	response, _ := selected["response"].(map[string]any)
	status := int(bmNumber(response["status"], 200))
	if status < 100 || status > 599 {
		status = 500
	}

	settings := s.Store.SettingsSnapshot()
	delay := bmNumber(response["fixedDelayMilliseconds"], bmNumber(settings["fixedDelay"], 0))
	distribution := response["delayDistribution"]
	if distribution == nil && response["fixedDelayMilliseconds"] == nil {
		distribution = settings["delayDistribution"]
	}
	if dist, ok := distribution.(map[string]any); ok {
		switch bmString(dist["type"]) {
		case "uniform":
			lo := bmNumber(dist["lower"], 0)
			hi := bmNumber(dist["upper"], lo)
			delay = lo + rand.Float64()*(hi-lo)
		case "lognormal":
			median := bmNumber(dist["median"], 1)
			sigma := bmNumber(dist["sigma"], 1)
			delay = math.Exp(math.Log(median) + sigma*rand.NormFloat64())
		}
	}
	if delay > 0 {
		req.AddedDelayMillis = int64(delay)
		waitingStarted := time.Now()
		timer := time.NewTimer(time.Duration(delay * float64(time.Millisecond)))
		defer timer.Stop()
		select {
		case <-timer.C:
			waited += time.Since(waitingStarted)
		case <-r.Context().Done():
			waited += time.Since(waitingStarted)
			return
		}
	}
	if fault := bmString(response["fault"]); fault != "" {
		s.serveFault(w, r, fault)
		return
	}
	templated := s.options.GlobalTemplating
	for _, v := range bmSlice(response["transformers"]) {
		if v == "response-template" {
			templated = true
		}
	}
	if s.options.DisableTemplating {
		templated = false
	}
	params, _ := response["transformerParameters"].(map[string]any)
	render := func(v string) (string, error) {
		if !templated {
			return v, nil
		}
		return RenderTemplate(v, req, params)
	}
	if headers, ok := response["headers"].(map[string]any); ok {
		for name, value := range headers {
			values := bmSlice(value)
			if values == nil {
				values = []any{value}
			}
			for _, item := range values {
				v, e := render(fmt.Sprint(item))
				if e != nil {
					s.writeTemplateError(w, e, selected)
					return
				}
				w.Header().Add(name, v)
			}
		}
	}
	if s.serveStreaming(w, r, req, response, selected) {
		return
	}
	if base := bmString(response["proxyBaseUrl"]); base != "" {
		base, err = render(base)
		if err != nil {
			s.writeTemplateError(w, err, selected)
			return
		}
		if templated {
			copyResponse := make(map[string]any, len(response))
			for k, v := range response {
				copyResponse[k] = v
			}
			if additional, ok := response["additionalProxyRequestHeaders"].(map[string]any); ok {
				transformed := make(map[string]any, len(additional))
				for name, value := range additional {
					values := bmSlice(value)
					if values == nil {
						values = []any{value}
					}
					rendered := make([]any, 0, len(values))
					for _, value := range values {
						v, e := render(fmt.Sprint(value))
						if e != nil {
							s.writeTemplateError(w, e, selected)
							return
						}
						rendered = append(rendered, v)
					}
					transformed[name] = rendered
				}
				copyResponse["additionalProxyRequestHeaders"] = transformed
			}
			response = copyResponse
		}
		s.proxy(w, r, body, base, response)
		return
	}
	var out []byte
	entity := resolvedEntity{}
	switch {
	case response["jsonBody"] != nil:
		if raw, ok := response["jsonBody"].(json.RawMessage); ok {
			// Stored mappings hold jsonBody in its compact wire form (see DecodeDocument).
			out = raw
		} else {
			out, err = marshalJSON(response["jsonBody"])
		}
	case response["base64Body"] != nil:
		out, err = base64.StdEncoding.DecodeString(bmString(response["base64Body"]))
		entity.binary = true
	case response["bodyFileName"] != nil:
		name, e := render(bmString(response["bodyFileName"]))
		if e != nil {
			err = e
			break
		}
		entity, err = resolveBodyEntity(map[string]any{"filePath": name}, s.options.RootDir)
		out = entity.data
	default:
		value := response["body"]
		if object, ok := value.(map[string]any); ok && object["filePath"] != nil {
			copied := cloneMap(object)
			name, e := render(bmString(object["filePath"]))
			if e != nil {
				err = e
				break
			}
			copied["filePath"] = name
			value = copied
		}
		entity, err = resolveBodyEntity(value, s.options.RootDir)
		out = entity.data
	}
	if err != nil {
		var entityError *wireEntityError
		if errors.As(err, &entityError) {
			writeJettyError(w, r, 500, entityError.Error())
		} else {
			s.writeTemplateError(w, err, selected)
		}
		return
	}
	entity.data = out
	disableFileTemplate := params["disableBodyFileTemplating"] == true && entity.fromFile
	rendered := false
	if templated && !disableFileTemplate {
		source, e := entity.text()
		if e != nil {
			s.writeTemplateError(w, e, selected)
			return
		}
		if strings.Contains(source, "{{") {
			v, e := render(source)
			if e != nil {
				s.writeTemplateError(w, e, selected)
				return
			}
			out = readOnlyBytes(v)
			rendered = true
		}
	}
	// Avoid Go's automatic content sniffing; WireMock does not infer a type for inline bodies.
	if _, ok := w.Header()["Content-Type"]; !ok {
		w.Header()["Content-Type"] = nil
	}
	if acceptsGzip(r.Header.Get("Accept-Encoding")) && gzipResponseAllowed(r, status, w.Header()) {
		compress := wireGzip
		if !rendered {
			// Static bodies are identical across requests; reuse their compressed form.
			compress = cachedWireGzip
		}
		compressed, err := compress(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out = compressed
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")
	}
	w.Header().Set("Matched-Stub-Id", bmString(selected["id"]))
	if name := bmString(selected["name"]); name != "" {
		w.Header().Set("Matched-Stub-Name", name)
	}
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	if chunk, ok := response["chunkedDribbleDelay"].(map[string]any); ok {
		n := int(bmNumber(chunk["numberOfChunks"], 1))
		if n < 1 {
			n = 1
		}
		d := time.Duration(bmNumber(chunk["totalDuration"], 0)) * time.Millisecond / time.Duration(n)
		for i := 0; i < n; i++ {
			if d > 0 {
				timer := time.NewTimer(d)
				select {
				case <-timer.C:
				case <-r.Context().Done():
					timer.Stop()
					return
				}
			}
			w.Write(out[len(out)*i/n : len(out)*(i+1)/n])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		return
	}
	w.Write(out)
}
func (s *Server) proxy(w http.ResponseWriter, r *http.Request, body []byte, base string, response map[string]any) {
	target, e := url.Parse(base)
	if e != nil || target.Scheme == "" {
		http.Error(w, "Invalid proxyBaseUrl", 500)
		return
	}
	path := r.URL.EscapedPath()
	if prefix := bmString(response["proxyUrlPrefixToRemove"]); prefix != "" {
		path = strings.TrimPrefix(path, prefix)
	}
	escapedPath := strings.TrimRight(target.EscapedPath(), "/") + path
	target.Path, e = url.PathUnescape(escapedPath)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	target.RawPath = escapedPath
	target.RawQuery = r.URL.RawQuery
	upstream, e := http.NewRequestWithContext(r.Context(), r.Method, target.String(), bytes.NewReader(body))
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	upstream.Header = r.Header.Clone()
	upstream.Header.Del("Connection")
	for _, name := range bmSlice(response["removeProxyRequestHeaders"]) {
		upstream.Header.Del(fmt.Sprint(name))
	}
	if add, ok := response["additionalProxyRequestHeaders"].(map[string]any); ok {
		for k, v := range add {
			values := bmSlice(v)
			if values == nil {
				values = []any{v}
			}
			upstream.Header.Del(k)
			if len(values) > 1 {
				values = values[:1]
			}
			for _, value := range values {
				upstream.Header.Add(k, fmt.Sprint(value))
			}
		}
	}

	res, e := s.client.Do(upstream)
	if e != nil {
		http.Error(w, e.Error(), 502)
		return
	}
	defer res.Body.Close()
	if capture, ok := w.(*servedResponseWriter); ok {
		capture.headerOrder = proxyResponseHeaderOrder(res)
	}
	for k, vs := range res.Header {
		configured := append([]string(nil), w.Header().Values(k)...)
		w.Header().Del(k)
		for _, v := range vs {
			w.Header().Add(k, v)
		}
		for _, v := range configured {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(res.StatusCode)
	io.Copy(w, res.Body)
}
func (s *Server) serveFault(w http.ResponseWriter, r *http.Request, fault string) {
	h, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Fault requires HTTP/1.x", 500)
		return
	}
	c, b, e := h.Hijack()
	if e != nil {
		return
	}
	defer c.Close()
	switch fault {
	case "EMPTY_RESPONSE":
		return
	case "CONNECTION_RESET_BY_PEER":
		if tcp, ok := c.(interface{ SetLinger(int) error }); ok {
			tcp.SetLinger(0)
		}
	case "MALFORMED_RESPONSE_CHUNK":
		b.WriteString("HTTP/1.1 200 OK\r\n")
		if r.ProtoMajor == 1 && r.ProtoMinor >= 1 {
			if r.Close {
				b.WriteString("Connection: close\r\n")
			} else {
				b.WriteString("Transfer-Encoding: chunked\r\n")
			}
		}
		b.WriteString("\r\nlskdu018973t09sylgasjkfg1][]'./.sdlv")
		b.Flush()
	case "RANDOM_DATA_THEN_CLOSE":
		// WireMock JettyFaultInjector's fixed diagnostic garbage bytes.
		b.WriteString("lskdu018973t09sylgasjkfg1][]'./.sdlv")
		b.Flush()
	}
}
func bmString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
func bmNumber(v any, fallback float64) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case json.Number:
		f, e := n.Float64()
		if e == nil {
			return f
		}
	}
	return fallback
}
func bmSlice(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}

// servedResponseWriter captures the actual response for the request journal and recording.
type servedResponseWriter struct {
	http.ResponseWriter
	startedSending time.Time
	headerOrder    []string
	status         int
	wroteHeader    bool
	body           bytes.Buffer
}

func (w *servedResponseWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.startedSending = time.Now()
		w.status = code
		w.wroteHeader = true
		w.ResponseWriter.WriteHeader(code)
	}
}
func (w *servedResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(200)
	}
	n, e := w.ResponseWriter.Write(b)
	w.body.Write(b[:n])
	return n, e
}
func (w *servedResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(200)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *servedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijacking unavailable")
	}
	return h.Hijack()
}
func (w *servedResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// decodeLatin1Headers decodes header values in place as Jetty does: each byte
// is one ISO-8859-1 character. Matching, templates and the journal then see the
// same text as WireMock, for example byte 0xE9 as "é" and UTF-8 "é" as "Ã©".
func decodeLatin1Headers(headers http.Header) {
	for _, values := range headers {
		for i, value := range values {
			values[i] = latin1String(value)
		}
	}
}

func latin1String(value string) string {
	for i := 0; i < len(value); i++ {
		if value[i] >= utf8.RuneSelf {
			decoded := make([]rune, len(value))
			for j := 0; j < len(value); j++ {
				decoded[j] = rune(value[j])
			}
			return string(decoded)
		}
	}
	return value
}

func acceptsGzip(value string) bool {
	for _, part := range strings.Split(value, ",") {
		p := strings.Split(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(p[0]), "gzip") {
			continue
		}
		enabled := true
		for _, param := range p[1:] {
			pair := strings.SplitN(strings.TrimSpace(param), "=", 2)
			if len(pair) == 2 && strings.EqualFold(strings.TrimSpace(pair[0]), "q") {
				if quality, err := json.Number(strings.TrimSpace(pair[1])).Float64(); err == nil && quality == 0 {
					enabled = false
				}
			}
		}
		if enabled {
			return true
		}
	}
	return false
}

func gzipContentType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	// Jetty excludes its known compressed media types, rather than excluding
	// whole top-level MIME families. Unknown types remain compressible.
	switch value {
	case "application/compress", "application/zip", "application/gzip", "application/bzip2", "application/x-bzip2", "application/brotli", "application/x-xz", "application/x-rar-compressed", "text/event-stream",
		"audio/basic", "audio/midi", "audio/mpeg", "audio/x-aiff", "audio/x-pn-realaudio", "audio/x-wav",
		"image/apng", "image/avif", "image/bmp", "image/gif", "image/ief", "image/jpeg", "image/jpeg2000", "image/png", "image/tiff", "image/vnd.wap.wbmp", "image/webp", "image/x-cmu-raster", "image/x-icon", "image/x-portable-anymap", "image/x-portable-bitmap", "image/x-portable-graymap", "image/x-portable-pixmap", "image/x-rgb", "image/x-xbitmap", "image/x-xpixmap", "image/x-xwindowdump", "image/xcf",
		"video/mp4", "video/mpeg", "video/quicktime", "video/vnd.rn-realvideo", "video/x-msvideo", "video/x-sgi-movie", "video/x.ms.asf", "video/x.ms.asx":
		return false
	default:
		return true
	}
}

func gzipResponseAllowed(request *http.Request, status int, headers http.Header) bool {
	if status < 200 || status == 204 || status == 304 || headers.Get("Content-Encoding") != "" || !gzipContentType(headers.Get("Content-Type")) {
		return false
	}
	switch request.Method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return false
	}
	if length := headers.Get("Content-Length"); length != "" {
		if n, err := json.Number(length).Int64(); err == nil && n >= 0 && n < 32 {
			return false
		}
	}
	if strings.HasSuffix(request.URL.Path, ".svgz") {
		return false
	}
	// The gzip handler checks Jetty's MIME mapping for the request path before
	// invoking the stub. These are the excluded extensions from that mapping.
	switch strings.ToLower(filepath.Ext(request.URL.Path)) {
	case ".aif", ".aifc", ".aiff", ".apng", ".asf", ".asx", ".au", ".avi", ".avif", ".bmp", ".br", ".gif", ".gz", ".gzip", ".ico", ".ief", ".jpe", ".jp2", ".jpeg", ".jpg", ".kar", ".mid", ".midi", ".mov", ".movie", ".mp2", ".mp3", ".mp4", ".mpe", ".mpeg", ".mpg", ".mpga", ".pbm", ".pgm", ".png", ".pnm", ".ppm", ".qt", ".ra", ".rar", ".ram", ".ras", ".rgb", ".rm", ".rv", ".snd", ".tif", ".tiff", ".wav", ".wbmp", ".webp", ".xbm", ".xcf", ".xpm", ".xwd", ".xz", ".z", ".zip":
		return false
	}
	return true
}

func (s *Server) writeTemplateError(w http.ResponseWriter, err error, m Mapping) {
	var templateError *TemplateExecutionError
	if errors.As(err, &templateError) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Connection", "close")
		if id := bmString(m["id"]); id != "" {
			w.Header().Set("Matched-Stub-Id", id)
		}
		w.WriteHeader(500)
		w.Write([]byte("[ERROR] " + templateError.Error()))
		return
	}
	http.Error(w, err.Error(), 500)
}
