// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	raymond "github.com/Breaking-IT/BreakMock/internal/handlebars"
	"github.com/antchfx/xmlquery"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type templateList []any

func (v templateList) String() string {
	out := make([]string, len(v))
	for i, a := range v {
		out[i] = raymond.Str(a)
	}
	return "[" + strings.Join(out, ", ") + "]"
}

type templateJSONArray []any

func (v templateJSONArray) String() string { return templatePrettyJSON(v, 0) }

type templateJSONObject map[string]any

func (v templateJSONObject) String() string {
	return templatePrettyJSON(v, 0)
}
func templateJSONValue(v any) any {
	switch x := v.(type) {
	case *templateObject:
		copy := *x
		copy.jsonDisplay = true
		return &copy
	case []any:
		return templateJSONArray(x)
	case map[string]any:
		return templateJSONObject(x)
	}
	return v
}

type templateXMLNode struct{ node *xmlquery.Node }

func (v templateXMLNode) String() string {
	if v.node.Type == xmlquery.TextNode || v.node.Type == xmlquery.AttributeNode {
		return v.node.InnerText()
	}
	return templateXMLSerialize(v.node, 0, true) + "\n"
}
func (v templateXMLNode) Text() string { return v.node.InnerText() }
func (v templateXMLNode) Name() string { return v.node.Data }
func (v templateXMLNode) Attributes() map[string]any {
	a := map[string]any{}
	for _, attr := range v.node.Attr {
		a[attr.Name.Local] = attr.Value
	}
	return a
}

type templateXMLValues []templateXMLNode

func (v templateXMLValues) String() string {
	if len(v) == 0 {
		return ""
	}
	return v[0].String()
}

// templatePrettyJSON follows Jackson's default pretty-printer whitespace.
// Map keys use deterministic lexical order; source-object insertion order is not retained.
func templatePrettyJSON(value any, depth int) string {
	if o, ok := value.(templateObject); ok {
		return templatePrettyJSON(&o, depth)
	}
	if o, ok := value.(*templateObject); ok {
		if len(o.order) == 0 {
			return "{ }"
		}
		parts := make([]string, 0, len(o.order))
		for _, k := range o.order {
			key, _ := templateMarshalJSON(k)
			parts = append(parts, strings.Repeat("  ", depth+1)+string(key)+" : "+templatePrettyJSON(o.values[k], depth+1))
		}
		return "{\n" + strings.Join(parts, ",\n") + "\n" + strings.Repeat("  ", depth) + "}"
	}
	r := reflect.ValueOf(value)
	if !r.IsValid() {
		return "null"
	}
	switch r.Kind() {
	case reflect.Map:
		if r.Len() == 0 {
			return "{ }"
		}
		keys := r.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		out := make([]string, len(keys))
		for i, k := range keys {
			key, _ := templateMarshalJSON(k.String())
			out[i] = strings.Repeat("  ", depth+1) + string(key) + " : " + templatePrettyJSON(r.MapIndex(k).Interface(), depth+1)
		}
		return "{\n" + strings.Join(out, ",\n") + "\n" + strings.Repeat("  ", depth) + "}"
	case reflect.Array, reflect.Slice:
		if r.Len() == 0 {
			return "[ ]"
		}
		out := make([]string, r.Len())
		for i := 0; i < r.Len(); i++ {
			out[i] = templatePrettyJSON(r.Index(i).Interface(), depth)
		}
		return "[ " + strings.Join(out, ", ") + " ]"
	default:
		b, e := templateMarshalJSON(value)
		if e != nil {
			panic(e)
		}
		return string(b)
	}
}

type templatePath map[string]any

func newTemplatePath(path string) templatePath {
	v := templatePath{"_path": path}
	for i, part := range strings.Split(strings.Trim(path, "/"), "/") {
		v[strconv.Itoa(i)] = part
	}
	return v
}
func (v templatePath) String() string { return v["_path"].(string) }

type templateHeaders map[string]any

func (v templateHeaders) ResolveTemplateField(name string) (any, bool) {
	a, ok := v[strings.ToLower(name)]
	return a, ok
}

func templateSlice(value any) []any {
	r := reflect.ValueOf(value)
	v := []any{}
	if r.IsValid() && (r.Kind() == reflect.Slice || r.Kind() == reflect.Array) {
		for i := 0; i < r.Len(); i++ {
			v = append(v, r.Index(i).Interface())
		}
	}
	return v
}

// templateSegments is a list whose display value retains the original URL path.
type templateSegments string

func (s templateSegments) String() string { return string(s) }
func (s templateSegments) TemplateIterationValues() []any {
	v := []any{}
	for _, part := range strings.Split(string(s), "/") {
		if part != "" {
			v = append(v, part)
		}
	}
	return v
}
func (s templateSegments) ResolveTemplateField(k string) (any, bool) {
	v := s.TemplateIterationValues()
	if k == "size" {
		return len(v), true
	}
	n, e := strconv.Atoi(k)
	if e == nil && n >= 0 && n < len(v) {
		return v[n], true
	}
	return nil, false
}
