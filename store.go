// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type storedMapping struct {
	mapping  Mapping
	sequence uint64
	order    *adminOrder
}
type journalEntry struct {
	request Request
	event   map[string]any
	order   *adminOrder
}

// Store keeps immutable mappings and a request journal protected against concurrent access.
type Store struct {
	mu              sync.RWMutex
	mappings        map[string]storedMapping
	scenarios       map[string]string
	journal         []journalEntry
	settings        map[string]any
	sequence        uint64
	sorted          []Mapping
	journalDisabled bool
	journalLimit    int
}

func NewStore() *Store {
	return &Store{mappings: map[string]storedMapping{}, scenarios: map[string]string{}, settings: map[string]any{"proxyPassThrough": true}}
}
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	b, _ := marshalJSON(m)
	c, _ := DecodeDocument(b)
	return c
}
func (s *Store) AddMapping(m Mapping) (Mapping, error) { return s.addMapping(m, nil, false) }
func (s *Store) addMapping(m Mapping, sourceOrder *adminOrder, replaceOrder bool) (Mapping, error) {
	if _, ok := m["request"].(map[string]any); !ok {
		return nil, fmt.Errorf("request must be an object")
	}
	if _, ok := m["response"].(map[string]any); !ok {
		return nil, fmt.Errorf("response must be an object")
	}
	c := cloneMap(m)
	if req, ok := c["request"].(map[string]any); ok {
		if req["method"] == nil {
			req["method"] = "ANY"
		}
	}
	id, _ := c["id"].(string)
	if id == "" {
		id, _ = c["uuid"].(string)
	}
	if id == "" {
		id = newID()
	}
	c["id"] = id
	c["uuid"] = id
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, exists := s.mappings[id]
	sequence := existing.sequence
	if !exists {
		s.sequence++
		sequence = s.sequence
	}
	order := existing.order
	if replaceOrder {
		order = sourceOrder
	}
	s.mappings[id] = storedMapping{mapping: c, sequence: sequence, order: order}
	s.rebuildMappings()
	if name, _ := c["scenarioName"].(string); name != "" {
		if _, ok := s.scenarios[name]; !ok {
			s.scenarios[name] = "Started"
		}
	}
	return cloneMap(c), nil
}
func mappingPriority(m Mapping) float64 {
	if n, ok := m["priority"].(float64); ok {
		return n
	}
	return 5
}
func (s *Store) Mappings() []Mapping {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.sorted == nil {
		return []Mapping{}
	}
	return s.sorted
}
func (s *Store) rebuildMappings() {
	v := make([]storedMapping, 0, len(s.mappings))
	for _, m := range s.mappings {
		v = append(v, m)
	}
	sort.Slice(v, func(i, j int) bool {
		a, b := mappingPriority(v[i].mapping), mappingPriority(v[j].mapping)
		if a == b {
			return v[i].sequence > v[j].sequence
		}
		return a < b
	})
	out := make([]Mapping, 0, len(v))
	for _, m := range v {
		out = append(out, m.mapping)
	}
	s.sorted = out
}
func (s *Store) GetMapping(id string) Mapping {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMap(s.mappings[id].mapping)
}
func (s *Store) DeleteMapping(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.mappings[id]
	delete(s.mappings, id)
	s.rebuildMappings()
	return ok
}
func (s *Store) ResetMappings() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mappings = map[string]storedMapping{}
	s.sorted = nil
	s.scenarios = map[string]string{}
}
func (s *Store) ScenarioState(name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.scenarios[name]; ok {
		return v
	}
	return "Started"
}
func (s *Store) SetScenarioState(name, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scenarios[name] = state
}
func (s *Store) ResetScenarios() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.scenarios {
		s.scenarios[k] = "Started"
	}
}
func (s *Store) Settings() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMap(s.settings)
}
func (s *Store) SetSettings(v map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = cloneMap(v)
	if s.settings["proxyPassThrough"] == nil {
		s.settings["proxyPassThrough"] = false
	}
}
func (s *Store) ResetRequests()                          { s.mu.Lock(); defer s.mu.Unlock(); s.journal = nil }
func (s *Store) Record(r Request, m Mapping, status int) { s.RecordServed(r, m, status, nil, nil) }
func (s *Store) RecordServed(r Request, m Mapping, status int, responseHeaders http.Header, responseBody []byte, responseHeaderOrder ...[]string) {
	s.mu.RLock()
	disabled := s.journalDisabled
	s.mu.RUnlock()
	if disabled {
		return
	}
	now := time.Now()
	id := newID()
	headers := map[string]any{}
	for k, v := range r.Headers {
		if len(v) == 1 {
			headers[k] = v[0]
		} else {
			headers[k] = append([]string(nil), v...)
		}
	}
	restoreHeaderCasing(headers, r.HeaderOrder)
	queryOrder := []string{}
	cookieOrder := []string{}
	formOrder := []string{}
	req := map[string]any{"url": r.URL, "absoluteUrl": r.AbsoluteURL, "method": r.Method, "headers": headers, "body": string(r.Body), "bodyAsBase64": base64.StdEncoding.EncodeToString(r.Body), "loggedDate": now.UnixMilli(), "loggedDateString": now.UTC().Format("2006-01-02T15:04:05.000Z"), "cookies": map[string]any{}, "queryParams": map[string]any{}}
	req["clientIp"] = r.ClientIP
	req["browserProxyRequest"] = false
	req["protocol"] = "HTTP/1.1"
	req["formParams"] = map[string]any{}
	if u, err := url.Parse(r.AbsoluteURL); err == nil {
		req["scheme"] = u.Scheme
		req["host"] = u.Hostname()
		port := 80
		if u.Scheme == "https" {
			port = 443
		}
		if v, err := strconv.Atoi(u.Port()); err == nil {
			port = v
		}
		req["port"] = port
	}
	if u, err := url.Parse(r.URL); err == nil {
		q := map[string]any{}
		for k, v := range u.Query() {
			q[k] = map[string]any{"key": k, "values": v}
		}
		req["queryParams"] = q
		queryOrder = queryKeys(u.RawQuery)
	}
	cookieValues := map[string]any{}
	hr := http.Request{Header: r.Headers}
	for _, c := range hr.Cookies() {
		cookieValues[c.Name] = c.Value
		cookieOrder = append(cookieOrder, c.Name)
	}
	req["cookies"] = cookieValues
	if strings.HasPrefix(r.Headers.Get("Content-Type"), "application/x-www-form-urlencoded") {
		values, _ := url.ParseQuery(string(r.Body))
		form := map[string]any{}
		for k, v := range values {
			form[k] = map[string]any{"key": k, "values": v}
		}
		req["formParams"] = form
		formOrder = queryKeys(string(r.Body))
	}
	event := map[string]any{"id": id, "request": req, "wasMatched": m != nil, "responseDefinition": map[string]any{"status": status}, "timing": map[string]any{"addedDelay": 0, "processTime": 0, "responseSendTime": 0, "serveTime": 0, "totalTime": 0}, "subEvents": []any{}}
	if m != nil {
		event["stubMapping"] = m
		event["responseDefinition"] = m["response"]
	}
	response := map[string]any{"status": status, "body": string(responseBody), "bodyAsBase64": base64.StdEncoding.EncodeToString(responseBody)}
	if len(responseHeaders) > 0 {
		h := map[string]any{}
		for k, v := range responseHeaders {
			if len(v) == 0 {
				continue
			}
			if len(v) == 1 {
				h[k] = v[0]
			} else {
				h[k] = append([]string(nil), v...)
			}
		}
		response["headers"] = h
	}
	event["response"] = response
	r.Body = append([]byte(nil), r.Body...)
	r.Headers = r.Headers.Clone()
	r.HeaderOrder = append([]string(nil), r.HeaderOrder...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.journalDisabled {
		return
	}
	order := &adminOrder{children: map[string]*adminOrder{}}
	order.children["request"] = &adminOrder{children: map[string]*adminOrder{"headers": {keys: append([]string(nil), r.HeaderOrder...)}, "queryParams": {keys: queryOrder}, "cookies": {keys: cookieOrder}, "formParams": {keys: formOrder}}}
	if m != nil {
		order.children["responseDefinition"] = s.mappings[bmString(m["id"])].order.child("response")
	}
	responseOrder := []string{}
	if len(responseHeaderOrder) > 0 && len(responseHeaderOrder[0]) > 0 {
		responseOrder = append(responseOrder, responseHeaderOrder[0]...)
	} else if m != nil {
		if source := s.mappings[bmString(m["id"])].order; source != nil {
			if headers := source.child("response").child("headers"); headers != nil {
				responseOrder = append(responseOrder, headers.keys...)
			}
		}
	}
	responseOrder = append(responseOrder, "Matched-Stub-Id", "Matched-Stub-Name")
	if h, ok := response["headers"].(map[string]any); ok {
		restoreHeaderCasing(h, responseOrder)
	}
	order.children["response"] = &adminOrder{children: map[string]*adminOrder{"headers": {keys: responseOrder}}}
	s.journal = append(s.journal, journalEntry{request: r, event: event, order: order})
	if s.journalLimit > 0 && len(s.journal) > s.journalLimit {
		discard := len(s.journal) - s.journalLimit
		clear(s.journal[:discard])
		s.journal = s.journal[discard:]
	}
}
func (s *Store) journalSnapshot() []journalEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := make([]journalEntry, len(s.journal))
	for i, e := range s.journal {
		v[len(v)-i-1] = e
	}
	return v
}
func (s *Store) removeEvents(ids map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := s.journal[:0]
	for _, e := range s.journal {
		if !ids[e.event["id"].(string)] {
			keep = append(keep, e)
		}
	}
	clear(s.journal[len(keep):])
	s.journal = keep
}

func (s *Store) ConfigureJournal(enabled bool, maxEntries int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.journalDisabled = !enabled
	s.journalLimit = maxEntries
	if !enabled {
		s.journal = nil
	}
}
func (s *Store) JournalDisabled() bool { s.mu.RLock(); defer s.mu.RUnlock(); return s.journalDisabled }
func (s *Store) Reset()                { s.ResetMappings(); s.ResetRequests() }

// SettingsSnapshot returns the current immutable settings. Callers must not mutate it.
func (s *Store) SettingsSnapshot() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// AddMappingDocument retains caller-defined JSON map member order for admin output.
func (s *Store) AddMappingDocument(raw []byte) (Mapping, error) {
	m, err := DecodeDocument(raw)
	if err != nil {
		return nil, err
	}
	order, err := decodeAdminOrder(raw)
	if err != nil {
		return nil, err
	}
	return s.addMapping(m, order, true)
}

func (s *Store) SetMappingOrder(id string, raw []byte) {
	order, err := decodeAdminOrder(raw)
	if err != nil {
		return
	}
	s.setMappingOrder(id, order)
}
func (s *Store) setMappingOrder(id string, order *adminOrder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.mappings[id]
	if ok {
		m.order = order
		s.mappings[id] = m
	}
}
func (s *Store) mappingOrder(id string) *adminOrder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mappings[id].order
}

func restoreHeaderCasing(headers map[string]any, order []string) {
	seen := map[string]bool{}
	for _, name := range order {
		lower := strings.ToLower(name)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		for key, value := range headers {
			if strings.EqualFold(key, name) && key != name {
				delete(headers, key)
				headers[name] = value
				break
			}
		}
	}
}
func queryKeys(raw string) []string {
	keys := []string{}
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, "&") {
		name, _, _ := strings.Cut(part, "=")
		key, err := url.QueryUnescape(name)
		if err == nil && key != "" && !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	return keys
}
