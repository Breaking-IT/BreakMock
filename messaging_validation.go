// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import "net/http"

func requiredMessageFields(w http.ResponseWriter, m Mapping, keys ...string) bool {
	for _, key := range keys {
		if m[key] == nil {
			adminProblem(w, 422, 10, key+" is required", "", key)
			return false
		}
	}
	return true
}
func validateMessageMapping(w http.ResponseWriter, m Mapping) bool {
	if trigger, ok := m["trigger"].(map[string]any); ok {
		switch trigger["type"] {
		case "http-request":
			if !requiredMessageFields(w, trigger, "requestPattern") {
				return false
			}
		case "http-stub":
			if !requiredMessageFields(w, trigger, "stubId") {
				return false
			}
		}
	}
	if actions, ok := m["actions"].([]any); ok {
		for _, value := range actions {
			if action, ok := value.(map[string]any); ok && action["type"] == "send" {
				if !requiredMessageFields(w, action, "message") {
					return false
				}
				if target, ok := action["channelTarget"].(map[string]any); ok && target["type"] == "fixed-channel" {
					if !requiredMessageFields(w, target, "providerName", "channelName") {
						return false
					}
				}
			}
		}
	}
	return true
}
