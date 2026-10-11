// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"encoding/base64"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf16"
)

// ValueMatchDistance follows WireMock's string equality, boolean and logical
// distances, together with JSON structural and XML comparison distances.
func ValueMatchDistance(pattern any, actual string, present bool) float64 {
	p, ok := pattern.(map[string]any)
	if !ok {
		return 1
	}
	for _, op := range numberOperators {
		if expected, exists := p[op]; exists {
			if !present {
				return 1
			}
			_, distance := numberPatternMatch(op, expected, actual)
			return distance
		}
	}
	if expected, ok := p["matchesJsonPath"]; ok {
		return jsonPathMatchDistance(expected, actual)
	}
	if expected, ok := p["matchesXPath"]; ok {
		return xpathMatchDistance(expected, p, actual)
	}
	if _, ok := p["equalToXml"]; ok {
		return xmlMatchDistance(p, actual)
	}
	if _, ok := p["equalToJson"]; ok {
		return jsonMatchDistance(p, actual)
	}
	if expected, ok := p["equalTo"]; ok {
		value, valid := expected.(string)
		if !valid || !present {
			return 1
		}
		return normalizedLevenshtein(value, actual)
	}
	if list, ok := p["and"].([]any); ok {
		if len(list) == 0 {
			return 0
		}
		sum := 0.0
		for _, child := range list {
			sum += ValueMatchDistance(child, actual, present)
		}
		return sum / float64(len(list))
	}
	if list, ok := p["or"].([]any); ok {
		best := 1.0
		for _, child := range list {
			best = math.Min(best, ValueMatchDistance(child, actual, present))
		}
		return best
	}
	if expected, ok := p["matchesPathTemplate"].(string); ok {
		if _, matched := matchPathTemplate(expected, actual); matched {
			return 0
		}
		without := regexp.MustCompile(`\{[^}]*\}`).ReplaceAllString(expected, "")
		return normalizedLevenshtein(without, actual)
	}
	if matchValue(p, actual, present) {
		return 0
	}
	return 1
}
func normalizedLevenshtein(expected, actual string) float64 {
	a, b := utf16.Encode([]rune(expected)), utf16.Encode([]rune(actual))
	maximum := max(len(a), len(b))
	if maximum == 0 {
		return 0
	}
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		a = a[1:]
		b = b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		a = a[:len(a)-1]
		b = b[:len(b)-1]
	}
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, x := range a {
		diagonal := previous[0]
		previous[0] = i + 1
		for j, y := range b {
			old := previous[j+1]
			cost := 0
			if x != y {
				cost = 1
			}
			previous[j+1] = min(previous[j]+1, old+1, diagonal+cost)
			diagonal = old
		}
	}
	return float64(previous[len(b)]) / float64(maximum)
}
func multiMatchDistance(pattern any, values []string) float64 {
	p, ok := pattern.(map[string]any)
	if !ok {
		return 1
	}
	for _, key := range []string{"includes", "hasExactly"} {
		if children, ok := p[key].([]any); ok {
			if len(values) == 0 {
				return 1
			}
			sum := 0.0
			for _, child := range children {
				sum += multiMatchDistance(child, values)
			}
			if len(children) > 0 {
				sum /= float64(len(children))
			}
			if key == "hasExactly" {
				count := 0.0
				if len(children) != len(values) {
					count = 1
				}
				return (sum + count) / 2
			}
			return sum
		}
	}
	if len(values) == 0 {
		return ValueMatchDistance(pattern, "", false)
	}
	best := 1.0
	for _, value := range values {
		best = math.Min(best, ValueMatchDistance(pattern, value, true))
	}
	return best
}
func aggregateNamedDistance(patterns any, values func(string) []string) float64 {
	p, ok := patterns.(map[string]any)
	if !ok || len(p) == 0 {
		return 0
	}
	sum := 0.0
	for name, pattern := range p {
		sum += multiMatchDistance(pattern, values(name))
	}
	return sum / float64(len(p))
}

// RequestMatchDistance reproduces WireMock's two-stage weighted aggregation.
// Multipart mismatch distances currently use boolean distance.
func RequestMatchDistance(pattern map[string]any, req Request) float64 {
	u, err := url.Parse(req.URL)
	if err != nil {
		return 1
	}
	absolute, _ := url.Parse(req.AbsoluteURL)
	if absolute == nil {
		absolute = &url.URL{}
	}
	sum := 0.0
	firstExact := true
	add := func(key string, weight float64, distance float64) {
		sum += weight * distance
		if !MatchRequest(map[string]any{key: pattern[key]}, req) {
			firstExact = false
		}
	}
	if _, ok := pattern["scheme"]; ok {
		d := 0.0
		if pattern["scheme"] != absolute.Scheme {
			d = 1
		}
		add("scheme", 3, d)
	}
	if host, ok := pattern["host"]; ok {
		if value, ok := host.(string); ok {
			host = map[string]any{"equalTo": value}
		}
		add("host", 10, ValueMatchDistance(host, absolute.Hostname(), true))
	}
	if _, ok := pattern["port"]; ok {
		d := 0.0
		if !MatchRequest(map[string]any{"port": pattern["port"]}, req) {
			d = 1
		}
		add("port", 10, d)
	}
	if ip, ok := pattern["clientIp"]; ok {
		add("clientIp", 3, ValueMatchDistance(ip, req.ClientIP, req.ClientIP != ""))
	}
	for _, key := range []string{"url", "urlPattern", "urlPath", "urlPathPattern", "urlPathTemplate"} {
		if value, ok := pattern[key]; ok {
			actual := req.URL
			operator := "equalTo"
			if key == "urlPath" || key == "urlPathPattern" || key == "urlPathTemplate" {
				actual = u.EscapedPath()
			}
			if key == "urlPattern" || key == "urlPathPattern" {
				operator = "matches"
			}
			if key == "urlPathTemplate" {
				operator = "matchesPathTemplate"
			}
			add(key, 10, ValueMatchDistance(map[string]any{operator: value}, actual, true))
			break
		}
	}
	if _, ok := pattern["method"]; ok {
		d := 0.0
		if !MatchRequest(map[string]any{"method": pattern["method"]}, req) {
			d = 1
		}
		add("method", 3, d)
	}
	if !firstExact {
		return sum / 39
	}
	if template, ok := pattern["urlPathTemplate"].(string); ok {
		params, _ := matchPathTemplate(template, u.EscapedPath())
		sum += aggregateNamedDistance(pattern["pathParameters"], func(k string) []string {
			if v, ok := params[k]; ok {
				return []string{v}
			}
			return nil
		})
	}
	headers := map[string]any{}
	if originals, ok := pattern["headers"].(map[string]any); ok {
		for k, v := range originals {
			headers[k] = v
		}
	}
	if auth, ok := pattern["basicAuthCredentials"].(map[string]any); ok {
		headers["Authorization"] = map[string]any{"equalTo": "Basic " + base64.StdEncoding.EncodeToString([]byte(bmString(auth["username"])+":"+bmString(auth["password"])))}
	}
	sum += aggregateNamedDistance(headers, req.Headers.Values)
	sum += aggregateNamedDistance(pattern["queryParameters"], func(k string) []string { return u.Query()[k] })
	formBody := string(req.Body)
	if !strings.HasPrefix(strings.ToLower(req.Headers.Get("Content-Type")), "application/x-www-form-urlencoded") {
		formBody = ""
	}
	form, _ := url.ParseQuery(formBody)
	sum += aggregateNamedDistance(pattern["formParameters"], func(k string) []string { return form[k] })
	cookieReq := &http.Request{Header: req.Headers}
	sum += aggregateNamedDistance(pattern["cookies"], func(k string) []string {
		var values []string
		for _, c := range cookieReq.Cookies() {
			if c.Name == k {
				values = append(values, c.Value)
			}
		}
		return values
	})
	if bodies, ok := pattern["bodyPatterns"].([]any); ok && len(bodies) > 0 && req.Body != nil {
		total := 0.0
		for _, body := range bodies {
			present := len(req.Body) > 0
			if p, ok := body.(map[string]any); ok {
				if _, binary := p["binaryEqualTo"]; binary {
					present = true
				}
			}
			total += ValueMatchDistance(body, string(req.Body), present)
		}
		sum += total / float64(len(bodies))
	}
	if multipart, ok := pattern["multipartPatterns"].([]any); ok && !matchMultipart(multipart, req) {
		sum++
	}
	return sum / 46
}
