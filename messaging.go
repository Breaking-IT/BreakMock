// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

type MessageChannel struct {
	order      *adminOrder
	ID, Type   string
	Request    Request
	Definition map[string]any
	Send       func(map[string]any) error
	Close      func()
}
type messageMapping struct {
	value    Mapping
	order    *adminOrder
	sequence uint64
}
type MessageStore struct {
	rootDir          string
	mu               sync.RWMutex
	owner            *Store
	mappings         map[string]messageMapping
	mappingEntries   []messageMapping
	channels         map[string]*MessageChannel
	channelOrder     []string
	providers        map[string]Mapping
	providerOrders   map[string]*adminOrder
	providerOrder    []string
	providerCapacity int
	events           []Mapping
	eventOrders      map[string]*adminOrder
	sequence         uint64
	changed          chan struct{}
}

func (s *Store) Messaging() *MessageStore {
	// Publish the store once without contending with request journal writes on
	// every HTTP request. Once also supports concurrent first access.
	s.messagingOnce.Do(func() {
		s.messaging = &MessageStore{owner: s, mappings: map[string]messageMapping{}, channels: map[string]*MessageChannel{}, providers: map[string]Mapping{}, providerOrders: map[string]*adminOrder{}, changed: make(chan struct{})}
	})
	return s.messaging
}
func (s *MessageStore) signal() { close(s.changed); s.changed = make(chan struct{}) }
func (s *MessageStore) Mappings() []Mapping {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.mappingEntries) == 0 {
		return []Mapping{}
	}
	items := append([]messageMapping(nil), s.mappingEntries...)
	sort.Slice(items, func(i, j int) bool {
		pi, pj := bmNumber(items[i].value["priority"], 5), bmNumber(items[j].value["priority"], 5)
		if pi != pj {
			return pi < pj
		}
		return items[i].sequence > items[j].sequence
	})
	out := make([]Mapping, len(items))
	for i, m := range items {
		out[i] = m.value
	}
	return out
}
func (s *MessageStore) Channels() []*MessageChannel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*MessageChannel{}
	seen := map[string]bool{}
	for _, id := range s.channelOrder {
		if seen[id] {
			continue
		}
		seen[id] = true
		if c := s.channels[id]; c != nil {
			out = append(out, c)
		}
	}
	return out
}
func (s *MessageStore) Channel(id string) *MessageChannel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.channels[id]
}
func (s *MessageStore) RegisterChannel(id, typ string, req Request, send func(map[string]any) error, closeFn func()) *MessageChannel {
	if id == "" {
		id = newID()
	}
	tmp := NewStore()
	tmp.Record(req, nil, 101)
	entry := tmp.journalSnapshot()[0]
	logged := cloneMap(entry.event["request"].(map[string]any))
	for k, v := range logged {
		switch x := v.(type) {
		case bool:
			if !x {
				delete(logged, k)
			}
		case int:
			if x == -1 {
				delete(logged, k)
			}
		case float64:
			if x == -1 {
				delete(logged, k)
			}
		case map[string]any:
			if len(x) == 0 {
				delete(logged, k)
			}
		case []any:
			if len(x) == 0 {
				delete(logged, k)
			}
		}
	}
	serializedType := typ
	if typ == "sse" {
		serializedType = "websocket"
	}

	channel := &MessageChannel{order: &adminOrder{children: map[string]*adminOrder{"initiatingRequest": entry.order.child("request")}}, ID: id, Type: typ, Request: req, Definition: map[string]any{"type": serializedType, "id": id, "open": true, "initiatingRequest": logged}, Send: send, Close: closeFn}
	s.mu.Lock()
	s.channels[id] = channel
	s.channelOrder = append(s.channelOrder, id)
	s.signal()
	s.mu.Unlock()
	return channel
}
func (s *MessageStore) UnregisterChannel(id string) {
	s.mu.Lock()
	delete(s.channels, id)
	for i := len(s.channelOrder) - 1; i >= 0; i-- {
		if s.channelOrder[i] == id {
			copy(s.channelOrder[i:], s.channelOrder[i+1:])
			s.channelOrder[len(s.channelOrder)-1] = ""
			s.channelOrder = s.channelOrder[:len(s.channelOrder)-1]
		}
	}
	s.signal()
	s.mu.Unlock()
}
func (s *MessageStore) Record(id, eventType string, message map[string]any, matched Mapping) string {
	s.owner.mu.RLock()
	disabled, limit := s.owner.journalDisabled, s.owner.journalLimit
	s.owner.mu.RUnlock()
	if disabled {
		return ""
	}
	channel := s.Channel(id)
	if channel == nil {
		return ""
	}
	eventID := newID()
	event := Mapping{"id": eventID, "eventType": eventType, "channel": channel.Definition, "message": cloneMap(message), "wasMatched": matched != nil || eventType == "sent", "timestamp": time.Now().UTC().Format(time.RFC3339Nano)}
	if matched != nil {
		event["stubMapping"] = matched
	}
	s.mu.Lock()
	if s.eventOrders == nil {
		s.eventOrders = map[string]*adminOrder{}
	}
	s.eventOrders[eventID] = &adminOrder{children: map[string]*adminOrder{"channel": channel.order}}
	s.events = append(s.events, event)
	if limit > 0 && len(s.events) > limit {
		discard := len(s.events) - limit
		for _, old := range s.events[:discard] {
			delete(s.eventOrders, bmString(old["id"]))
		}
		clear(s.events[:discard])
		s.events = s.events[discard:]
	}
	s.signal()
	s.mu.Unlock()
	return eventID
}
func (s *MessageStore) Events() []Mapping {
	if s.owner.JournalDisabled() {
		return []Mapping{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Mapping, len(s.events))
	for i, e := range s.events {
		out[len(out)-i-1] = e
	}
	return out
}
func (s *MessageStore) Reset() {
	s.mu.Lock()
	s.mappings = map[string]messageMapping{}
	s.mappingEntries = nil
	s.events = nil
	s.eventOrders = nil
	s.signal()
	s.mu.Unlock()
}
func (s *MessageStore) AddMapping(m Mapping, order *adminOrder) Mapping {
	return s.addMapping(m, order, false)
}
func (s *MessageStore) ReplaceMapping(m Mapping, order *adminOrder) Mapping {
	return s.addMapping(m, order, true)
}
func (s *MessageStore) addMapping(m Mapping, order *adminOrder, replace bool) Mapping {
	m = cloneMap(m)
	normalizeMessagePatterns(m)
	id, _ := m["id"].(string)
	if id == "" {
		id = newID()
	}
	m["id"] = id
	delete(m, "uuid")
	delete(m, "$schema")
	delete(m, "insertionIndex")
	if m["trigger"] == nil {
		m["trigger"] = map[string]any{"type": "message"}
	}
	for _, key := range []string{"actions", "metadata", "name"} {
		switch v := m[key].(type) {
		case []any:
			if len(v) == 0 {
				delete(m, key)
			}
		case map[string]any:
			if len(v) == 0 {
				delete(m, key)
			}
		case string:
			if v == "" {
				delete(m, key)
			}
		}
	}
	if actions, ok := m["actions"].([]any); ok {
		for _, a := range actions {
			if action, ok := a.(map[string]any); ok {
				if action["type"] == "send" && action["channelTarget"] == nil {
					action["channelTarget"] = map[string]any{"type": "originating"}
				}
			}
		}
	}
	s.mu.Lock()
	s.sequence++
	entry := messageMapping{m, order, s.sequence}
	if replace {
		best := -1
		for i, old := range s.mappingEntries {
			if old.value["id"] != id {
				continue
			}
			if best < 0 || bmNumber(old.value["priority"], 5) < bmNumber(s.mappingEntries[best].value["priority"], 5) || bmNumber(old.value["priority"], 5) == bmNumber(s.mappingEntries[best].value["priority"], 5) && old.sequence > s.mappingEntries[best].sequence {
				best = i
			}
		}
		if best >= 0 {
			entry.sequence = 0
			s.mappingEntries[best] = entry
		}
	} else {
		s.mappingEntries = append(s.mappingEntries, entry)
	}
	s.mappings[id] = entry
	s.signal()
	s.mu.Unlock()
	return m
}
func (s *MessageStore) mappingValue(m Mapping) any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.mappingEntries {
		if reflect.DeepEqual(entry.value, m) {
			return adminOrderedValue{m, entry.order}
		}
	}
	return adminOrderedValue{m, s.mappings[bmString(m["id"])].order}
}
func messageEventMatches(pattern Mapping, event Mapping) bool {
	if p, ok := pattern["channel"].(map[string]any); ok {
		channel, _ := event["channel"].(map[string]any)
		logged, _ := channel["initiatingRequest"].(map[string]any)
		req := Request{Method: bmString(logged["method"]), URL: bmString(logged["url"]), AbsoluteURL: bmString(logged["absoluteUrl"])}
		if !MatchRequest(p, req) {
			return false
		}
	}
	if p, ok := pattern["body"].(map[string]any); ok {
		message, _ := event["message"].(map[string]any)
		body, ok := message["body"].(string)
		if !ok {
			bytes, _ := base64.StdEncoding.DecodeString(bmString(message["base64Body"]))
			body = string(bytes)
		}
		if !matchValue(p, body, true) {
			return false
		}
	}
	return true
}

func (s *MessageStore) LoadMappings(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) && path == root {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		doc, err := DecodeDocument(raw)
		if err != nil {
			return fmt.Errorf("message mapping %s: %w", path, err)
		}
		raws := []json.RawMessage{raw}
		if doc["messageMappings"] != nil {
			var collection struct {
				Mappings []json.RawMessage `json:"messageMappings"`
			}
			if err = json.Unmarshal(raw, &collection); err != nil {
				return err
			}
			raws = collection.Mappings
		}
		for _, item := range raws {
			m, err := DecodeDocument(item)
			if err != nil {
				return err
			}
			order, err := decodeAdminOrder(item)
			if err != nil {
				return err
			}
			s.AddMapping(m, order)
		}
		return nil
	})
}

func normalizeMessagePatterns(v any) {
	switch x := v.(type) {
	case map[string]any:
		for key, item := range x {
			if key == "metadata" || key == "transformerParameters" || key == "data" {
				continue
			}
			if key == "requestPattern" || key == "initiatingRequestPattern" {
				if pattern, ok := item.(map[string]any); ok && pattern["method"] == nil {
					pattern["method"] = "ANY"
				}
			}
			normalizeMessagePatterns(item)
		}
	case []any:
		for _, item := range x {
			normalizeMessagePatterns(item)
		}
	}
}

func (s *MessageStore) RemoveMapping(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.mappings, id)
	keep := s.mappingEntries[:0]
	for _, entry := range s.mappingEntries {
		if entry.value["id"] != id {
			keep = append(keep, entry)
		}
	}
	clear(s.mappingEntries[len(keep):])
	s.mappingEntries = keep
}
