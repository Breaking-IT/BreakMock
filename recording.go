// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func (a *adminHandler) recordings(w http.ResponseWriter, r *http.Request, p string) {
	a.recordMu.Lock()
	defer a.recordMu.Unlock()
	if p == "/recordings/status" && r.Method == "GET" {
		a.writeJSON(w, 200, map[string]any{"status": a.recordStatus})
		return
	}
	if r.Method != "POST" {
		adminError(w, 405, "Method not allowed")
		return
	}
	switch p {
	case "/recordings/start":
		data, err := io.ReadAll(r.Body)
		if err != nil {
			adminError(w, 422, err.Error())
			return
		}
		spec := map[string]any{}
		if len(data) > 0 {
			if err = json.Unmarshal(data, &spec); err != nil {
				adminError(w, 422, err.Error())
				return
			}
		}
		if err = validateRecordSpec(spec); err != nil {
			adminError(w, 501, err.Error())
			return
		}
		if a.recordStatus == "Recording" {
			a.writeJSON(w, 200, map[string]any{})
			return
		}
		a.recordSpec = spec
		a.recordStartID = ""
		entries := a.store.journalSnapshot()
		if len(entries) > 0 {
			a.recordStartID = entries[0].event["id"].(string)
		}
		if target, _ := spec["targetBaseUrl"].(string); target != "" {
			m, _ := a.store.AddMapping(Mapping{"request": map[string]any{"urlPattern": ".*", "method": "ANY"}, "response": map[string]any{"proxyBaseUrl": target}})
			a.recordProxyID = m["id"].(string)
		}
		a.recordStatus = "Recording"
		a.writeJSON(w, 200, map[string]any{})
	case "/recordings/stop":
		if a.recordStatus != "Recording" {
			a.writeJSON(w, 400, map[string]any{"errors": []any{map[string]any{"code": 30, "title": "Not currently recording."}}})
			return
		}
		a.recordStatus = "Stopped"
		if a.recordProxyID != "" {
			a.store.DeleteMapping(a.recordProxyID)
			a.recordProxyID = ""
		}
		entries := a.store.journalSnapshot()
		selected := []journalEntry{}
		for _, e := range entries {
			if e.event["id"] == a.recordStartID {
				break
			}
			selected = append(selected, e)
		}
		v, err := a.snapshot(selected, a.recordSpec)
		if err != nil {
			adminError(w, 500, err.Error())
			return
		}
		a.writeJSON(w, 200, v)
	case "/recordings/snapshot":
		data, err := io.ReadAll(r.Body)
		if err != nil {
			adminError(w, 422, err.Error())
			return
		}
		spec := map[string]any{}
		if len(data) > 0 {
			if err = json.Unmarshal(data, &spec); err != nil {
				adminError(w, 422, err.Error())
				return
			}
		}
		if err = validateRecordSpec(spec); err != nil {
			adminError(w, 501, err.Error())
			return
		}
		v, err := a.snapshot(a.store.journalSnapshot(), spec)
		if err != nil {
			adminError(w, 500, err.Error())
			return
		}
		a.writeJSON(w, 200, v)
	default:
		adminError(w, 404, "Not found")
	}
}
func validateRecordSpec(spec map[string]any) error {
	for _, k := range []string{"transformers", "extractBodyCriteria"} {
		if spec[k] != nil {
			return fmt.Errorf("recording option %s is not implemented", k)
		}
	}
	return nil
}
func (a *adminHandler) snapshot(entries []journalEntry, spec map[string]any) (map[string]any, error) {
	filters, _ := spec["filters"].(map[string]any)
	pattern := cloneMap(filters)
	delete(pattern, "ids")
	delete(pattern, "allowNonProxied")
	capture, _ := spec["captureHeaders"].(map[string]any)
	bodyOptions, _ := spec["requestBodyPattern"].(map[string]any)
	mappings := []Mapping{}
	ids := []string{}
	seen := map[string]bool{}
	for _, entry := range entries {
		definition, _ := entry.event["responseDefinition"].(map[string]any)
		if definition["proxyBaseUrl"] == nil && filters["allowNonProxied"] != true {
			continue
		}
		if len(pattern) > 0 && !MatchRequest(pattern, entry.request) {
			continue
		}
		if allowed, ok := filters["ids"].([]any); ok {
			found := false
			for _, id := range allowed {
				if id == entry.event["id"] {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		req := map[string]any{"method": entry.request.Method, "url": entry.request.URL}
		u, _ := url.Parse(entry.request.URL)
		if u != nil && len(u.Query()) > 0 {
			delete(req, "url")
			req["urlPath"] = u.Path
			q := map[string]any{}
			for k, v := range u.Query() {
				exact := []any{}
				for _, value := range v {
					exact = append(exact, map[string]any{"equalTo": value})
				}
				q[k] = map[string]any{"hasExactly": exact}
			}
			req["queryParameters"] = q
		}
		if len(capture) > 0 {
			h := map[string]any{}
			for name, opt := range capture {
				if value := entry.request.Headers.Get(name); value != "" {
					p := map[string]any{"equalTo": value}
					if o, ok := opt.(map[string]any); ok && o["caseInsensitive"] != nil {
						p["caseInsensitive"] = o["caseInsensitive"]
					}
					h[name] = p
				}
			}
			if len(h) > 0 {
				req["headers"] = h
			}
		}
		if len(entry.request.Body) > 0 {
			ct := entry.request.Headers.Get("Content-Type")
			kind, _ := bodyOptions["matcher"].(string)
			if kind == "" || kind == "auto" {
				switch {
				case strings.Contains(ct, "json"):
					kind = "equalToJson"
				case strings.Contains(ct, "xml"):
					kind = "equalToXml"
				case strings.Contains(ct, "multipart/form-data"):
					kind = "anything"
				case ct != "" && !strings.HasPrefix(ct, "text/") && !strings.Contains(ct, "x-www-form-urlencoded"):
					kind = "binaryEqualTo"
				default:
					kind = "equalTo"
				}
			}
			bp := map[string]any{kind: string(entry.request.Body)}
			switch kind {
			case "equalToJson":
				bp["ignoreArrayOrder"] = true
				bp["ignoreExtraElements"] = true
				for _, key := range []string{"ignoreArrayOrder", "ignoreExtraElements"} {
					if v, ok := bodyOptions[key]; ok {
						bp[key] = v
					}
				}
			case "equalTo":
				bp["caseInsensitive"] = false
				if v, ok := bodyOptions["caseInsensitive"]; ok {
					bp["caseInsensitive"] = v
				}
			case "binaryEqualTo":
				bp[kind] = base64.StdEncoding.EncodeToString(entry.request.Body)
			case "anything":
				bp[kind] = true
			}
			req["bodyPatterns"] = []any{bp}
		}
		logged, _ := entry.event["response"].(map[string]any)
		response := map[string]any{"status": logged["status"]}
		if response["status"] == nil {
			response["status"] = 200
		}
		headers, _ := logged["headers"].(map[string]any)
		rh := map[string]any{}
		ct := ""
		for k, v := range headers {
			if strings.EqualFold(k, "Content-Type") {
				ct = fmt.Sprint(v)
			}
			switch strings.ToLower(k) {
			case "content-encoding", "content-length", "transfer-encoding":
				continue
			}
			rh[k] = v
		}
		if len(rh) > 0 {
			response["headers"] = rh
		}
		body, _ := logged["body"].(string)
		if body != "" {
			if ct == "" || strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") || strings.Contains(ct, "xml") {
				response["body"] = body
			} else {
				response["base64Body"] = logged["bodyAsBase64"]
			}
		}
		keyBytes, _ := json.Marshal(req)
		key := string(keyBytes)
		if seen[key] && spec["repeatsAsScenarios"] == false {
			continue
		}
		seen[key] = true
		name := ""
		if u != nil {
			name = strings.Trim(strings.ReplaceAll(u.Path, "/", "_"), "_")
		}
		m := Mapping{"request": req, "response": response, "name": name}
		if spec["persist"] != false {
			m["persistent"] = true
		}
		m, err := a.store.AddMapping(m)
		if err != nil {
			return nil, err
		}
		a.store.setMappingOrder(m["id"].(string), &adminOrder{children: map[string]*adminOrder{"response": entry.order.child("response")}})
		mappings = append(mappings, m)
		ids = append(ids, m["id"].(string))
		if spec["persist"] != false && a.root != "" {
			dir := filepath.Join(a.root, "mappings")
			if err = os.MkdirAll(dir, 0755); err != nil {
				return nil, err
			}
			b, _ := marshalAdminJSONWithStore(m, a.store)
			if err = os.WriteFile(filepath.Join(dir, m["id"].(string)+".json"), b, 0644); err != nil {
				return nil, err
			}
		}
	}

	if spec["repeatsAsScenarios"] != false {
		groups := map[string][]Mapping{}
		keys := []string{}
		for _, m := range mappings {
			b, _ := json.Marshal(m["request"])
			key := string(b)
			if groups[key] == nil {
				keys = append(keys, key)
			}
			groups[key] = append(groups[key], m)
		}
		index := 0
		for _, key := range keys {
			group := groups[key]
			if len(group) < 2 {
				continue
			}
			index++
			req := group[0]["request"].(map[string]any)
			path, _ := req["url"].(string)
			if path == "" {
				path, _ = req["urlPath"].(string)
			}
			u, _ := url.Parse(path)
			name := fmt.Sprintf("scenario-%d-%s", index, strings.ReplaceAll(strings.Trim(u.Path, "/"), "/", "-"))
			for i := len(group) - 1; i >= 0; i-- {
				m := group[i]
				count := len(group) - i
				m["scenarioName"] = name
				m["requiredScenarioState"] = "Started"
				if count > 1 {
					m["requiredScenarioState"] = fmt.Sprintf("%s-%d", name, count)
				}
				if i > 0 {
					m["newScenarioState"] = fmt.Sprintf("%s-%d", name, count+1)
				}
				_, _ = a.store.AddMapping(m)
				if spec["persist"] != false && a.root != "" {
					b, _ := marshalAdminJSONWithStore(m, a.store)
					if err := os.WriteFile(filepath.Join(a.root, "mappings", m["id"].(string)+".json"), b, 0644); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	if format, _ := spec["outputFormat"].(string); strings.EqualFold(format, "ids") {
		return map[string]any{"ids": ids}, nil
	}
	return map[string]any{"mappings": mappings}, nil
}
