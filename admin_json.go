// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
)

// marshalAdminJSON mirrors Jackson's default pretty printer (objects indent,
// arrays use inline spaces). Known WireMock model properties use declaration order.
func marshalAdminJSON(v any) ([]byte, error) { return marshalAdminJSONWithStore(v, nil) }
func marshalAdminJSONWithStore(v any, store *Store) ([]byte, error) {
	var b bytes.Buffer
	if m, ok := v.(map[string]any); ok && len(m) == 0 {
		return []byte("{}"), nil
	}
	err := adminJSONValue(&b, v, 0, "", nil, store)
	return b.Bytes(), err
}
func adminJSONValue(b *bytes.Buffer, v any, depth int, context string, order *adminOrder, store *Store) error {
	if ordered, ok := v.(adminOrderedValue); ok {
		return adminJSONValue(b, ordered.value, depth, context, ordered.order, store)
	}
	if raw, ok := v.(json.RawMessage); ok {
		return adminRawJSON(b, raw, depth)
	}
	if m, ok := v.(map[string]any); ok {
		if len(m) == 0 {
			b.WriteString("{ }")
			return nil
		}
		if store != nil && m["request"] != nil && m["response"] != nil && m["wasMatched"] == nil {
			if id, ok := m["id"].(string); ok {
				order = store.mappingOrder(id)
			}
		}
		keys := adminKeys(m, context, order)
		b.WriteString("{\n")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString(strings.Repeat("  ", depth+1))
			adminJSONString(b, k)
			b.WriteString(" : ")
			if err := adminJSONValue(b, m[k], depth+1, k, order.child(k), store); err != nil {
				return err
			}
		}
		b.WriteByte('\n')
		b.WriteString(strings.Repeat("  ", depth))
		b.WriteByte('}')
		return nil
	}
	rv := reflect.ValueOf(v)
	if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
		if data, ok := v.([]byte); ok {
			encoded, _ := json.Marshal(data)
			b.Write(encoded)
			return nil
		}
		b.WriteString("[ ")
		for i := 0; i < rv.Len(); i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			if err := adminJSONValue(b, rv.Index(i).Interface(), depth, context, order.element(i), store); err != nil {
				return err
			}
		}
		if rv.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte(']')
		return nil
	}
	switch x := v.(type) {
	case string:
		adminJSONString(b, x)
	case nil:
		b.WriteString("null")
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		b.Write(raw)
	}
	return nil
}
func adminKeys(m map[string]any, context string, source *adminOrder) []string {
	var order []string
	switch {
	case m["wasMatched"] != nil:
		order = strings.Fields("id request responseDefinition response wasMatched timing subEvents stubMapping")
	case m["absoluteUrl"] != nil:
		order = strings.Fields("url absoluteUrl method clientIp headers cookies browserProxyRequest loggedDate bodyAsBase64 body protocol scheme host port loggedDateString queryParams formParams")
	case m["request"] != nil && m["response"] != nil:
		order = strings.Fields("id name request newRequest response uuid persistent priority scenarioName requiredScenarioState newScenarioState postServeActions serveEventListeners metadata")
	case m["possibleStates"] != nil:
		order = strings.Fields("id name state possibleStates mappings")
	case context == "request":
		order = strings.Fields("scheme host port url urlPattern urlPath urlPathPattern urlPathTemplate clientIp method headers pathParameters queryParameters formParameters cookies basicAuthCredentials bodyPatterns multipartPatterns customMatcher")
	case context == "response" && m["bodyAsBase64"] != nil:
		order = strings.Fields("status headers bodyAsBase64 body")
	case context == "response" || context == "responseDefinition":
		order = strings.Fields("status statusMessage body jsonBody base64Body bodyFileName headers additionalProxyRequestHeaders removeProxyRequestHeaders fixedDelayMilliseconds delayDistribution chunkedDribbleDelay proxyBaseUrl proxyUrlPrefixToRemove fault transformers transformerParameters fromConfiguredStub bodyAsBase64")
	case context == "settings":
		order = strings.Fields("fixedDelay delayDistribution proxyPassThrough extended")
	case context == "timing":
		order = strings.Fields("addedDelay processTime responseSendTime serveTime totalTime")
	case context == "bodyPatterns":
		order = strings.Fields("equalTo equalToJson equalToXml binaryEqualTo contains matches doesNotMatch matchesJsonPath matchesXPath caseInsensitive ignoreArrayOrder ignoreExtraElements")
	case m["mappings"] != nil:
		order = strings.Fields("mappings meta")
	case m["requests"] != nil:
		order = strings.Fields("requests meta requestJournalDisabled")
	case m["count"] != nil:
		order = strings.Fields("count requestJournalDisabled")
	case m["code"] != nil:
		order = strings.Fields("code source title detail")
	case m["key"] != nil && m["values"] != nil:
		order = strings.Fields("key values")
	}
	if len(order) == 0 && source != nil {
		order = source.keys
	}
	keys := make([]string, 0, len(m))
	seen := map[string]bool{}
	for _, k := range order {
		if _, ok := m[k]; ok && !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	rest := []string{}
	for k := range m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}
func adminRawJSON(b *bytes.Buffer, data []byte, depth int) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	return adminRawValue(b, d, depth)
}
func adminRawValue(b *bytes.Buffer, d *json.Decoder, depth int) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	switch x := t.(type) {
	case json.Delim:
		if x == '{' {
			b.WriteByte('{')
			i := 0
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				if i == 0 {
					b.WriteByte('\n')
				} else {
					b.WriteString(",\n")
				}
				b.WriteString(strings.Repeat("  ", depth+1))
				adminJSONString(b, key.(string))
				b.WriteString(" : ")
				if err = adminRawValue(b, d, depth+1); err != nil {
					return err
				}
				i++
			}
			if i == 0 {
				b.WriteByte(' ')
			} else {
				b.WriteByte('\n')
				b.WriteString(strings.Repeat("  ", depth))
			}
			b.WriteByte('}')
		} else if x == '[' {
			b.WriteString("[ ")
			i := 0
			for d.More() {
				if i > 0 {
					b.WriteString(", ")
				}
				if err = adminRawValue(b, d, depth); err != nil {
					return err
				}
				i++
			}
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteByte(']')
		} else {
			return fmt.Errorf("unexpected JSON delimiter")
		}
		_, err = d.Token()
		return err
	case string:
		adminJSONString(b, x)
	case json.Number:
		b.WriteString(string(x))
	default:
		raw, _ := json.Marshal(x)
		b.Write(raw)
	}
	return nil
}

type adminOrder struct {
	keys     []string
	children map[string]*adminOrder
	elements []*adminOrder
}

func (o *adminOrder) child(k string) *adminOrder {
	if o == nil {
		return nil
	}
	return o.children[k]
}
func (o *adminOrder) element(i int) *adminOrder {
	if o == nil || i >= len(o.elements) {
		return nil
	}
	return o.elements[i]
}
func decodeAdminOrder(raw []byte) (*adminOrder, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	return readAdminOrder(d)
}
func readAdminOrder(d *json.Decoder) (*adminOrder, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	o := &adminOrder{}
	switch t {
	case json.Delim('{'):
		o.children = map[string]*adminOrder{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			key := k.(string)
			if _, ok := o.children[key]; !ok {
				o.keys = append(o.keys, key)
			}
			child, err := readAdminOrder(d)
			if err != nil {
				return nil, err
			}
			o.children[key] = child
		}
		_, err = d.Token()
	case json.Delim('['):
		for d.More() {
			child, err := readAdminOrder(d)
			if err != nil {
				return nil, err
			}
			o.elements = append(o.elements, child)
		}
		_, err = d.Token()
	}
	return o, err
}
func (a *adminHandler) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		data, err := marshalAdminJSONWithStore(v, a.store)
		if err == nil {
			_, _ = w.Write(data)
		}
	}
}
func (a *adminHandler) rememberOrder(m Mapping, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	a.store.SetMappingOrder(m["id"].(string), raw)
}

type adminOrderedValue struct {
	value any
	order *adminOrder
}

func adminJSONString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 32 {
				fmt.Fprintf(b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}
