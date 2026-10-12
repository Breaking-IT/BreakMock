// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"encoding/base64"
	"encoding/json"
)

// HTTP response definitions expose legacy scalar body fields even when configured
// using v4 entity objects. Keep the original object internally for rendering.
func adminResponseEntity(m map[string]any, order *adminOrder) (map[string]any, *adminOrder) {
	entity, ok := m["body"].(map[string]any)
	if !ok {
		return m, order
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	delete(out, "body")
	if data, ok := entity["data"].(map[string]any); ok {
		out["jsonBody"] = data
		if order != nil {
			copyOrder := &adminOrder{keys: order.keys, children: map[string]*adminOrder{}}
			for k, v := range order.children {
				copyOrder.children[k] = v
			}
			copyOrder.children["jsonBody"] = order.child("body").child("data")
			order = copyOrder
		}
	} else if raw, ok := entity["data"].(json.RawMessage); ok {
		out["jsonBody"] = raw
	} else if file, ok := entity["filePath"].(string); ok {
		out["bodyFileName"] = file
	} else if data, ok := entity["data"].(string); ok {
		format := bmString(entity["format"])
		if format == "" {
			format = bmString(entity["encoding"])
		}
		if format == "binary" || (entity["compression"] != nil && entity["compression"] != "none") {
			bytes, _ := entityEncode(data, bmString(entity["charset"]))
			out["base64Body"] = base64.StdEncoding.EncodeToString(bytes)
		} else {
			out["body"] = data
		}
	} else if data, ok := entity["base64Data"].(string); ok {
		if entity["format"] == "text" {
			bytes, _ := base64.StdEncoding.DecodeString(data)
			out["body"] = string(bytes)
		} else {
			out["base64Body"] = data
		}
	}
	return out, order
}

func adminMessageEntity(entity map[string]any) (map[string]any, bool) {
	if entity["data"] == nil && entity["base64Data"] == nil && entity["filePath"] == nil && entity["dataRef"] == nil {
		return entity, false
	}
	if data, ok := entity["data"].(map[string]any); ok {
		return map[string]any{"data": data, "format": "json"}, true
	}
	if data, ok := entity["data"].(json.RawMessage); ok {
		return map[string]any{"data": data, "format": "json"}, true
	}
	out := map[string]any{}
	format := bmString(entity["format"])
	if format == "" {
		format = bmString(entity["encoding"])
	}
	inline := entity["filePath"] == nil && entity["dataRef"] == nil
	if format != "" && !(inline && (format == "text" || format == "binary")) {
		out["format"] = format
	}
	if compression := entity["compression"]; compression != nil && compression != "none" {
		out["compression"] = compression
	}
	if charset := entity["charset"]; charset != nil && charset != "UTF-8" {
		out["charset"] = charset
	}
	if !inline {
		for _, key := range []string{"filePath", "dataStore", "dataRef"} {
			if entity[key] != nil {
				out[key] = entity[key]
			}
		}
		return out, true
	}
	binary := format == "binary" || entity["compression"] != nil && entity["compression"] != "none" || format == "" && entity["base64Data"] != nil
	if binary {
		if text, ok := entity["data"].(string); ok {
			bytes, _ := entityEncode(text, bmString(entity["charset"]))
			out["base64Data"] = base64.StdEncoding.EncodeToString(bytes)
		} else {
			out["base64Data"] = entity["base64Data"]
		}
	} else if entity["data"] != nil {
		out["data"] = entity["data"]
	} else if data, ok := entity["base64Data"].(string); ok {
		bytes, _ := base64.StdEncoding.DecodeString(data)
		out["data"] = string(bytes)
	}
	return out, true
}
