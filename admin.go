// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type adminHandler struct {
	recordMu      sync.Mutex
	recordStatus  string
	recordSpec    map[string]any
	recordStartID string
	recordProxyID string
	store         *Store
	root          string
}

func NewAdminHandler(store *Store, rootDir string) http.Handler {
	return &adminHandler{store: store, root: rootDir, recordStatus: "NeverStarted"}
}
func adminJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		data, err := marshalAdminJSON(v)
		if err == nil {
			_, _ = w.Write(data)
		}
	}
}
func adminError(w http.ResponseWriter, status int, message string) {
	adminJSON(w, status, map[string]any{"errors": []any{map[string]any{"code": 10, "title": message}}})
}
func adminDecode(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	data, readErr := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(data))
	v, err := DecodeDocument(data)
	if readErr != nil || err != nil || v == nil {
		adminError(w, 422, "Invalid JSON")
		return nil, false
	}
	return v, true
}
func (a *adminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/__admin"), "/")
	s := a.store
	switch {
	case p == "" && r.Method == "GET":
		a.writeJSON(w, 200, map[string]any{"mappings": s.Mappings()})
		return
	case p == "/health" && r.Method == "GET":
		a.writeJSON(w, 200, map[string]any{"status": "healthy", "message": "Wiremock is ok", "version": "BreakMock 0.1.0", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "uptimeInSeconds": int(time.Since(adminStart).Seconds())})
		return
	case p == "/version" && r.Method == "GET":
		if strings.Contains(r.Header.Get("Accept"), "text/plain") {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("BreakMock 0.1.0"))
		} else {
			a.writeJSON(w, 200, map[string]any{"version": "BreakMock 0.1.0"})
		}
		return
	case p == "/reset" && r.Method == "POST":
		if err := a.reloadDefaults(); err != nil {
			adminError(w, 500, err.Error())
			return
		}
		w.WriteHeader(200)
		return
	case p == "/mappings":
		switch r.Method {
		case "GET":
			all := s.Mappings()
			v := paginateMappings(all, r)
			a.writeJSON(w, 200, map[string]any{"mappings": v, "meta": map[string]any{"total": len(all)}})
		case "POST":
			v, ok := adminDecode(w, r)
			if !ok {
				return
			}
			m, err := a.addMappingDocument(v, r)
			if err != nil {
				adminError(w, 422, err.Error())
				return
			}
			a.writeJSON(w, 201, m)
		case "DELETE":
			if err := a.removeMappingFiles(""); err != nil {
				adminError(w, 500, err.Error())
				return
			}
			s.ResetMappings()
			a.writeJSON(w, 200, map[string]any{})
		default:
			adminError(w, 405, "Method not allowed")
		}
		return
	case p == "/mappings/import" && r.Method == "POST":
		v, ok := adminDecode(w, r)
		if !ok {
			return
		}
		items, ok := v["mappings"].([]any)
		if !ok {
			adminError(w, 422, "mappings must be an array")
			return
		}
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				adminError(w, 422, "mapping must be an object")
				return
			}
			if _, ok = m["request"].(map[string]any); !ok {
				adminError(w, 422, "request must be an object")
				return
			}
			if _, ok = m["response"].(map[string]any); !ok {
				adminError(w, 422, "response must be an object")
				return
			}
		}
		rawImport, _ := io.ReadAll(r.Body)
		var importSource struct {
			Mappings []json.RawMessage `json:"mappings"`
		}
		_ = json.Unmarshal(rawImport, &importSource)
		opts, _ := v["importOptions"].(map[string]any)
		if opts["deleteAllNotInImport"] == true {
			ids := map[string]bool{}
			for _, item := range items {
				m := item.(map[string]any)
				id, _ := m["id"].(string)
				ids[id] = true
			}
			for _, m := range s.Mappings() {
				if !ids[m["id"].(string)] {
					if err := a.removeMapping(m["id"].(string)); err != nil {
						adminError(w, 500, err.Error())
						return
					}
				}
			}
		}
		for index, item := range items {
			m := item.(map[string]any)
			id, _ := m["id"].(string)
			if opts["duplicatePolicy"] == "IGNORE" && s.GetMapping(id) != nil {
				continue
			}
			var order *adminOrder
			if index < len(importSource.Mappings) {
				order, _ = decodeAdminOrder(importSource.Mappings[index])
			}
			_, _ = s.addMapping(m, order, true)
		}
		w.WriteHeader(200)
		return
	case p == "/mappings/save" && r.Method == "POST":
		if a.root == "" {
			adminError(w, 500, "No root directory configured")
			return
		}
		dir := filepath.Join(a.root, "mappings")
		if err := os.MkdirAll(dir, 0755); err != nil {
			adminError(w, 500, err.Error())
			return
		}
		for _, original := range s.Mappings() {
			m := cloneMap(original)
			m["persistent"] = true
			m, _ = s.AddMapping(m)
			data, _ := marshalAdminJSONWithStore(m, s)
			if err := os.WriteFile(filepath.Join(dir, filepath.Base(m["id"].(string))+".json"), data, 0644); err != nil {
				adminError(w, 500, err.Error())
				return
			}
		}
		w.WriteHeader(200)
		return
	case p == "/mappings/reset" && r.Method == "POST":
		if err := a.reloadDefaults(); err != nil {
			adminError(w, 500, err.Error())
			return
		}
		w.WriteHeader(200)
		return
	case p == "/mappings/remove" && r.Method == "POST":
		v, ok := adminDecode(w, r)
		if !ok {
			return
		}
		id, _ := v["id"].(string)
		if id != "" {
			if err := a.removeMapping(id); err != nil {
				adminError(w, 500, err.Error())
				return
			}
		} else {
			for _, m := range s.Mappings() {
				if jsonEqual(m["request"], v["request"]) {
					if err := a.removeMapping(m["id"].(string)); err != nil {
						adminError(w, 500, err.Error())
						return
					}
				}
			}
		}
		w.WriteHeader(200)
		return
	case (p == "/mappings/find-by-metadata" || p == "/mappings/remove-by-metadata") && r.Method == "POST":
		v, ok := adminDecode(w, r)
		if !ok {
			return
		}
		found := []Mapping{}
		for _, m := range s.Mappings() {
			b, _ := json.Marshal(m["metadata"])
			if matchValue(v, string(b), m["metadata"] != nil) {
				found = append(found, m)
				if p == "/mappings/remove-by-metadata" {
					if err := a.removeMapping(m["id"].(string)); err != nil {
						adminError(w, 500, err.Error())
						return
					}
				}
			}
		}
		if p == "/mappings/find-by-metadata" {
			a.writeJSON(w, 200, map[string]any{"mappings": found, "meta": map[string]any{"total": len(found)}})
		} else {
			a.writeJSON(w, 200, map[string]any{})
		}
		return
	case p == "/mappings/unmatched" && (r.Method == "GET" || r.Method == "DELETE"):
		used := map[string]bool{}
		for _, e := range s.journalSnapshot() {
			if m, ok := e.event["stubMapping"].(map[string]any); ok {
				if id, ok := m["id"].(string); ok {
					used[id] = true
				}
			}
		}
		found := []Mapping{}
		for _, m := range s.Mappings() {
			id := m["id"].(string)
			if !used[id] {
				found = append(found, m)
				if r.Method == "DELETE" {
					if err := a.removeMapping(id); err != nil {
						adminError(w, 500, err.Error())
						return
					}
				}
			}
		}
		if r.Method == "GET" {
			a.writeJSON(w, 200, map[string]any{"mappings": found, "meta": map[string]any{"total": len(found)}})
		} else {
			a.writeJSON(w, 200, map[string]any{})
		}
		return
	case p == "/files" || strings.HasPrefix(p, "/files/"):
		a.files(w, r, p)
		return
	case strings.HasPrefix(p, "/mappings/"):
		id := strings.TrimPrefix(p, "/mappings/")
		if strings.Contains(id, "/") || id == "unmatched" || id == "find-by-metadata" || id == "remove-by-metadata" || id == "edit" {
			adminError(w, 501, "This admin operation is not implemented")
			return
		}
		m := s.GetMapping(id)
		if m == nil {
			w.WriteHeader(404)
			return
		}
		switch r.Method {
		case "GET":
			a.writeJSON(w, 200, m)
		case "DELETE":
			if err := a.removeMapping(id); err != nil {
				adminError(w, 500, err.Error())
				return
			}
			a.writeJSON(w, 200, map[string]any{})
		case "PUT":
			v, ok := adminDecode(w, r)
			if !ok {
				return
			}
			v["id"] = id
			m, err := a.addMappingDocument(v, r)
			if err != nil {
				adminError(w, 422, err.Error())
				return
			}
			a.writeJSON(w, 200, m)
		default:
			adminError(w, 405, "Method not allowed")
		}
		return
	case p == "/scenarios" && r.Method == "GET":
		states := map[string]map[string]bool{}
		for _, m := range s.Mappings() {
			name, _ := m["scenarioName"].(string)
			if name == "" {
				continue
			}
			if states[name] == nil {
				states[name] = map[string]bool{"Started": true}
			}
			for _, k := range []string{"requiredScenarioState", "newScenarioState"} {
				if v, ok := m[k].(string); ok {
					states[name][v] = true
				}
			}
		}
		out := []any{}
		for name, st := range states {
			possible := []string{}
			for v := range st {
				possible = append(possible, v)
			}
			sort.Slice(possible, func(i, j int) bool {
				if possible[i] == "Started" {
					return true
				}
				if possible[j] == "Started" {
					return false
				}
				return possible[i] < possible[j]
			})
			related := []Mapping{}
			for _, m := range s.Mappings() {
				if m["scenarioName"] == name {
					related = append(related, m)
				}
			}
			out = append(out, map[string]any{"id": name, "name": name, "state": s.ScenarioState(name), "possibleStates": possible, "mappings": related})
		}
		a.writeJSON(w, 200, map[string]any{"scenarios": out})
		return
	case p == "/scenarios/reset" && r.Method == "POST":
		s.ResetScenarios()
		a.writeJSON(w, 200, map[string]any{})
		return
	case strings.HasPrefix(p, "/scenarios/") && strings.HasSuffix(p, "/state") && r.Method == "PUT":
		name := strings.TrimSuffix(strings.TrimPrefix(p, "/scenarios/"), "/state")
		v, ok := adminDecode(w, r)
		if !ok {
			return
		}
		state, _ := v["state"].(string)
		if state == "" {
			state = "Started"
		}
		s.SetScenarioState(name, state)
		a.writeJSON(w, 200, map[string]any{})
		return
	case p == "/settings":
		switch r.Method {
		case "GET":
			a.writeJSON(w, 200, map[string]any{"settings": s.Settings()})
		case "PUT", "POST":
			v, ok := adminDecode(w, r)
			if !ok {
				return
			}
			if nested, ok := v["settings"].(map[string]any); ok {
				v = nested
			}
			s.SetSettings(v)
			w.WriteHeader(200)
		default:
			adminError(w, 405, "Method not allowed")
		}
		return
	case strings.HasPrefix(p, "/recordings/"):
		a.recordings(w, r, p)
		return
	case p == "/requests" || strings.HasPrefix(p, "/requests/"):
		a.requests(w, r, p)
		return
	default:
		adminError(w, 501, "This admin operation is not implemented")
	}
}
func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func paginateMappings(v []Mapping, r *http.Request) []Mapping {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	if offset > len(v) {
		offset = len(v)
	}
	v = v[offset:]
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err == nil && limit >= 0 && limit < len(v) {
			v = v[:limit]
		}
	}
	return v
}
func (a *adminHandler) requests(w http.ResponseWriter, r *http.Request, p string) {
	s := a.store
	if p == "/requests" && r.Method == "DELETE" {
		s.ResetRequests()
		a.writeJSON(w, 200, map[string]any{})
		return
	}
	entries := s.journalSnapshot()
	events := []any{}
	requests := []any{}
	ids := map[string]bool{}
	var pattern map[string]any
	if (p == "/requests/count" || p == "/requests/find" || p == "/requests/remove" || p == "/requests/remove-by-metadata") && r.Method == "POST" {
		var ok bool
		pattern, ok = adminDecode(w, r)
		if !ok {
			return
		}
	}
	if p == "/requests/unmatched/near-misses" {
		adminError(w, 501, "This admin operation is not implemented")
		return
	}
	if p != "/requests" && p != "/requests/count" && p != "/requests/find" && p != "/requests/remove" && p != "/requests/remove-by-metadata" && p != "/requests/unmatched" {
		id := strings.TrimPrefix(p, "/requests/")
		for _, e := range entries {
			if e.event["id"] == id {
				if r.Method == "DELETE" {
					s.removeEvents(map[string]bool{id: true})
					w.WriteHeader(200)
				} else if r.Method == "GET" {
					a.writeJSON(w, 200, adminOrderedValue{e.event, e.order})
				} else {
					adminError(w, 405, "Method not allowed")
				}
				return
			}
		}
		w.WriteHeader(404)
		return
	}
	for _, e := range entries {
		if p == "/requests/remove-by-metadata" {
			m, _ := e.event["stubMapping"].(map[string]any)
			b, _ := json.Marshal(m["metadata"])
			if !matchValue(pattern, string(b), m["metadata"] != nil) {
				continue
			}
		} else if pattern != nil && !MatchRequest(pattern, e.request) {
			continue
		}
		if p == "/requests/unmatched" && e.event["wasMatched"] == true {
			continue
		}
		if id := r.URL.Query().Get("matchingStub"); id != "" {
			m, _ := e.event["stubMapping"].(map[string]any)
			if m["id"] != id {
				continue
			}
		}
		events = append(events, adminOrderedValue{e.event, e.order})
		requests = append(requests, adminOrderedValue{e.event["request"], e.order.child("request")})
		ids[e.event["id"].(string)] = true
	}
	switch p {
	case "/requests/count":
		a.writeJSON(w, 200, map[string]any{"count": len(events), "requestJournalDisabled": s.JournalDisabled()})
	case "/requests/find", "/requests/unmatched":
		a.writeJSON(w, 200, map[string]any{"requests": requests, "requestJournalDisabled": s.JournalDisabled()})
	case "/requests/remove", "/requests/remove-by-metadata":
		s.removeEvents(ids)
		a.writeJSON(w, 200, map[string]any{"serveEvents": events})
	default:
		total := len(events)
		if limit, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && limit >= 0 && limit < len(events) {
			events = events[:limit]
		}
		a.writeJSON(w, 200, map[string]any{"requests": events, "meta": map[string]any{"total": total}, "requestJournalDisabled": s.JournalDisabled()})
	}
}

var adminStart = time.Now()

func (a *adminHandler) files(w http.ResponseWriter, r *http.Request, p string) {
	if a.root == "" {
		adminError(w, 500, "No root directory configured")
		return
	}
	dir := filepath.Join(a.root, "__files")
	if err := os.MkdirAll(dir, 0755); err != nil {
		adminError(w, 500, err.Error())
		return
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		adminError(w, 500, err.Error())
		return
	}
	defer root.Close()
	if p == "/files" && r.Method == "GET" {
		names := []string{}
		err := fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				names = append(names, path)
			}
			return nil
		})
		if err != nil {
			adminError(w, 500, err.Error())
			return
		}
		sort.Strings(names)
		a.writeJSON(w, 200, names)
		return
	}
	name := strings.TrimPrefix(p, "/files/")
	if !filepath.IsLocal(name) {
		adminError(w, 400, "Invalid file path")
		return
	}
	switch r.Method {
	case "GET":
		b, err := root.ReadFile(name)
		if err != nil {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(200)
		_, _ = w.Write(b)
	case "PUT":
		b, err := io.ReadAll(r.Body)
		if err == nil {
			err = root.MkdirAll(filepath.Dir(name), 0755)
		}
		if err == nil {
			err = root.WriteFile(name, b, 0644)
		}
		if err != nil {
			adminError(w, 500, err.Error())
			return
		}
		a.writeJSON(w, 200, b)
	case "DELETE":
		err := root.Remove(name)
		if err != nil && !os.IsNotExist(err) {
			adminError(w, 500, err.Error())
			return
		}
		w.WriteHeader(200)
	default:
		adminError(w, 405, "Method not allowed")
	}
}

func (a *adminHandler) reloadDefaults() error {
	a.store.ResetMappings()
	a.store.ResetRequests()
	if a.root == "" {
		return nil
	}
	dir := filepath.Join(a.root, "mappings")
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == dir {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		m, err := DecodeDocument(b)
		if err != nil {
			return err
		}
		if _, ok := m["mappings"].([]any); ok {
			var bulk struct {
				Mappings []json.RawMessage `json:"mappings"`
			}
			if err = json.Unmarshal(b, &bulk); err != nil {
				return err
			}
			for _, raw := range bulk.Mappings {
				if _, err = a.store.AddMappingDocument(raw); err != nil {
					return err
				}
			}
			return nil
		}
		_, err = a.store.AddMappingDocument(b)
		return err
	})
}
func (a *adminHandler) removeMappingFiles(id string) error {
	if a.root == "" {
		return nil
	}
	dir := filepath.Join(a.root, "mappings")
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == dir {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		if id == "" {
			return os.Remove(path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		m, err := DecodeDocument(b)
		if err != nil {
			return err
		}
		if m["id"] == id || m["uuid"] == id {
			return os.Remove(path)
		}
		if list, ok := m["mappings"].([]any); ok {
			var bulk struct {
				Mappings []json.RawMessage `json:"mappings"`
			}
			if err = json.Unmarshal(b, &bulk); err != nil {
				return err
			}
			keep := []any{}
			removed := false
			for index, value := range list {
				item, ok := value.(map[string]any)
				if ok && (item["id"] == id || item["uuid"] == id) {
					removed = true
					continue
				}
				keep = append(keep, bulk.Mappings[index])
			}
			if removed {
				if len(keep) == 0 {
					return os.Remove(path)
				}
				m["mappings"] = keep
				order, err := decodeAdminOrder(b)
				if err != nil {
					return err
				}
				data, err := marshalAdminJSON(adminOrderedValue{m, order})
				if err != nil {
					return err
				}
				return writeMappingFileAtomically(path, data)
			}
		}
		return nil
	})
}

func (a *adminHandler) removeMapping(id string) error {
	if err := a.removeMappingFiles(id); err != nil {
		return err
	}
	a.store.DeleteMapping(id)
	return nil
}
func writeMappingFileAtomically(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".breakmock-mapping-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(info.Mode().Perm()); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (a *adminHandler) addMappingDocument(m Mapping, r *http.Request) (Mapping, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	order, err := decodeAdminOrder(raw)
	if err != nil {
		return nil, err
	}
	return a.store.addMapping(m, order, true)
}
