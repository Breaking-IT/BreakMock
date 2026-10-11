// Copyright (C) 2025-2026 Thomas Akehurst
// Copyright 2026 Breaking-IT. Licensed under Apache-2.0; see LICENSE.
package breakmock

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// Incoming executes only the first matching message stub, then journals reception.
// Sent events precede reception in insertion order, as in WireMock's handler.
func (s *MessageStore) Incoming(id string, message map[string]any) error {
	channel := s.Channel(id)
	if channel == nil {
		return nil
	}
	var request *Request
	if channel.Type != "fixed" {
		request = &channel.Request
	}
	body, present := messageBodyBytes(message)
	var matched Mapping
	for _, stub := range s.Mappings() {
		trigger, _ := stub["trigger"].(map[string]any)
		typ := bmString(trigger["type"])
		if typ != "" && typ != "message" {
			continue
		}
		cp, _ := trigger["channel"].(map[string]any)
		if cp != nil && !MatchMessageChannel(cp, request, bmString(channel.Definition["providerName"]), bmString(channel.Definition["channelName"])) {
			continue
		}
		mp, _ := trigger["message"].(map[string]any)
		matches := MatchMessage(mp, request, body, present)
		if _, binary := message["base64Body"]; binary {
			matches = MatchBinaryMessage(mp, request, body, present)
		}
		if !matches {
			continue
		}
		matched = stub
		if err := s.executeMessageActions(stub, channel, request, message); err != nil {
			return err
		}
		break
	}
	s.Record(id, "received", message, matched)
	return nil
}

// TriggerHTTP processes the first HTTP trigger in priority/insertion order.
func (s *MessageStore) TriggerHTTP(req Request, matched ...Mapping) error {
	for _, stub := range s.Mappings() {
		trigger, _ := stub["trigger"].(map[string]any)
		switch trigger["type"] {
		case "http-stub":
			if len(matched) == 0 || matched[0] == nil || trigger["stubId"] != matched[0]["id"] {
				continue
			}
		case "http-request":
			pattern, _ := trigger["requestPattern"].(map[string]any)
			if !MatchRequest(pattern, req) {
				continue
			}
		default:
			continue
		}
		return s.executeMessageActions(stub, nil, &req, nil)
	}
	return nil
}
func messageBodyBytes(message map[string]any) ([]byte, bool) {
	if body, ok := message["body"].(string); ok {
		return []byte(body), true
	}
	if body, ok := message["base64Body"].(string); ok {
		bytes, err := base64.StdEncoding.DecodeString(body)
		return bytes, err == nil
	}
	return nil, false
}
func (s *MessageStore) executeMessageActions(stub Mapping, origin *MessageChannel, req *Request, incoming map[string]any) error {
	for _, raw := range bmSlice(stub["actions"]) {
		action, ok := raw.(map[string]any)
		if !ok || action["type"] != "send" {
			continue
		}
		definition, _ := action["message"].(map[string]any)
		message, err := s.resolveMessageDefinition(definition, action, req, incoming)
		if err != nil {
			return err
		}
		if origin != nil {
			delete(message, "headers")
		}
		target, _ := action["channelTarget"].(map[string]any)
		typ := bmString(target["type"])
		if typ == "" || typ == "originating" {
			if origin != nil {
				if err := s.sendMessage(origin, message); err != nil {
					return err
				}
			}
			continue
		}
		found := false
		for _, c := range s.Channels() {
			switch typ {
			case "fixed-channel":
				if c.Type != "fixed" || c.Definition["providerName"] != target["providerName"] || c.Definition["channelName"] != target["channelName"] {
					continue
				}
			case "request-initiated":
				if c.Type == "fixed" {
					continue
				}
				if ct := bmString(target["channelType"]); ct != "" && !strings.EqualFold(ct, c.Type) {
					continue
				}
				pattern, _ := target["requestPattern"].(map[string]any)
				if !MatchRequest(pattern, c.Request) {
					continue
				}
			default:
				continue
			}
			found = true
			if err := s.sendMessage(c, message); err != nil {
				return err
			}
			if typ == "fixed-channel" {
				break
			}
		}
		if typ == "fixed-channel" && !found {
			return &wireEntityError{fmt.Sprintf("com.github.tomakehurst.wiremock.admin.NotFoundException: No fixed channel named '%s' exists on provider '%s'", bmString(target["channelName"]), bmString(target["providerName"]))}
		}
	}
	return nil
}
func (s *MessageStore) sendMessage(channel *MessageChannel, message map[string]any) error {
	if channel.Send != nil {
		if err := channel.Send(message); err != nil {
			return err
		}
	}
	s.Record(channel.ID, "sent", message, nil)
	return nil
}
func (s *MessageStore) resolveMessageDefinition(definition, action map[string]any, req *Request, incoming map[string]any) (map[string]any, error) {
	entity, err := resolveBodyEntity(definition["body"], s.rootDirectory())
	if err != nil {
		return nil, err
	}
	binary := entity.binary
	inline := !entity.fromFile
	body := entity.data
	if !binary && inline && entity.compression != "" && entity.compression != "none" {
		// SimpleEntityDefinition.getDataAsString exposes compressed text as
		// base64 to MessageTemplateTransformer before rebuilding a text entity.
		body = []byte(base64.StdEncoding.EncodeToString(body))
	} else if !binary {
		text, err := entity.text()
		if err != nil {
			return nil, err
		}
		body = []byte(text)
	}
	message := map[string]any{}
	params, _ := action["transformerParameters"].(map[string]any)
	transformed := !binary && inline && len(body) > 0
	if transformed {
		text, err := RenderMessageTemplateWithRequest(string(body), req, incoming, params)
		if err != nil {
			return nil, err
		}
		body = []byte(text)
	}
	if binary {
		message["base64Body"] = base64.StdEncoding.EncodeToString(body)
	} else {
		message["body"] = string(body)
	}
	if headers, ok := definition["headers"].(map[string]any); ok {
		rendered := map[string]any{}
		for key, value := range headers {
			if !transformed {
				rendered[key] = value
				continue
			}
			values := bmSlice(value)
			scalar := values == nil
			if scalar {
				values = []any{value}
			}
			out := []any{}
			for _, v := range values {
				text, err := RenderMessageTemplateWithRequest(bmString(v), req, incoming, params)
				if err != nil {
					return nil, err
				}
				out = append(out, text)
			}
			if scalar && len(out) == 1 {
				rendered[key] = out[0]
			} else {
				rendered[key] = out
			}
		}
		if len(rendered) > 0 {
			message["headers"] = rendered
		}
	}
	return message, nil
}
func (s *MessageStore) rootDirectory() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.rootDir == "" {
		return "."
	}
	return s.rootDir
}
