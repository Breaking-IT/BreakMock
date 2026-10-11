// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import "strings"

// MatchMessage evaluates the v4 verification MessagePattern. A present empty
// message remains an empty string, distinct from a missing message.
func MatchMessage(pattern map[string]any, channel *Request, body []byte, present bool) bool {
	if p, ok := pattern["channel"].(map[string]any); ok {
		if channel == nil || !MatchRequest(p, *channel) {
			return false
		}
	}
	if p, exists := pattern["body"]; exists {
		return matchValue(p, string(body), present)
	}
	return true
}

// MatchMessageChannel evaluates polymorphic v4 channel patterns. Fixed channels
// compare provider identity and the channel-name string pattern.
func MatchMessageChannel(pattern map[string]any, channel *Request, provider, name string) bool {
	if strings.EqualFold(bmString(pattern["type"]), "fixed") {
		if channel != nil || pattern["channelProvider"] != provider {
			return false
		}
		if p, exists := pattern["channelName"]; exists {
			return matchValue(p, name, true)
		}
		return true
	}
	if p, ok := pattern["initiatingRequestPattern"].(map[string]any); ok {
		return channel != nil && MatchRequest(p, *channel)
	}
	return true
}

// MatchBinaryMessage preserves WireMock's null string view of binary messages.
func MatchBinaryMessage(pattern map[string]any, channel *Request, body []byte, present bool) bool {
	if p, ok := pattern["channel"].(map[string]any); ok {
		if channel == nil || !MatchRequest(p, *channel) {
			return false
		}
	}
	if p, exists := pattern["body"]; exists {
		if object, ok := p.(map[string]any); ok {
			if _, binary := object["binaryEqualTo"]; binary {
				return matchValue(p, string(body), present)
			}
		}
		return matchValue(p, "", false)
	}
	return true
}
