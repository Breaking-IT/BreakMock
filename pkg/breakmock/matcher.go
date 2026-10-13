// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/antchfx/xmlquery"
	"github.com/antchfx/xpath"
	"github.com/dlclark/regexp2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// MatchRequest evaluates the built-in WireMock request pattern. Unknown custom
// extensions do not match: Go cannot execute a Java RequestMatcherExtension.
func MatchRequest(pattern map[string]any, request Request) bool {
	if !matchRequestMethod(pattern["method"], request.Method) {
		return false
	}
	if clientIP, ok := pattern["clientIp"]; ok && !matchValue(clientIP, request.ClientIP, request.ClientIP != "") {
		return false
	}
	u, err := url.Parse(request.URL)
	if err != nil {
		return false
	}
	if _, custom := pattern["customMatcher"]; custom {
		return false
	}
	for _, key := range []string{"url", "urlPattern", "urlPath", "urlPathPattern", "urlPathTemplate"} {
		value, exists := pattern[key]
		if !exists {
			continue
		}
		s, _ := value.(string)
		switch key {
		case "url":
			if request.URL != s {
				return false
			}
		case "urlPattern":
			if !matchRegex(s, request.URL) {
				return false
			}
		case "urlPath":
			if u.EscapedPath() != s {
				return false
			}
		case "urlPathPattern":
			if !matchRegex(s, u.EscapedPath()) {
				return false
			}
		case "urlPathTemplate":
			if _, ok := matchPathTemplate(s, u.EscapedPath()); !ok {
				return false
			}
		}
		break
	}
	if host, ok := pattern["host"]; ok {
		absolute, e := url.Parse(request.AbsoluteURL)
		if e != nil {
			return false
		}
		if value, ok := host.(string); ok {
			host = map[string]any{"equalTo": value}
		}
		if !matchValue(host, absolute.Hostname(), true) {
			return false
		}
	}
	if scheme, ok := pattern["scheme"].(string); ok {
		absolute, e := url.Parse(request.AbsoluteURL)
		if e != nil || absolute.Scheme != scheme {
			return false
		}
	}
	if port, ok := pattern["port"]; ok {
		absolute, e := url.Parse(request.AbsoluteURL)
		if e != nil {
			return false
		}
		p := absolute.Port()
		if p == "" {
			p = "80"
			if absolute.Scheme == "https" {
				p = "443"
			}
		}
		if fmt.Sprint(port) != p {
			return false
		}
	}
	if patterns, ok := pattern["headers"].(map[string]any); ok {
		for name, p := range patterns {
			if _, auth := pattern["basicAuthCredentials"]; auth && strings.EqualFold(name, "Authorization") {
				continue
			}
			if !matchMulti(p, request.Headers.Values(name)) {
				return false
			}
		}
	}
	if patterns, ok := pattern["queryParameters"].(map[string]any); ok {
		for name, p := range patterns {
			if !matchMulti(p, u.Query()[name]) {
				return false
			}
		}
	}
	if patterns, ok := pattern["formParameters"].(map[string]any); ok {
		formBody := string(request.Body)
		if !strings.HasPrefix(strings.ToLower(request.Headers.Get("Content-Type")), "application/x-www-form-urlencoded") {
			formBody = ""
		}
		form, e := url.ParseQuery(formBody)
		if e != nil {
			return false
		}
		for name, p := range patterns {
			if !matchMulti(p, form[name]) {
				return false
			}
		}
	}
	if patterns, ok := pattern["cookies"].(map[string]any); ok {
		r := &http.Request{Header: request.Headers}
		for name, p := range patterns {
			var values []string
			for _, cookie := range r.Cookies() {
				if cookie.Name == name {
					values = append(values, cookie.Value)
				}
			}
			if !matchMulti(p, values) {
				return false
			}
		}
	}

	if auth, ok := pattern["basicAuthCredentials"].(map[string]any); ok {
		r := &http.Request{Header: request.Headers}
		user, pass, valid := r.BasicAuth()
		if !valid || user != auth["username"] || pass != auth["password"] {
			return false
		}
	}
	if patterns, ok := pattern["pathParameters"].(map[string]any); ok && pattern["urlPathTemplate"] != nil && pattern["url"] == nil && pattern["urlPattern"] == nil && pattern["urlPath"] == nil && pattern["urlPathPattern"] == nil {
		template, _ := pattern["urlPathTemplate"].(string)
		params, matched := matchPathTemplate(template, u.EscapedPath())
		if !matched {
			return false
		}
		for key, p := range patterns {
			v, present := params[key]
			if !matchValue(p, v, present) {
				return false
			}
		}
	}
	if patterns, ok := pattern["bodyPatterns"].([]any); ok && request.Body != nil {
		for _, p := range patterns {
			if !matchBodyValue(p, request.Body) {
				return false
			}
		}
	}
	if patterns, ok := pattern["multipartPatterns"].([]any); ok && !matchMultipart(patterns, request) {
		return false
	}
	return true
}

func matchPathTemplate(template, path string) (map[string]string, bool) {
	names := []string{}
	var expression strings.Builder
	expression.WriteString("^")
	for len(template) > 0 {
		i := strings.IndexByte(template, '{')
		if i < 0 {
			expression.WriteString(regexp.QuoteMeta(template))
			break
		}
		expression.WriteString(regexp.QuoteMeta(template[:i]))
		template = template[i+1:]
		j := strings.IndexByte(template, '}')
		if j < 0 {
			return nil, false
		}
		names = append(names, template[:j])
		expression.WriteString("([^/]+)")
		template = template[j+1:]
	}
	expression.WriteString("$")
	re, err := regexp.Compile(expression.String())
	if err != nil {
		return nil, false
	}
	values := re.FindStringSubmatch(path)
	if values == nil {
		return nil, false
	}
	out := map[string]string{}
	for i, n := range names {
		out[n] = values[i+1]
	}
	return out, true
}

type compiledMatchRegex struct {
	native   *regexp.Regexp
	fallback *regexp2.Regexp
}

var regexCache = struct {
	sync.RWMutex
	entries map[string]compiledMatchRegex
}{entries: make(map[string]compiledMatchRegex)}

func matchRegex(pattern, value string) bool {
	regexCache.RLock()
	re, exists := regexCache.entries[pattern]
	regexCache.RUnlock()
	if !exists {
		translated := javaRegex(pattern)
		re.native, _ = regexp.Compile("(?s)^(?:" + translated + ")$")
		if re.native == nil {
			re.fallback, _ = regexp2.Compile("\\A(?:"+translated+")\\z", regexp2.Singleline)
		}
		regexCache.Lock()
		if len(regexCache.entries) >= 2048 {
			clear(regexCache.entries)
		}
		regexCache.entries[pattern] = re
		regexCache.Unlock()
	}
	if re.native != nil {
		return re.native.MatchString(value)
	}
	if re.fallback == nil {
		return false
	}
	matched, err := re.fallback.MatchString(value)
	return err == nil && matched
}

func matchMulti(pattern any, values []string) bool {
	p, ok := pattern.(map[string]any)
	if !ok {
		return false
	}
	for _, key := range []string{"includes", "hasExactly"} {
		if raw, exists := p[key]; exists {
			if len(values) == 0 {
				return false
			}
			patterns, ok := raw.([]any)
			if !ok || (key == "hasExactly" && len(patterns) != len(values)) {
				return false
			}
			for _, child := range patterns {
				found := false
				for _, v := range values {
					if matchValue(child, v, true) {
						found = true
						break
					}
				}
				if !found {
					return false
				}
			}
			return true
		}
	}
	if len(values) == 0 {
		return matchValue(p, "", false)
	}
	for _, v := range values {
		if matchValue(p, v, true) {
			return true
		}
	}
	return false
}
func matchValue(pattern any, actual string, present bool) bool {
	p, ok := pattern.(map[string]any)
	if !ok {
		return false
	}
	for _, op := range numberOperators {
		if expected, exists := p[op]; exists {
			if !present {
				return false
			}
			matched, _ := numberPatternMatch(op, expected, actual)
			return matched
		}
	}
	if _, ok := p["absent"].(bool); ok {
		return !present
	}
	if _, ok := p["anything"]; ok {
		return true
	}
	if child, ok := p["not"]; ok {
		return !matchValue(child, actual, present)
	}
	for _, key := range []string{"and", "or"} {
		if children, ok := p[key].([]any); ok {
			result := key == "and"
			for _, child := range children {
				match := matchValue(child, actual, present)
				if key == "and" {
					result = result && match
				} else {
					result = result || match
				}
			}
			return result
		}
	}
	if !present {
		_, negativeRegex := p["doesNotMatch"]
		_, negativeContains := p["doesNotContain"]
		return negativeRegex || negativeContains
	}
	for key, expected := range p {
		text, _ := expected.(string)
		switch key {
		case "equalTo":
			if insensitive, _ := p["caseInsensitive"].(bool); insensitive {
				return javaEqualsIgnoreCase(actual, text)
			}
			return actual == text
		case "matchesPathTemplate":
			_, matched := matchPathTemplate(text, actual)
			return matched
		case "binaryEqualTo":
			data, err := base64.StdEncoding.DecodeString(text)
			return err == nil && bytes.Equal(data, []byte(actual))
		case "contains":
			return strings.Contains(actual, text)
		case "doesNotContain":
			return !strings.Contains(actual, text)
		case "matches":
			return matchRegex(text, actual)
		case "doesNotMatch":
			return !matchRegex(text, actual)
		case "equalToJson":
			var want, got any
			if s, ok := expected.(string); ok {
				if decodeMatcherJSON([]byte(s), &want) != nil {
					return false
				}
			} else {
				want = expected
			}
			if decodeMatcherJSON([]byte(actual), &got) != nil {
				return false
			}
			unordered, _ := p["ignoreArrayOrder"].(bool)
			extras, _ := p["ignoreExtraElements"].(bool)
			return matchJSON(want, got, unordered, extras)
		case "matchesJsonSchema":
			return matchJSONSchema(expected, actual, p)
		case "matchesJsonPath":
			return matchJSONPath(expected, actual)
		case "equalToXml":
			return matchXML(text, actual, p)
		case "matchesXPath":
			return matchXPath(expected, p, actual)
		case "before", "after", "equalToDateTime":
			return matchDate(key, text, actual, p)
		}
	}
	return false
}
func matchJSON(want, got any, unordered, extras bool) bool {
	if s, ok := want.(string); ok {
		switch s {
		case "${json-unit.ignore}", "${json-unit.anything}":
			return true
		case "${json-unit.any-string}":
			_, ok := got.(string)
			return ok
		case "${json-unit.any-number}":
			_, ok := matcherNumber(got)
			return ok
		case "${json-unit.any-boolean}":
			_, ok := got.(bool)
			return ok
		}
		if strings.HasPrefix(s, "${json-unit.regex}") {
			v, ok := got.(string)
			return ok && matchRegex(strings.TrimPrefix(s, "${json-unit.regex}"), v)
		}
	}
	switch expected := want.(type) {
	case map[string]any:
		actual, ok := got.(map[string]any)
		if !ok {
			return false
		}
		if !extras {
			expectedCount, actualCount := len(expected), len(actual)
			for key, value := range expected {
				if marker, ok := value.(string); ok && marker == "${json-unit.ignore-element}" {
					expectedCount--
					if _, present := actual[key]; present {
						actualCount--
					}
				}
			}
			if expectedCount != actualCount {
				return false
			}
		}
		for k, v := range expected {
			a, ok := actual[k]
			if marker, ok := v.(string); ok && marker == "${json-unit.ignore-element}" {
				continue
			}
			if !ok || !matchJSON(v, a, unordered, extras) {
				return false
			}
		}
		return true
	case []any:
		actual, ok := got.([]any)
		if !ok || len(expected) > len(actual) || (!extras && len(expected) != len(actual)) {
			return false
		}
		if !unordered {
			for i, v := range expected {
				if !matchJSON(v, actual[i], unordered, extras) {
					return false
				}
			}
			return true
		}
		used := make([]bool, len(actual))
		var assign func(int) bool
		assign = func(i int) bool {
			if i == len(expected) {
				return true
			}
			for j, v := range actual {
				if !used[j] && matchJSON(expected[i], v, unordered, extras) {
					used[j] = true
					if assign(i + 1) {
						return true
					}
					used[j] = false
				}
			}
			return false
		}
		return assign(0)
	default:
		if n, ok := matcherNumber(want); ok {
			other, valid := matcherNumber(got)
			return valid && n.Cmp(other) == 0
		}
		return fmt.Sprintf("%T", want) == fmt.Sprintf("%T", got) && fmt.Sprint(want) == fmt.Sprint(got)
	}
}
func matchJSONPath(expected any, actual string) bool {
	expression, ok := expected.(string)
	nested := map[string]any(nil)
	if !ok {
		nested, ok = expected.(map[string]any)
		if !ok {
			return false
		}
		expression, _ = nested["expression"].(string)
	}
	var document any
	if json.Unmarshal([]byte(actual), &document) != nil {
		return false
	}
	result, err := evaluateMatcherJSONPath(expression, document)
	if err != nil {
		return false
	}

	if nested == nil {
		if result == nil {
			return false
		}
		switch v := result.(type) {
		case []any:
			return len(v) > 0 || matcherJSONPathDefinite(expression)
		case map[string]any:
			return len(v) > 0 || matcherJSONPathDefinite(expression)
		}
		return true
	}
	if result == nil {
		return matchValue(nested, "", false)
	}
	if array, ok := result.([]any); ok {
		if _, jsonEqual := nested["equalToJson"]; jsonEqual {
			raw, _ := json.Marshal(array)
			return matchValue(nested, string(raw), true)
		}
		if len(array) == 0 {
			// A definite path can select a present empty array. An indefinite
			// path with zero results instead means no nodes were selected.
			if _, absent := nested["absent"].(bool); absent && matcherJSONPathDefinite(expression) {
				return false
			}
			return matchValue(nested, "", false)
		}
		for _, v := range array {
			if v == nil && matchValue(nested, "null", true) {
				return true
			}
			if v != nil && matchValue(nested, matcherJSONValueString(v), true) {
				return true
			}
		}
		return false
	}

	return matchValue(nested, matcherJSONValueString(result), true)
}
func matcherJSONValueString(v any) string {
	if n, ok := v.(jsonPathFloat); ok {
		s := fmt.Sprint(float64(n))
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		return s
	}
	if s, ok := v.(string); ok {
		return s
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

// XPath object syntax may contain only expression/namespace metadata, without
// a nested value matcher. It then has the same predicate semantics as a string.
func xpathHasValueMatcher(pattern map[string]any) bool {
	for key := range pattern {
		if key != "expression" && key != "xPathNamespaces" {
			return true
		}
	}
	return false
}

func matchXPath(expected any, p map[string]any, actual string) bool {
	expression, ok := expected.(string)
	nested := map[string]any(nil)
	if !ok {
		nested, ok = expected.(map[string]any)
		if !ok {
			return false
		}
		expression, _ = nested["expression"].(string)
	}
	doc, err := xmlquery.Parse(strings.NewReader(actual))
	if err != nil {
		return false
	}
	namespaces := map[string]string{}
	if ns, ok := p["xPathNamespaces"].(map[string]any); ok {
		for key, value := range ns {
			if uri, ok := value.(string); ok {
				namespaces[key] = uri
			}
		}
	}
	if nested != nil {
		if ns, ok := nested["xPathNamespaces"].(map[string]any); ok {
			for key, value := range ns {
				if uri, ok := value.(string); ok {
					namespaces[key] = uri
				}
			}
		}
	}
	compiled, err := xpath.CompileWithNS(expression, namespaces)
	if err != nil {
		return false
	}
	evaluation := compiled.Evaluate(xmlquery.CreateXPathNavigator(doc))
	if _, ok := evaluation.(*xpath.NodeIterator); !ok {
		if !xpathHasValueMatcher(nested) {
			// XPath 1.0 boolean conversion applies to scalar predicates too.
			switch value := evaluation.(type) {
			case bool:
				return value
			case float64:
				return value != 0 && !math.IsNaN(value)
			case string:
				return value != ""
			default:
				return false
			}
		}
		return matchValue(nested, fmt.Sprint(evaluation), true)
	}
	nodes := xmlquery.QuerySelectorAll(doc, compiled)
	for _, node := range nodes {
		rendered := node.InnerText()
		if node.Type == xmlquery.ElementNode || node.Type == xmlquery.DocumentNode {
			rendered = strings.TrimPrefix(node.OutputXMLWithOptions(xmlquery.WithOutputSelf(), xmlquery.WithIndentation("  "), xmlquery.WithEmptyTagSupport()), "\n") + "\n"
		}
		if !xpathHasValueMatcher(nested) || matchValue(nested, rendered, true) {
			return true
		}
	}
	return false
}
func matchMultipart(patterns []any, request Request) bool {
	_, params, err := mime.ParseMediaType(request.Headers.Get("Content-Type"))
	if err != nil || params["boundary"] == "" {
		return false
	}
	reader := multipart.NewReader(bytes.NewReader(request.Body), params["boundary"])
	var parts []Request
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return false
		}
		body, err := io.ReadAll(part)
		if err != nil {
			return false
		}
		parts = append(parts, Request{Headers: http.Header(part.Header), Body: body})
	}
	for _, raw := range patterns {
		p, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		mode, _ := p["matchingType"].(string)
		matched := false
		for _, part := range parts {
			good := true
			if filename, ok := p["fileName"].(string); ok && filename != "" {
				_, disposition, err := mime.ParseMediaType(part.Headers.Get("Content-Disposition"))
				if err != nil || disposition["filename"] != filename {
					good = false
				}
			}
			if headers, ok := p["headers"].(map[string]any); ok {
				for k, v := range headers {
					if !matchMulti(v, part.Headers.Values(k)) {
						good = false
					}
				}
			}
			if body, ok := p["bodyPatterns"].([]any); ok {
				for _, v := range body {
					if !matchBodyValue(v, part.Body) {
						good = false
					}
				}
			}
			if mode == "ALL" && !good {
				return false
			}
			matched = matched || good
		}
		if !matched {
			return false
		}
	}
	return true
}

var schemaCache = struct {
	sync.RWMutex
	entries map[string]*jsonschema.Schema
}{entries: make(map[string]*jsonschema.Schema)}

func matchJSONSchema(expected any, actual string, pattern map[string]any) bool {
	var document any
	if text, ok := expected.(string); ok {
		if json.Unmarshal([]byte(text), &document) != nil {
			return false
		}
	} else {
		document = expected
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return false
	}
	version, _ := pattern["schemaVersion"].(string)
	key := version + string(encoded)
	schemaCache.RLock()
	schema, exists := schemaCache.entries[key]
	schemaCache.RUnlock()
	if !exists {
		compiler := jsonschema.NewCompiler()
		switch version {
		case "V4":
			compiler.DefaultDraft(jsonschema.Draft4)
		case "V6":
			compiler.DefaultDraft(jsonschema.Draft6)
		case "V7":
			compiler.DefaultDraft(jsonschema.Draft7)
		case "V201909":
			compiler.DefaultDraft(jsonschema.Draft2019)
		default:
			compiler.DefaultDraft(jsonschema.Draft2020)
		}
		if compiler.AddResource("https://breakmock.invalid/schema", document) != nil {
			return false
		}
		schema, err = compiler.Compile("https://breakmock.invalid/schema")
		if err != nil {
			return false
		}
		schemaCache.Lock()
		if len(schemaCache.entries) >= 256 {
			clear(schemaCache.entries)
		}
		schemaCache.entries[key] = schema
		schemaCache.Unlock()
	}
	var value any
	if json.Unmarshal([]byte(actual), &value) != nil {
		value = actual
	}
	if schema.Validate(value) == nil {
		return true
	}
	switch value.(type) {
	case string, map[string]any, []any:
		return false
	default:
		return schema.Validate(actual) == nil
	}
}

func matchBodyValue(pattern any, body []byte) bool {
	present := len(body) > 0
	if p, ok := pattern.(map[string]any); ok {
		if _, binary := p["binaryEqualTo"]; binary {
			present = true
		}
	}
	return matchValue(pattern, string(body), present)
}

func javaEqualsIgnoreCase(a, b string) bool {
	ar, br := []rune(a), []rune(b)
	if len(ar) != len(br) {
		return false
	}
	for i, x := range ar {
		y := br[i]
		if x == y {
			continue
		}
		ux, uy := unicode.ToUpper(x), unicode.ToUpper(y)
		if ux != uy && unicode.ToLower(ux) != unicode.ToLower(uy) {
			return false
		}
	}
	return true
}
func decodeMatcherJSON(data []byte, value *any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("invalid trailing JSON")
	}
	return nil
}
func matcherNumber(value any) (*big.Rat, bool) {
	switch value.(type) {
	case json.Number, float64, float32, int, int64, uint64:
		n, ok := new(big.Rat).SetString(fmt.Sprint(value))
		return n, ok
	default:
		return nil, false
	}
}
