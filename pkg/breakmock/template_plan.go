// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/aymerick/raymond/ast"
	"github.com/aymerick/raymond/parser"
)

// A plan avoids constructing a reflection-based object graph for straight-line
// interpolation. Any block, unknown field, dynamic helper or stateful helper
// falls back to the full evaluator. Only literal pure helper calls are folded.
type templatePlanPart struct {
	text string
	path []string
}
type templatePlan struct {
	parts    []templatePlanPart
	capacity int
}

var templatePlans = struct {
	sync.RWMutex
	entries map[string]*templatePlan
}{entries: map[string]*templatePlan{}}

func lookupTemplatePlan(source string) *templatePlan {
	templatePlans.RLock()
	plan, known := templatePlans.entries[source]
	templatePlans.RUnlock()
	if known {
		return plan
	}
	plan = compileTemplatePlan(source)
	templatePlans.Lock()
	if len(templatePlans.entries) < 1024 && len(source) < 65536 {
		templatePlans.entries[source] = plan
	}
	templatePlans.Unlock()
	return plan
}
func compileTemplatePlan(source string) *templatePlan {
	if len(source) > 65536 || strings.Contains(source, "{{~") || strings.Contains(source, "~}}") {
		return nil
	}
	program, e := parser.Parse(source)
	if e != nil {
		return nil
	}
	plan := &templatePlan{capacity: len(source)}
	for i, node := range program.Body {
		switch n := node.(type) {
		case *ast.ContentStatement:
			plan.parts = append(plan.parts, templatePlanPart{text: n.Value})
		case *ast.CommentStatement:
			continue
		case *ast.MustacheStatement:
			ex := n.Expression
			path := ex.FieldPath()
			if path == nil || path.Data || path.Depth > 0 || path.Scoped {
				return nil
			}
			parts := append([]string(nil), path.Parts...)
			for i, part := range parts {
				if len(part) >= 2 && part[0] == '[' && part[len(part)-1] == ']' {
					parts[i] = part[1 : len(part)-1]
				}
			}
			if len(ex.Params) == 0 && ex.Hash == nil && supportedTemplatePath(parts) {
				plan.parts = append(plan.parts, templatePlanPart{path: parts})
				continue
			}
			if !pureTemplateHelper(ex.HelperName()) || ex.Hash != nil {
				return nil
			}
			for _, param := range ex.Params {
				switch param.(type) {
				case *ast.StringLiteral, *ast.NumberLiteral, *ast.BooleanLiteral:
				default:
					return nil
				}
			}
			end := len(source)
			if i+1 < len(program.Body) {
				end = program.Body[i+1].Location().Pos
			}
			value, e := renderTemplateFull(source[n.Location().Pos:end], Request{URL: "/"}, nil)
			if e != nil {
				return nil
			}
			plan.parts = append(plan.parts, templatePlanPart{text: value})
		default:
			return nil
		}
	}
	return plan
}
func pureTemplateHelper(name string) bool {
	switch name {
	case "math", "trim", "base64", "urlEncode", "lower", "upper", "capitalize", "capitalizeFirst", "cut", "replace", "substring", "defaultIfEmpty", "size", "matches", "contains":
		return true
	}
	return false
}
func supportedTemplatePath(parts []string) bool {
	if len(parts) < 2 || parts[0] != "request" {
		return false
	}
	switch parts[1] {
	case "method", "url", "absoluteUrl", "body", "host", "port", "scheme", "baseUrl":
		return len(parts) == 2
	case "path", "pathSegments":
		if len(parts) == 2 {
			return true
		}
		if len(parts) == 3 {
			n, e := strconv.Atoi(parts[2])
			// request.path is a map with canonical numeric keys; pathSegments is a slice.
			return e == nil && (parts[1] != "path" || strconv.Itoa(n) == parts[2])
		}
		return false
	case "query", "headers":
		if len(parts) == 3 {
			return true
		}
		if len(parts) != 4 {
			return false
		}
		switch parts[3] {
		case "first", "last", "size":
			return true
		}
		_, e := strconv.Atoi(parts[3])
		return e == nil
	case "cookies":
		return len(parts) == 3
	}
	return false
}
func (p *templatePlan) render(request Request) (string, error) {
	var u *url.URL
	var err error
	if request.AbsoluteURL != "" {
		u, err = url.Parse(request.AbsoluteURL)
	}
	if err != nil || u == nil || u.Host == "" {
		u, err = url.Parse(request.URL)
	}
	if err != nil {
		return "", err
	}
	var query url.Values
	var segments []string
	var cookies map[string]string
	var out strings.Builder
	out.Grow(p.capacity)
	for _, part := range p.parts {
		if part.path == nil {
			out.WriteString(part.text)
			continue
		}
		path := part.path
		value := ""
		switch path[1] {
		case "method":
			value = request.Method
		case "url":
			value = request.URL
		case "absoluteUrl":
			value = ""
		case "body":
			value = string(request.Body)
		case "host":
			value = u.Hostname()
		case "scheme":
			value = u.Scheme
		case "baseUrl":
			value = u.Scheme + "://" + u.Host
		case "port":
			value = u.Port()
			if value != "" {
				n, _ := strconv.Atoi(value)
				value = strconv.Itoa(n)
			}
			if value == "" {
				value = "80"
				if u.Scheme == "https" {
					value = "443"
				}
			}
		case "path", "pathSegments":
			if len(path) == 2 {
				value = u.Path
				break
			}
			if segments == nil {
				segments = strings.Split(strings.Trim(u.Path, "/"), "/")
			}
			if len(path) == 2 {
				value = strings.Join(segments, "")
			} else if n, _ := strconv.Atoi(path[2]); n >= 0 && n < len(segments) {
				value = segments[n]
			}
		case "query":
			if query == nil {
				query = u.Query()
			}
			if values, found := query[path[2]]; found {
				value = templatePlanValues(values, path[3:])
			}
		case "headers":
			for key, values := range request.Headers {
				if strings.EqualFold(key, path[2]) {
					value = templatePlanValues(values, path[3:])
					break
				}
			}
		case "cookies":
			if cookies == nil {
				cookies = map[string]string{}
				r := http.Request{Header: request.Headers}
				for _, c := range r.Cookies() {
					if _, seen := cookies[c.Name]; !seen {
						cookies[c.Name] = c.Value
					}
				}
			}
			value = cookies[path[2]]
		}
		out.WriteString(value)
	}
	return out.String(), nil
}
func templatePlanValues(values []string, path []string) string {
	if len(path) == 1 && path[0] == "size" {
		return strconv.Itoa(len(values))
	}
	if len(values) == 0 {
		return ""
	}
	if len(path) == 0 || path[0] == "first" {
		return values[0]
	}
	if path[0] == "last" {
		return values[len(values)-1]
	}
	n, e := strconv.Atoi(path[0])
	if e == nil && n >= 0 && n < len(values) {
		return values[n]
	}
	return ""
}
