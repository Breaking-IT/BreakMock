// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"io"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (a *adminHandler) serveMessaging(w http.ResponseWriter, r *http.Request) bool {
	p := strings.TrimPrefix(r.URL.Path, "/__admin")
	if !(strings.HasPrefix(p, "/message-mappings") || strings.HasPrefix(p, "/messages") || strings.HasPrefix(p, "/channels") || strings.HasPrefix(p, "/channel-providers")) {
		return false
	}
	s := a.store.Messaging()
	write := func(v any) {
		if v == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte("null"))
			return
		}
		a.writeJSON(w, 200, v)
	}
	empty := func() { write(map[string]any{}) }
	decode := func() (Mapping, bool) { return adminDecode(w, r) }
	if p == "/message-mappings" {
		switch r.Method {
		case "GET":
			items := []any{}
			for _, m := range s.Mappings() {
				items = append(items, s.mappingValue(m))
			}
			write(map[string]any{"messageMappings": items, "meta": map[string]any{"total": len(items)}})
		case "POST":
			m, ok := decode()
			if !ok {
				return true
			}
			if !validateMessageMapping(w, m) {
				return true
			}
			raw, _ := io.ReadAll(r.Body)
			order, _ := decodeAdminOrder(raw)
			added := s.AddMapping(m, order)
			a.writeJSON(w, 201, s.mappingValue(added))
		case "DELETE":
			s.mu.Lock()
			s.mappings = map[string]messageMapping{}
			s.mappingEntries = nil
			s.mu.Unlock()
			empty()
		default:
			w.WriteHeader(404)
		}
		return true
	}
	if p == "/message-mappings/find-by-metadata" || p == "/message-mappings/remove-by-metadata" {
		pattern, ok := decode()
		if !ok {
			return true
		}
		items := []any{}
		for _, m := range s.Mappings() {
			if matchMetadata(pattern, m["metadata"]) {
				items = append(items, s.mappingValue(m))
				if strings.Contains(p, "remove-") {
					s.RemoveMapping(bmString(m["id"]))
				}
			}
		}
		if strings.Contains(p, "remove-") {
			empty()
		} else {
			write(map[string]any{"messageMappings": items, "meta": map[string]any{"total": len(items)}})
		}
		return true
	}
	if strings.HasPrefix(p, "/message-mappings/") {
		id := strings.TrimPrefix(p, "/message-mappings/")
		s.mu.RLock()
		m, exists := s.mappings[id]
		s.mu.RUnlock()
		for _, candidate := range s.Mappings() {
			if candidate["id"] == id {
				m.value = candidate
				break
			}
		}
		switch r.Method {
		case "GET":
			if !exists {
				w.WriteHeader(404)
			} else {
				write(s.mappingValue(m.value))
			}
		case "DELETE":
			s.RemoveMapping(id)
			empty()
		case "PUT":
			if !exists {
				w.WriteHeader(404)
				break
			}
			v, ok := decode()
			if !ok {
				return true
			}
			v["id"] = id
			if !validateMessageMapping(w, v) {
				return true
			}
			raw, _ := io.ReadAll(r.Body)
			order, _ := decodeAdminOrder(raw)
			write(s.mappingValue(s.ReplaceMapping(v, order)))
		default:
			w.WriteHeader(404)
		}
		return true
	}
	if strings.HasPrefix(p, "/channel-providers") {
		name := strings.TrimPrefix(p, "/channel-providers/")
		if p == "/channel-providers" && r.Method == "GET" {
			s.mu.RLock()
			items := []Mapping{}
			seen := map[string]bool{}
			keys := append([]string(nil), s.providerOrder...)
			capacity := s.providerCapacity
			if capacity == 0 {
				capacity = 16
			}
			sort.SliceStable(keys, func(i, j int) bool {
				return javaStringHash(keys[i])&uint32(capacity-1) < javaStringHash(keys[j])&uint32(capacity-1)
			})
			for _, key := range keys {
				if seen[key] {
					continue
				}
				seen[key] = true
				if m := s.providers[key]; m != nil {
					items = append(items, m)
				}
			}
			s.mu.RUnlock()
			write(map[string]any{"channelProviders": items, "meta": map[string]any{"total": len(items)}})
			return true
		}
		if r.Method == "POST" || r.Method == "PUT" {
			m, ok := decode()
			if !ok {
				return true
			}
			if !requiredMessageFields(w, m, "name", "driverType") {
				return true
			}
			if r.Method == "PUT" {
				s.mu.RLock()
				old := s.providers[name]
				s.mu.RUnlock()
				if old == nil {
					w.WriteHeader(404)
					return true
				}
				if old["driverType"] != m["driverType"] {
					adminProblem(w, 422, 10, "driverType cannot be changed; use delete and re-register", "", "driverType")
					return true
				}
				settings := m["settings"]
				if settings == nil {
					settings = map[string]any{}
				}
				if !reflect.DeepEqual(old["settings"], settings) {
					adminProblem(w, 422, 10, "settings cannot be changed; use delete and re-register", "", "settings")
					return true
				}
				s.mu.Lock()
				s.deleteProviderLocked(name)
				s.mu.Unlock()
			}
			if m["driverType"] != "in-memory" {
				adminProblem(w, 422, 10, "No driver registered for type: "+bmString(m["driverType"]), "", "")
				return true
			}
			name = bmString(m["name"])
			if m["settings"] == nil {
				m["settings"] = map[string]any{}
			}
			s.mu.Lock()
			_, exists := s.providers[name]
			if !exists {
				s.providerOrder = append(s.providerOrder, name)
			}
			s.providers[name] = cloneMap(m)
			if s.providerCapacity == 0 {
				s.providerCapacity = 16
			}
			for len(s.providers) > s.providerCapacity*3/4 {
				s.providerCapacity *= 2
			}
			raw, _ := io.ReadAll(r.Body)
			s.providerOrders[name], _ = decodeAdminOrder(raw)
			s.mu.Unlock()
			status := 201
			if r.Method == "PUT" {
				status = 200
			}
			a.writeJSON(w, status, m)
			return true
		}
		s.mu.RLock()
		m := s.providers[name]
		s.mu.RUnlock()
		if r.Method == "DELETE" {
			s.mu.Lock()
			s.deleteProviderLocked(name)
			s.mu.Unlock()
			empty()
		} else if m == nil {
			w.WriteHeader(404)
		} else {
			write(m)
		}
		return true
	}
	if p == "/channels/send" && r.Method == "POST" {
		v, ok := decode()
		if !ok {
			return true
		}
		if !requiredMessageFields(w, v, "message") {
			return true
		}
		if typ, ok := v["type"].(string); ok {
			v["type"] = strings.ToLower(typ)
		}
		message, _ := v["message"].(map[string]any)
		if message == nil {
			adminProblem(w, 422, 10, "Message must not be null", "", "")
			return true
		}
		resolved, err := s.resolveMessageDefinition(message, map[string]any{}, nil, nil)
		if err != nil {
			adminError(w, 422, err.Error())
			return true
		}
		delete(resolved, "headers")
		message = resolved
		selected := []any{}
		for _, c := range s.Channels() {
			if v["type"] != nil && c.Type != v["type"] {
				continue
			}
			if c.Type == "fixed" {
				if c.Definition["providerName"] != v["providerName"] || c.Definition["channelName"] != v["channelName"] {
					continue
				}
				if err := s.Incoming(c.ID, message); err != nil {
					if strings.HasPrefix(err.Error(), "com.github.tomakehurst.wiremock.admin.NotFoundException:") {
						w.WriteHeader(http.StatusNotFound)
					} else {
						writeJettyError(w, r, 500, err.Error())
					}
					return true
				}
			} else {
				if pattern, ok := v["initiatingRequest"].(map[string]any); ok && !MatchRequest(pattern, c.Request) {
					continue
				}
				if c.Send != nil {
					if err := c.Send(message); err != nil {
						continue
					}
				}
				s.Record(c.ID, "sent", message, nil)
			}
			selected = append(selected, c.Definition)
		}
		if v["type"] == "fixed" {
			w.WriteHeader(200)
		} else {
			write(map[string]any{"channels": selected, "channelsMessaged": len(selected)})
		}
		return true
	}
	if p == "/channels" && r.Method == "POST" {
		v, ok := decode()
		if !ok {
			return true
		}
		if !requiredMessageFields(w, v, "providerName", "channelName") {
			return true
		}
		name := bmString(v["providerName"])
		s.mu.RLock()
		provider := s.providers[name]
		s.mu.RUnlock()
		if provider == nil {
			adminProblem(w, 422, 10, "No channel provider registered with name: "+name, "", "")
			return true
		}
		id := newID()
		def := Mapping{"type": "fixed", "id": id, "open": true, "providerName": name, "channelName": v["channelName"]}
		channel := &MessageChannel{ID: id, Type: "fixed", Definition: def}
		s.mu.Lock()
		for _, existing := range s.channels {
			if existing.Type == "fixed" && existing.Definition["providerName"] == name && existing.Definition["channelName"] == v["channelName"] {
				s.mu.Unlock()
				adminProblem(w, 409, 409, "Channel "+bmString(v["channelName"])+" already exists via provider "+name, "", "")
				return true
			}
		}
		s.channels[id] = channel
		s.channelOrder = append(s.channelOrder, id)
		s.signal()
		s.mu.Unlock()
		a.writeJSON(w, 201, def)
		return true
	}
	if p == "/channels" && r.Method == "GET" {
		items := []any{}
		for _, c := range s.Channels() {
			items = append(items, c.Definition)
		}
		write(map[string]any{"channels": items, "meta": map[string]any{"total": len(items)}})
		return true
	}
	if strings.HasPrefix(p, "/channels/") {
		id := strings.TrimPrefix(p, "/channels/")
		c := s.Channel(id)
		if r.Method == "DELETE" {
			if c != nil && c.Close != nil {
				c.Close()
			}
			s.UnregisterChannel(id)
			empty()
		} else if c == nil {
			w.WriteHeader(404)
		} else {
			write(c.Definition)
		}
		return true
	}
	if p == "/messages" {
		if r.Method == "DELETE" {
			s.settle()
			s.mu.Lock()
			s.events = nil
			s.eventOrders = nil
			s.signal()
			s.mu.Unlock()
			empty()
		} else {
			write(map[string]any{"messageServeEvents": s.Events(), "messageJournalDisabled": a.store.JournalDisabled()})
		}
		return true
	}
	if p == "/messages/count" || p == "/messages/find" || p == "/messages/remove" || p == "/messages/remove-by-metadata" || p == "/messages/wait" || p == "/messages/wait-for-count" {
		pattern, ok := decode()
		if !ok {
			return true
		}
		request := pattern
		waiting := strings.Contains(p, "/wait")
		if waiting {
			pattern, _ = request["pattern"].(map[string]any)
		}
		find := func() []Mapping {
			out := []Mapping{}
			for _, e := range s.Events() {
				matched := messageEventMatches(pattern, e)
				if p == "/messages/remove-by-metadata" {
					mapping, _ := e["stubMapping"].(map[string]any)
					matched = matchMetadata(pattern, mapping["metadata"])
				}
				if matched {
					out = append(out, e)
				}
			}
			return out
		}
		found := find()
		if waiting {
			if a.store.JournalDisabled() {
				writeJettyError(w, r, 500, "com.github.tomakehurst.wiremock.verification.MessageJournalDisabledException: The message journal is disabled, so no verification or message searching operations are available")
				return true
			}
			count := 1
			if p == "/messages/wait-for-count" {
				count = int(bmNumber(request["count"], 1))
			}
			timeout := time.Duration(bmNumber(request["timeoutMillis"], 0)) * time.Millisecond
			if count < 0 {
				message := strconv.Itoa(count)
				if timeout > 0 {
					message = "fromIndex(0) > toIndex(" + message + ")"
				}
				writeJettyError(w, r, 500, "java.lang.IllegalArgumentException: "+message)
				return true
			}
			deadline := time.NewTimer(timeout)
			defer deadline.Stop()
		wait:
			for len(found) < count {
				s.mu.RLock()
				changed := s.changed
				s.mu.RUnlock()
				found = find()
				if len(found) >= count {
					break
				}
				select {
				case <-changed:
					found = find()
				case <-deadline.C:
					found = find()
					break wait
				case <-r.Context().Done():
					return true
				}
			}
			if len(found) > count {
				found = found[:count]
			}
		}
		switch p {
		case "/messages/count":
			write(map[string]any{"count": len(found), "messageJournalDisabled": a.store.JournalDisabled()})
		case "/messages/wait":
			if len(found) > 0 {
				write(found[0])
			} else {
				write(nil)
			}
		case "/messages/wait-for-count":
			write(map[string]any{"messageServeEvents": found, "messageJournalDisabled": a.store.JournalDisabled()})
		default:
			if strings.Contains(p, "/remove") {
				ids := map[string]bool{}
				for _, e := range found {
					ids[bmString(e["id"])] = true
				}
				s.mu.Lock()
				keep := []Mapping{}
				for _, e := range s.events {
					if !ids[bmString(e["id"])] {
						keep = append(keep, e)
					}
				}
				s.events = keep
				retained := map[string]bool{}
				for _, e := range keep {
					retained[bmString(e["id"])] = true
				}
				for id := range s.eventOrders {
					if !retained[id] {
						delete(s.eventOrders, id)
					}
				}
				s.signal()
				s.mu.Unlock()
			}
			write(map[string]any{"messageServeEvents": found})
		}
		return true
	}
	if strings.HasPrefix(p, "/messages/") {
		id := strings.TrimPrefix(p, "/messages/")
		var item Mapping
		for _, e := range s.Events() {
			if e["id"] == id {
				item = e
				break
			}
		}
		if r.Method == "DELETE" {
			s.mu.Lock()
			keep := []Mapping{}
			for _, e := range s.events {
				if e["id"] != id {
					keep = append(keep, e)
				}
			}
			s.events = keep
			retained := map[string]bool{}
			for _, e := range keep {
				retained[bmString(e["id"])] = true
			}
			for id := range s.eventOrders {
				if !retained[id] {
					delete(s.eventOrders, id)
				}
			}
			s.signal()
			s.mu.Unlock()
			empty()
		} else if item == nil {
			write(nil)
		} else {
			write(item)
		}
		return true
	}
	w.WriteHeader(404)
	return true
}
func matchMetadata(pattern Mapping, value any) bool {
	data, _ := marshalAdminJSON(value)
	return matchValue(pattern, string(data), value != nil)
}

func (s *MessageStore) deleteProviderLocked(name string) {
	delete(s.providers, name)
	delete(s.providerOrders, name)
	for i := len(s.providerOrder) - 1; i >= 0; i-- {
		if s.providerOrder[i] == name {
			copy(s.providerOrder[i:], s.providerOrder[i+1:])
			s.providerOrder[len(s.providerOrder)-1] = ""
			s.providerOrder = s.providerOrder[:len(s.providerOrder)-1]
		}
	}
}
