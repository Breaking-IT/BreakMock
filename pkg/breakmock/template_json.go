// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	raymond "github.com/Breaking-IT/BreakMock/internal/handlebars"
	"github.com/ohler55/ojg/jp"
)

// templateObject retains input object order and implements jp.Keyed.
type templateObject struct {
	order       []string
	values      map[string]any
	jsonDisplay bool
}

func (o templateObject) Keys() []string                            { return o.order }
func (o templateObject) ValueForKey(k string) (any, bool)          { v, ok := o.values[k]; return v, ok }
func (o templateObject) ResolveTemplateField(k string) (any, bool) { return o.ValueForKey(k) }
func (o *templateObject) SetValueForKey(k string, v any) {
	if _, ok := o.values[k]; !ok {
		o.order = append(o.order, k)
	}
	o.values[k] = v
}
func (o *templateObject) RemoveValueForKey(k string) {
	delete(o.values, k)
	for i, key := range o.order {
		if key == k {
			o.order = append(o.order[:i], o.order[i+1:]...)
			break
		}
	}
}
func (o templateObject) String() string {
	if o.jsonDisplay {
		return templatePrettyJSON(&o, 0)
	}
	parts := make([]string, 0, len(o.order))
	for _, k := range o.order {
		v := o.values[k]
		if a, ok := v.([]any); ok {
			v = templateList(a)
		}
		parts = append(parts, k+"="+raymond.Str(v))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
func (o templateObject) MarshalJSON() ([]byte, error) {
	parts := make([]string, 0, len(o.order))
	for _, k := range o.order {
		key, _ := templateMarshalJSON(k)
		v, e := templateMarshalJSON(o.values[k])
		if e != nil {
			return nil, e
		}
		parts = append(parts, string(key)+":"+string(v))
	}
	return []byte("{" + strings.Join(parts, ",") + "}"), nil
}
func parseTemplateJSON(source string) (any, error) {
	d := json.NewDecoder(strings.NewReader(templateSanitizeSurrogates(source)))
	d.UseNumber()
	v, e := decodeTemplateJSON(d)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, fmt.Errorf("trailing JSON data")
	}
	return v, nil
}
func decodeTemplateJSON(d *json.Decoder) (any, error) {
	t, e := d.Token()
	if e != nil {
		return nil, e
	}
	switch t {
	case json.Delim('{'):
		o := &templateObject{values: map[string]any{}}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, e
			}
			v, e := decodeTemplateJSON(d)
			if e != nil {
				return nil, e
			}
			o.SetValueForKey(k.(string), v)
		}
		_, e = d.Token()
		return o, e
	case json.Delim('['):
		a := []any{}
		for d.More() {
			v, e := decodeTemplateJSON(d)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		_, e = d.Token()
		return a, e
	default:
		if number, ok := t.(json.Number); ok {
			if !strings.ContainsAny(string(number), ".eE") {
				if n, e := number.Int64(); e == nil {
					return n, nil
				}
				return number, nil
			}
			if n, e := number.Float64(); e == nil {
				if strings.ContainsAny(string(number), "eE") {
					return json.Number(strconv.FormatFloat(n, 'f', -1, 64)), nil
				}
				return templateFloat(n), nil
			}
		}
		return t, nil
	}
}
func templateJSONMerge(o *raymond.Options) any {
	if _, ok := o.Param(0).(string); !ok {
		return "[ERROR: Base JSON parameter must be a string]"
	}
	base, e := parseTemplateJSON(o.ParamStr(0))
	if e != nil {
		return "[ERROR: Base JSON is not valid JSON ('" + o.ParamStr(0) + "')]"
	}
	a, ok := base.(*templateObject)
	if !ok {
		return "[ERROR: Base JSON is not a JSON object ('" + o.ParamStr(0) + "')]"
	}
	src := o.ParamStr(1)
	if o.IsBlock() {
		src = o.Fn()
	} else if _, ok := o.Param(1).(string); !ok {
		return "[ERROR: JSON to merge must be a string]"
	}
	other, e := parseTemplateJSON(src)
	if e != nil {
		return "[ERROR: JSON to merge is not valid JSON ('" + src + "')]"
	}
	b, ok := other.(*templateObject)
	if !ok {
		return "[ERROR: JSON to merge is not a JSON object ('" + src + "')]"
	}
	mergeTemplateObjects(a, b, o.HashProp("removeNulls") == true)
	out, _ := templateMarshalJSON(a)
	return string(out)
}
func mergeTemplateObjects(a, b *templateObject, removeNulls bool) {
	for _, k := range b.order {
		v := b.values[k]
		if v == nil && removeNulls {
			a.RemoveValueForKey(k)
			continue
		}
		nested, ok := v.(*templateObject)
		if existing, ok2 := a.values[k].(*templateObject); ok && ok2 {
			mergeTemplateObjects(existing, nested, removeNulls)
		} else {
			a.SetValueForKey(k, v)
		}
	}
}
func templateJSONRemove(o *raymond.Options) any {
	if _, ok := o.Param(0).(string); !ok {
		return "[ERROR: Input JSON must be a string]"
	}
	src := o.ParamStr(0)
	if len(o.Params()) != 2 {
		return "[ERROR: A single JSONPath expression parameter must be supplied]"
	}
	if _, ok := o.Param(1).(string); !ok {
		return "[ERROR: JSONPath parameter must be a string]"
	}
	v, e := parseTemplateJSON(src)
	if e != nil {
		return "[ERROR: Input JSON string is not valid JSON ('" + src + "')]"
	}
	if v == nil {
		return src
	}
	p, e := jp.ParseString(templateNormalizeJSONPath(o.ParamStr(1)))
	if o.ParamStr(1) == "" {
		return "[ERROR: JSONPath parameter is not a valid JSONPath expression ('')]"
	}
	if e != nil || len(p) == 0 {
		return src
	}
	if o.ParamStr(1) == "$" {
		return "[ERROR: Delete operation cannot be applied to JSONPath expression ('$')]"
	}
	if len(p.Get(v)) == 0 {
		b, _ := templateMarshalJSON(v)
		return string(b)
	}
	locations := p.Locate(v, 0)
	for _, location := range locations {
		if v, e = location.Remove(v); e != nil {
			return ""
		}
	}
	b, _ := templateMarshalJSON(v)
	return string(b)
}

func templateJSONArrayAdd(o *raymond.Options) any {
	if _, ok := o.Param(0).(string); !ok {
		return "[ERROR: Base JSON must be a string]"
	}
	root, e := parseTemplateJSON(o.ParamStr(0))
	if e != nil {
		return "[ERROR: Base JSON is not valid JSON ('" + o.ParamStr(0) + "')]"
	}
	target := root
	var path jp.Expr
	if raw := o.HashProp("jsonPath"); raw != nil {
		expr, ok := raw.(string)
		if !ok {
			return "[ERROR: jsonPath option must be a string]"
		}
		path, e = jp.ParseString(templateNormalizeJSONPath(expr))
		if e != nil || expr == "" {
			return "[ERROR: jsonPath option is not valid JSONPath expression ('" + expr + "')]"
		}
		v := path.Get(root)
		target = nil
		if len(v) == 1 {
			target = v[0]
		}
	}
	list, ok := target.([]any)
	if !ok {
		detail := "'" + o.ParamStr(0) + "'"
		if o.HashProp("jsonPath") != nil {
			detail = "root: " + detail + ", jsonPath: '" + o.HashStr("jsonPath") + "'"
		}
		return "[ERROR: Target JSON is not a JSON array (" + detail + ")]"
	}
	src := o.ParamStr(1)
	if o.IsBlock() {
		src = o.Fn()
	} else if _, ok := o.Param(1).(string); !ok {
		return "[ERROR: Item-to-add JSON must be a string]"
	}
	item, e := parseTemplateJSON(src)
	if e != nil {
		return "[ERROR: Item-to-add JSON is not valid JSON ('" + src + "')]"
	}
	if f := o.HashProp("flatten"); f != nil {
		if _, ok := f.(bool); !ok {
			return "[ERROR: flatten option must be a boolean]"
		}
	}
	if flat, ok := item.([]any); ok && o.HashProp("flatten") == true {
		list = append(list, flat...)
	} else {
		list = append(list, item)
	}
	if n := o.HashProp("maxItems"); n != nil {
		switch n.(type) {
		case int, int64:
		default:
			return "[ERROR: maxItems option must be an integer]"
		}
		max := int(templateNumber(n, 0))
		if max < 0 {
			return "[ERROR: maxItems option integer must be positive]"
		}
		if len(list) > max {
			list = list[len(list)-max:]
		}
	}
	if path == nil {
		root = list
	} else if e = path.Set(root, list); e != nil {
		return ""
	}
	b, _ := templateMarshalJSON(root)
	return string(b)
}

// Java emits U+2028/U+2029 literally; encoding/json always escapes them.
func templateMarshalJSON(value any) ([]byte, error) {
	b, e := marshalJSON(value)
	if e != nil {
		return nil, e
	}
	var out strings.Builder
	for i := 0; i < len(b); {
		if b[i] == '\\' && i+1 < len(b) {
			if b[i+1] == '\\' {
				out.Write(b[i : i+2])
				i += 2
				continue
			}
			if i+6 <= len(b) && (string(b[i:i+6]) == `\u2028` || string(b[i:i+6]) == `\u2029`) {
				if b[i+5] == '8' {
					out.WriteRune('\u2028')
				} else {
					out.WriteRune('\u2029')
				}
				i += 6
				continue
			}
			if i+6 <= len(b) && b[i+1] == 'u' {
				out.WriteString("\\u" + strings.ToUpper(string(b[i+2:i+6])))
				i += 6
				continue
			}
		}
		out.WriteByte(b[i])
		i++
	}
	return []byte(out.String()), nil
}

// Jackson retains lone UTF-16 code units; its UTF-8 encoder substitutes '?'.
// Apply that substitution before Go's decoder would replace them with U+FFFD.
func templateSanitizeSurrogates(source string) string {
	var out strings.Builder
	for i := 0; i < len(source); {
		if source[i] == '\\' && i+1 < len(source) {
			if source[i+1] == '\\' {
				out.WriteString(source[i : i+2])
				i += 2
				continue
			}
			if source[i+1] == 'u' && i+6 <= len(source) {
				n, e := strconv.ParseUint(source[i+2:i+6], 16, 16)
				if e == nil && n >= 0xd800 && n <= 0xdfff {
					if n <= 0xdbff && i+12 <= len(source) && source[i+6:i+8] == `\u` {
						low, e := strconv.ParseUint(source[i+8:i+12], 16, 16)
						if e == nil && low >= 0xdc00 && low <= 0xdfff {
							out.WriteString(source[i : i+12])
							i += 12
							continue
						}
					}
					out.WriteByte('?')
					i += 6
					continue
				}
			}
		}
		out.WriteByte(source[i])
		i++
	}
	return out.String()
}

func templateJSONPathFunction(data any, expression string) (any, bool) {
	for _, name := range []string{"length", "size", "sum", "min", "max", "avg", "keys"} {
		suffix := "." + name + "()"
		if !strings.HasSuffix(expression, suffix) {
			continue
		}
		p, e := jp.ParseString(strings.TrimSuffix(expression, suffix))
		if e != nil {
			return "", true
		}
		found := p.Get(data)
		if len(found) == 0 {
			return "", true
		}
		v := found[0]
		if name == "keys" {
			if o, ok := v.(*templateObject); ok {
				values := templateJSONArray{}
				for _, k := range o.order {
					values = append(values, k)
				}
				return values, true
			}
			return "", true
		}
		values := templateSlice(v)
		if name == "length" || name == "size" {
			if o, ok := v.(*templateObject); ok {
				return len(o.order), true
			}
			return len(values), true
		}
		if len(values) == 0 {
			return "", true
		}
		number := templateNumber(values[0], 0)
		if name == "sum" || name == "avg" {
			number = 0
		}
		for _, item := range values {
			x := templateNumber(item, 0)
			switch name {
			case "sum", "avg":
				number += x
			case "min":
				if x < number {
					number = x
				}
			case "max":
				if x > number {
					number = x
				}
			}
		}
		if name == "avg" {
			number /= float64(len(values))
		}
		return templateFloat(number), true
	}
	return nil, false
}

func templateOmitNulls(v any) any {
	switch o := v.(type) {
	case templateObject:
		return templateOmitNulls(&o)
	case *templateObject:
		n := &templateObject{values: map[string]any{}}
		for _, k := range o.order {
			if o.values[k] != nil {
				n.SetValueForKey(k, templateOmitNulls(o.values[k]))
			}
		}
		return n
	case []any:
		n := make([]any, len(o))
		for i, x := range o {
			n[i] = templateOmitNulls(x)
		}
		return n
	}
	return v
}

func templateNormalizeJSONPath(s string) string {
	var b strings.Builder
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			b.WriteByte(c)
			if c == '\\' && i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
		}
		if c == '.' && i+1 < len(s) && s[i+1] == '[' {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func templateJSONMapError(value any) string {
	kind, token := "Integer", "VALUE_NUMBER_INT"
	switch v := value.(type) {
	case string:
		return "Cannot construct instance of `java.util.LinkedHashMap` (although at least one Creator exists): no String-argument constructor/factory method to deserialize from String value ('" + v + "')"
	case bool:
		kind = "Boolean"
		token = "VALUE_FALSE"
		if v {
			token = "VALUE_TRUE"
		}
	case float64, templateFloat:
		kind = "Floating-point"
		token = "VALUE_NUMBER_FLOAT"
	}
	return "Cannot deserialize value of type `java.util.LinkedHashMap<java.lang.String,java.lang.Object>` from " + kind + " value (token `JsonToken." + token + "`)"
}
