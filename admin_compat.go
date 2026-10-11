// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func adminProblem(w http.ResponseWriter, status, code int, title, detail, pointer string) {
	e := map[string]any{"code": code, "title": title}
	if detail != "" {
		e["detail"] = detail
	}
	if pointer != "" {
		e["source"] = map[string]any{"pointer": pointer}
	}
	adminJSON(w, status, map[string]any{"errors": []any{e}})
}
func validMappingUUID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil
}
func validateAdminMapping(w http.ResponseWriter, m Mapping) bool {
	allowed := strings.Fields("$schema id uuid name request newRequest response persistent priority scenarioName requiredScenarioState newScenarioState postServeActions serveEventListeners metadata")
	for key := range m {
		found := false
		for _, v := range allowed {
			if key == v {
				found = true
				break
			}
		}
		if !found {
			adminProblem(w, 422, 10, "Error parsing JSON", fmt.Sprintf("Unrecognized field %q (class com.github.tomakehurst.wiremock.stubbing.StubMapping), not marked as ignorable", key), "/"+key)
			return false
		}
	}
	for _, key := range []string{"id"} {
		if id, ok := m[key].(string); ok && !validMappingUUID(id) {
			detail := fmt.Sprintf("Cannot deserialize value of type `java.util.UUID` from String %q: UUID has to be represented by standard 36-char representation", id)
			adminProblem(w, 422, 10, "Error parsing JSON", detail, "/"+key)
			return false
		}
	}

	for _, section := range []struct{ key, typ, fields string }{{"request", "com.github.tomakehurst.wiremock.matching.RequestPattern", "scheme host port clientIp url urlPattern urlPath urlPathPattern urlPathTemplate method headers pathParameters queryParameters formParameters cookies basicAuthCredentials bodyPatterns multipartPatterns customMatcher"}, {"response", "com.github.tomakehurst.wiremock.http.ResponseDefinition", "status statusMessage body jsonBody base64Body bodyFileName openWebsocketChannel openSseChannel headers additionalProxyRequestHeaders removeProxyRequestHeaders fixedDelayMilliseconds delayDistribution chunkedDribbleDelay proxyBaseUrl proxyUrlPrefixToRemove fault transformers transformerParameters fromConfiguredStub"}} {
		obj, ok := m[section.key].(map[string]any)
		if !ok {
			continue
		}
		for key := range obj {
			valid := false
			for _, field := range strings.Fields(section.fields) {
				if field == key {
					valid = true
				}
			}
			if !valid {
				adminProblem(w, 422, 10, "Error parsing JSON", fmt.Sprintf("Unrecognized field %q (class %s), not marked as ignorable", key, section.typ), "/"+section.key+"/"+key)
				return false
			}
		}
	}
	return true
}
func (a *adminHandler) persistMapping(m Mapping) error {
	if m["persistent"] != true {
		return nil
	}
	dir := filepath.Join(a.root, "mappings")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := marshalAdminJSONWithStore(m, a.store)
	if err != nil {
		return err
	}
	id := m["id"].(string)
	updated := false
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if updated || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		doc, err := DecodeDocument(raw)
		if err != nil {
			return err
		}
		if doc["id"] == id || doc["uuid"] == id {
			updated = true
			return writeMappingFileAtomically(path, data)
		}
		if list, ok := doc["mappings"].([]any); ok {
			var source struct {
				Mappings []json.RawMessage `json:"mappings"`
			}
			if err = json.Unmarshal(raw, &source); err != nil {
				return err
			}
			for i, value := range list {
				item, ok := value.(map[string]any)
				if ok && (item["id"] == id || item["uuid"] == id) {
					source.Mappings[i] = data
					updated = true
				}
			}
			if updated {
				values := []any{}
				for _, item := range source.Mappings {
					values = append(values, item)
				}
				doc["mappings"] = values
				order, _ := decodeAdminOrder(raw)
				output, err := marshalAdminJSON(adminOrderedValue{doc, order})
				if err != nil {
					return err
				}
				return writeMappingFileAtomically(path, output)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if updated {
		return nil
	}
	return os.WriteFile(filepath.Join(dir, filepath.Base(id)+".json"), data, 0644)
}
func (a *adminHandler) scenarioKnown(name string) bool {
	for _, m := range a.store.Mappings() {
		if m["scenarioName"] == name {
			return true
		}
	}
	return false
}
func javaStringHash(s string) uint32 {
	var h uint32
	for _, r := range s {
		if r > 0xffff {
			r -= 0x10000
			h = 31*h + uint32(0xd800+(r>>10))
			h = 31*h + uint32(0xdc00+(r&1023))
		} else {
			h = 31*h + uint32(r)
		}
	}
	return h ^ (h >> 16)
}

func javaHashOrder(values []string) {
	capacity := 16
	for len(values) > capacity*3/4 {
		capacity *= 2
	}
	sort.SliceStable(values, func(i, j int) bool {
		return javaStringHash(values[i])&uint32(capacity-1) < javaStringHash(values[j])&uint32(capacity-1)
	})
}
