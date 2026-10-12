// Copyright (C) 2016-2024 Thomas Akehurst
// Copyright 2026 Breaking-IT. Licensed under Apache-2.0; see LICENSE.
package breakmock

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type nearCandidate struct {
	value    map[string]any
	distance float64
	order    *adminOrder
}

func nearestMappings(store *Store, req Request, logged map[string]any, requestOrder *adminOrder) []nearCandidate {
	list := []nearCandidate{}
	for _, m := range store.Mappings() {
		pattern, _ := m["request"].(map[string]any)
		distance := RequestMatchDistance(pattern, req)
		list = append(list, nearCandidate{map[string]any{"request": logged, "stubMapping": m, "matchResult": map[string]any{"subEvents": matcherSubEvents(pattern, req), "diffDescriptions": []any{}, "distance": distance}}, distance, &adminOrder{children: map[string]*adminOrder{"request": requestOrder}}})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].distance < list[j].distance })
	if len(list) > 3 {
		list = list[:3]
	}
	return list
}
func serveNearMissAdmin(w http.ResponseWriter, r *http.Request, store *Store) bool {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if !((r.Method == "POST" && (path == "/__admin/near-misses/request" || path == "/__admin/near-misses/request-pattern")) || (r.Method == "GET" && path == "/__admin/requests/unmatched/near-misses")) {
		return false
	}
	list := []nearCandidate{}
	if r.Method == "POST" {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return true
		}
		data, err := DecodeDocument(raw)
		if err != nil {
			http.Error(w, err.Error(), 422)
			return true
		}
		if strings.HasSuffix(path, "/request") {
			req, logged := decodeNearRequest(data)
			list = nearestMappings(store, req, logged, parseAdminOrder(raw))
		} else {
			if data["method"] == nil {
				data["method"] = "ANY"
			}
			for _, entry := range store.journalSnapshot() {
				distance := RequestMatchDistance(data, entry.request)
				request, _ := entry.event["request"].(map[string]any)
				list = append(list, nearCandidate{map[string]any{"request": request, "requestPattern": data, "matchResult": map[string]any{"subEvents": matcherSubEvents(data, entry.request), "diffDescriptions": []any{}, "distance": distance}}, distance, &adminOrder{children: map[string]*adminOrder{"request": entry.order.child("request"), "requestPattern": parseAdminOrder(raw)}}})
			}
			sort.SliceStable(list, func(i, j int) bool { return list[i].distance < list[j].distance })
			if len(list) > 3 {
				list = list[:3]
			}
		}
	} else {
		for _, entry := range store.journalSnapshot() {
			if entry.event["wasMatched"] == false {
				logged, _ := entry.event["request"].(map[string]any)
				list = append(list, nearestMappings(store, entry.request, logged, entry.order.child("request"))...)
			}
		}
	}
	values := []any{}
	for _, candidate := range list {
		values = append(values, adminOrderedValue{value: candidate.value, order: candidate.order})
	}
	body, err := marshalAdminJSONWithStore(map[string]any{"nearMisses": values}, store)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write(body)
	return true
}
func decodeNearRequest(v map[string]any) (Request, map[string]any) {
	req := Request{Method: bmString(v["method"]), URL: bmString(v["url"]), AbsoluteURL: bmString(v["absoluteUrl"]), ClientIP: bmString(v["clientIp"]), Headers: http.Header{}}
	if h, ok := v["headers"].(map[string]any); ok {
		for k, x := range h {
			if str, ok := x.(string); ok {
				req.Headers.Add(k, str)
			} else {
				for _, item := range bmSlice(x) {
					req.Headers.Add(k, bmString(item))
				}
			}
		}
	}
	if v["bodyAsBase64"] != nil {
		req.Body, _ = base64.StdEncoding.DecodeString(bmString(v["bodyAsBase64"]))
	}
	logged := map[string]any{"browserProxyRequest": v["browserProxyRequest"] == true, "port": -1, "queryParams": map[string]any{}, "formParams": map[string]any{}}
	for _, key := range []string{"url", "absoluteUrl", "method", "clientIp", "headers", "cookies", "loggedDate", "protocol", "scheme", "host", "port"} {
		if value, ok := v[key]; ok && value != nil {
			logged[key] = value
		}
	}
	if v["bodyAsBase64"] != nil {
		logged["bodyAsBase64"] = base64.StdEncoding.EncodeToString(req.Body)
		logged["body"] = string(req.Body)
	}
	if n, ok := v["loggedDate"].(float64); ok {
		logged["loggedDateString"] = time.UnixMilli(int64(n)).UTC().Format(time.RFC3339Nano)
	}
	if u, err := url.Parse(req.AbsoluteURL); err == nil && u.Host != "" {
		logged["scheme"], logged["host"] = u.Scheme, u.Hostname()
		port := 80
		if u.Scheme == "https" {
			port = 443
		}
		if u.Port() != "" {
			json.Unmarshal([]byte(u.Port()), &port)
		}
		logged["port"] = port
	}
	if u, err := url.Parse(req.URL); err == nil {
		q := map[string]any{}
		for k, v := range u.Query() {
			q[k] = map[string]any{"key": k, "values": v}
		}
		logged["queryParams"] = q
	}
	return req, logged
}
func parseAdminOrder(raw []byte) *adminOrder { order, _ := decodeAdminOrder(raw); return order }

func matcherSubEvents(pattern map[string]any, req Request) []any {
	events := []any{}
	if req.Body == nil {
		return events
	}
	if patterns, ok := pattern["bodyPatterns"].([]any); ok {
		for _, raw := range patterns {
			if p, ok := raw.(map[string]any); ok {
				if expected, ok := p["equalToXml"].(string); ok && XMLMatchComparisonWarning(expected, string(req.Body)) {
					events = append(events, map[string]any{"type": "WARNING", "data": map[string]any{"message": "Caught exception during comparison"}})
				}
			}
		}
	}
	return events
}
