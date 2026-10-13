// Copyright (C) 2016-2025 Thomas Akehurst
// Copyright 2026 Breaking-IT
// Licensed under the Apache License, Version 2.0. See LICENSE.
// Adapted from WireMock's Diff and PlainTextDiffRenderer.
package breakmock

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/ohler55/ojg/jp"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

type diagnosticLine struct{ expected, actual, message string }

func diagnosticMessage(attribute, actual string, exact bool) string {
	if actual == "" {
		return attribute + " is not present"
	}
	if !exact {
		return attribute + " does not match"
	}
	return ""
}
func diagnosticPattern(p any) (name, expected string) {
	m, _ := p.(map[string]any)
	for _, key := range []string{"equalTo", "equalToJson", "equalToXml", "binaryEqualTo", "contains", "doesNotContain", "matches", "doesNotMatch", "matchesJsonPath", "matchesXPath", "absent", "before", "after", "equalToDateTime", "matchesPathTemplate", "hasExactly", "includes", "and", "or", "not"} {
		if v, ok := m[key]; ok {
			if key == "absent" {
				return key, "(absent)"
			}
			if s, ok := v.(string); ok {
				return key, s
			}
			b, _ := marshalJSON(v)
			return key, string(b)
		}
	}
	return "equalTo", fmt.Sprint(p)
}
func diagnosticOperator(name, equality string) string {
	switch name {
	case "equalTo", "equalToJson", "equalToXml", "binaryEqualTo":
		return equality
	}
	return " [" + name + "] "
}
func (s *Server) unmatchedBody(req Request, width int) []byte {
	mappings := s.Store.Mappings()
	if len(mappings) == 0 {
		return []byte("No response could be served as there are no stub mappings in this WireMock instance.")
	}
	sorted := append([]Mapping(nil), mappings...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, _ := sorted[i]["request"].(map[string]any)
		b, _ := sorted[j]["request"].(map[string]any)
		return RequestMatchDistance(a, req) < RequestMatchDistance(b, req)
	})
	return []byte(s.renderDiagnostic(sorted[0], req, width))
}
func (s *Server) renderDiagnostic(m Mapping, req Request, width int) string {
	pattern, _ := m["request"].(map[string]any)
	order := s.Store.mappingOrder(bmString(m["id"])).child("request")
	lines := []diagnosticLine{}
	add := func(attribute, left, right string, exact bool) {
		lines = append(lines, diagnosticLine{left, right, diagnosticMessage(attribute, right, exact)})
	}
	blank := func() { lines = append(lines, diagnosticLine{}) }
	u, _ := url.Parse(req.AbsoluteURL)
	if u == nil {
		u = &url.URL{}
	}
	if p := pattern["host"]; p != nil {
		n, v := diagnosticPattern(p)
		add("Host", strings.TrimSpace(diagnosticOperator(n, "")+v), u.Hostname(), matchValue(p, u.Hostname(), true))
	}
	if v := pattern["port"]; v != nil {
		port := u.Port()
		if port == "" {
			port = "80"
			if u.Scheme == "https" {
				port = "443"
			}
		}
		add("Port", fmt.Sprint(v), port, fmt.Sprint(v) == port)
	}
	if p := pattern["clientIp"]; p != nil {
		_, v := diagnosticPattern(p)
		add("ClientIp", v, req.ClientIP, matchValue(p, req.ClientIP, true))
	}
	if v := bmString(pattern["scheme"]); v != "" {
		add("Scheme", v, u.Scheme, v == u.Scheme)
	}
	method := bmString(pattern["method"])
	if method == "" {
		method = "ANY"
	}
	add("HTTP method", method, req.Method, method == "ANY" || method == req.Method)
	urlKey, urlExpected, label := "urlPattern", ".*", ""
	for _, k := range []string{"url", "urlPattern", "urlPath", "urlPathPattern", "urlPathTemplate"} {
		if v, ok := pattern[k].(string); ok {
			urlKey, urlExpected = k, v
			label = map[string]string{"url": "", "urlPattern": "[regex] ", "urlPath": "[path] ", "urlPathPattern": "[path regex] ", "urlPathTemplate": "[path template] "}[k]
			break
		}
	}
	urlExact := MatchRequest(map[string]any{urlKey: urlExpected}, req)
	printedURL := label + urlExpected
	if urlExpected == ".*" && pattern["urlPattern"] == nil {
		printedURL = "(always)"
	}
	add("URL", printedURL, req.URL, urlExact)
	if strings.Contains(urlKey, "Pattern") && strings.Contains(urlExpected, "?") && !strings.Contains(urlExpected, "\\?") {
		lines[len(lines)-1].message += ". When using a regex, \"?\" should be \"\\?\""
	}
	if urlExpected != ".*" && !strings.HasPrefix(urlExpected, "/") {
		lines[len(lines)-1].message += ". URLs must start with a /"
	}
	blank()
	headers, _ := pattern["headers"].(map[string]any)
	if auth, ok := pattern["basicAuthCredentials"].(map[string]any); ok {
		headers = cloneMap(headers)
		if headers == nil {
			headers = map[string]any{}
		}
		headers["Authorization"] = map[string]any{"equalTo": "Basic " + base64.StdEncoding.EncodeToString([]byte(bmString(auth["username"])+":"+bmString(auth["password"])))}
	}
	for _, key := range adminKeys(headers, "", order.child("headers")) {
		p := headers[key]
		name, v := diagnosticPattern(p)
		actualName := key
		for _, wireName := range req.HeaderOrder {
			if strings.EqualFold(wireName, key) {
				actualName = wireName
				break
			}
		}
		values := req.Headers.Values(key)
		actual := []string{}
		for _, v := range values {
			actual = append(actual, actualName+": "+v)
		}
		suffix := ""
		if v != "" {
			suffix = ": " + v
		}
		add("Header", actualName+diagnosticOperator(name, "")+suffix, strings.Join(actual, "\n"), matchMulti(p, values))
	}
	if len(headers) > 0 {
		blank()
	}
	parsed, _ := url.ParseRequestURI(req.URL)
	if parsed == nil {
		parsed = &url.URL{}
	}
	if urlKey == "urlPathTemplate" && urlExact {
		params, _ := matchPathTemplate(urlExpected, parsed.EscapedPath())
		expected, _ := pattern["pathParameters"].(map[string]any)
		for _, key := range adminKeys(expected, "", order.child("pathParameters")) {
			p := expected[key]
			name, v := diagnosticPattern(p)
			actual, ok := params[key]
			add("Path parameter", "Path parameter: "+key+diagnosticOperator(name, " = ")+v, actual, matchValue(p, actual, ok))
		}
		if len(expected) > 0 {
			blank()
		}
	}
	for _, section := range []struct {
		key, attribute, prefix string
		values                 url.Values
	}{{"queryParameters", "Query", "Query: ", parsed.Query()}, {"formParameters", "Form data", "Form: ", nil}} {
		if section.key == "formParameters" {
			section.values, _ = url.ParseQuery(string(req.Body))
		}
		expected, _ := pattern[section.key].(map[string]any)
		for _, key := range adminKeys(expected, "", order.child(section.key)) {
			p := expected[key]
			name, v := diagnosticPattern(p)
			values := section.values[key]
			actual := []string{}
			for _, v := range values {
				actual = append(actual, key+": "+v)
			}
			add(section.attribute, section.prefix+key+diagnosticOperator(name, " = ")+v, strings.Join(actual, "\n"), matchMulti(p, values))
		}
		if len(expected) > 0 {
			blank()
		}
	}
	cookies, _ := pattern["cookies"].(map[string]any)
	hr := http.Request{Header: req.Headers}
	for _, key := range adminKeys(cookies, "", order.child("cookies")) {
		p := cookies[key]
		name, v := diagnosticPattern(p)
		c, e := hr.Cookie(key)
		actual := ""
		if e == nil {
			actual = c.Value
		}
		add("Cookie", "Cookie: "+key+diagnosticOperator(name, "=")+v, actual, matchValue(p, actual, e == nil))
	}
	if len(cookies) > 0 {
		blank()
	}
	for _, p := range bmSlice(pattern["bodyPatterns"]) {
		if matchBodyValue(p, req.Body) {
			continue
		}
		name, v := diagnosticPattern(p)
		actual := string(req.Body)
		if name == "equalToJson" {
			v = prettyDiagnosticJSON(v)
			actual = prettyDiagnosticJSON(actual)
		}
		if name == "equalToXml" {
			if text, err := RenderTemplate("{{formatXml request.body}}", Request{Body: []byte(v)}, nil); err == nil {
				v = text
			}
			if text, err := RenderTemplate("{{formatXml request.body}}", Request{Body: []byte(actual)}, nil); err == nil {
				actual = text
			}
		}
		expected := "[" + name + "]\n" + v
		actual = "\n" + actual
		if name == "binaryEqualTo" {
			expected = v
			actual = javaByteArrayIdentity()
		}
		if name == "matchesJsonPath" || name == "matchesXPath" {
			expected = v
			actual = string(req.Body)
		}
		if name == "matchesJsonPath" {
			if outer, ok := p.(map[string]any); ok {
				if nested, ok := outer[name].(map[string]any); ok {
					expr := bmString(nested["expression"])
					op, val := diagnosticPattern(nested)
					expected = expr + " [" + op + "] " + val
					var doc any
					if json.Unmarshal(req.Body, &doc) == nil {
						if path, err := jp.ParseString(expr); err == nil {
							if values := path.Get(doc); len(values) > 0 {
								if path.Normal() {
									actual = matcherJSONValueString(values[0])
								} else {
									actual = matcherJSONValueString(values)
								}
							}
						}
					}
				}
			}
		}

		add("Body", expected, actual, false)
	}
	if scenario := bmString(m["scenarioName"]); scenario != "" && m["requiredScenarioState"] != nil {
		expected := bmString(m["requiredScenarioState"])
		state := s.Store.ScenarioState(scenario)
		add("Scenario", "[Scenario '"+scenario+"' state: "+expected+"]", "[Scenario '"+scenario+"' state: "+state+"]", state == expected)
	}
	if width < 20 || width > 4096 {
		width = 119
	}
	middle := width/2 - 1
	var out strings.Builder
	title := "Request was not matched"
	pad := strings.Repeat(" ", middle-len(title)/2)
	out.WriteString("\n" + pad + title + "\n" + pad + strings.Repeat("=", len(title)) + "\n\n" + strings.Repeat("-", width) + "\n|" + diagnosticPad(" Closest stub", middle) + "|" + diagnosticPad(" Request", middle) + "|\n" + strings.Repeat("-", width) + "\n")
	write := func(l diagnosticLine) {
		left, right := diagnosticWrap(l.expected, width/2-2), diagnosticWrap(l.actual, width/2-2)
		n := max(len(left), len(right))
		for i := 0; i < n; i++ {
			a, b := "", ""
			if i < len(left) {
				a = left[i]
			}
			if i < len(right) {
				b = right[i]
			}
			out.WriteString(diagnosticPad(a, middle+1) + "|")
			message := ""
			if i == 0 {
				message = l.message
			}
			if b != "" {
				out.WriteByte(' ')
				if message != "" {
					out.WriteString(diagnosticPad(b, middle-6) + "<<<<< " + message)
				} else {
					out.WriteString(b)
				}
			} else if message != "" {
				out.WriteString(diagnosticPad(b, middle-5) + "<<<<< " + message)
			}
			out.WriteByte('\n')
		}
	}
	write(diagnosticLine{})
	if name := bmString(m["name"]); name != "" {
		write(diagnosticLine{expected: name})
		write(diagnosticLine{})
	}
	for _, line := range lines {
		write(line)
	}
	write(diagnosticLine{})
	out.WriteString(strings.Repeat("-", width) + "\n")
	return out.String()
}
func diagnosticPad(s string, n int) string {
	length := len(utf16.Encode([]rune(s)))
	if length < n {
		return s + strings.Repeat(" ", n-length)
	}
	return s
}
func diagnosticWrap(s string, width int) []string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	longest := 0
	for _, line := range strings.Split(s, "\n") {
		longest = max(longest, len(utf16.Encode([]rune(line))))
	}
	if longest > width {
		chars := utf16.Encode([]rune(s))
		var out []uint16
		for len(chars) > width {
			if chars[0] == ' ' {
				chars = chars[1:]
				continue
			}
			at := -1
			for i := 0; i <= width && i < len(chars); i++ {
				if chars[i] == ' ' {
					at = i
				}
			}
			if at < 0 {
				at = width
			}
			out = append(out, chars[:at]...)
			out = append(out, '\n')
			if at < len(chars) && chars[at] == ' ' {
				at++
			}
			chars = chars[at:]
		}
		out = append(out, chars...)
		s = string(utf16.Decode(out))
		nonblank := []string{}
		for _, line := range strings.Split(s, "\n") {
			if strings.TrimSpace(line) != "" {
				nonblank = append(nonblank, line)
			}
		}
		s = strings.Join(nonblank, "\n")
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines
}
func diagnosticWidth(r *http.Request) int {
	n, _ := strconv.Atoi(r.Header.Get("X-WireMock-Console-Width"))
	if n == 0 {
		return 119
	}
	return n
}

func prettyDiagnosticJSON(s string) string {
	var b bytes.Buffer
	if err := adminRawJSON(&b, []byte(s), 0); err != nil {
		return s
	}
	return b.String()
}

// javaByteArrayIdentity mirrors how WireMock renders the actual body for a
// binary mismatch: Java's default toString of a fresh byte[], "[B@" followed by
// the array's identity hash in hex. That hash is a per-object JVM value, never
// derived from the request, so it is random here as it is in Java.
func javaByteArrayIdentity() string {
	return "[B@" + strconv.FormatInt(int64(rand.Int32N(math.MaxInt32)+1), 16)
}
