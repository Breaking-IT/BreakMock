// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"encoding/json"
	"fmt"
	"io"
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
		key, _ := marshalJSON(k)
		v, e := marshalJSON(o.values[k])
		if e != nil {
			return nil, e
		}
		parts = append(parts, string(key)+":"+string(v))
	}
	return []byte("{" + strings.Join(parts, ",") + "}"), nil
}
func parseTemplateJSON(source string) (any, error) {
	d := json.NewDecoder(strings.NewReader(source))
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
		return t, nil
	}
}
func templateJSONMerge(o *raymond.Options) any {
	base, e := parseTemplateJSON(o.ParamStr(0))
	if e != nil {
		return ""
	}
	src := o.ParamStr(1)
	if len(o.Params()) < 2 {
		src = o.Fn()
	}
	other, e := parseTemplateJSON(src)
	if e != nil {
		return ""
	}
	a, ok := base.(*templateObject)
	if !ok {
		return ""
	}
	b, ok := other.(*templateObject)
	if !ok {
		return ""
	}
	mergeTemplateObjects(a, b, o.HashProp("removeNulls") == true)
	out, _ := marshalJSON(a)
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
	src := o.ParamStr(0)
	v, e := parseTemplateJSON(src)
	if e != nil {
		return ""
	}
	if v == nil {
		return src
	}
	p, e := jp.ParseString(o.ParamStr(1))
	if e != nil || len(p) == 0 {
		return ""
	}
	if len(p.Get(v)) == 0 {
		b, _ := marshalJSON(v)
		return string(b)
	}
	if v, e = p.Remove(v); e != nil {
		return ""
	}
	b, _ := marshalJSON(v)
	return string(b)
}

func templateJSONArrayAdd(o *raymond.Options) any {
	root, e := parseTemplateJSON(o.ParamStr(0))
	if e != nil {
		return ""
	}
	target := root
	var path jp.Expr
	if expr := o.HashStr("jsonPath"); expr != "" {
		path, e = jp.ParseString(expr)
		if e != nil {
			return ""
		}
		v := path.Get(root)
		if len(v) != 1 {
			return ""
		}
		target = v[0]
	}
	list, ok := target.([]any)
	if !ok {
		return ""
	}
	src := o.ParamStr(1)
	if len(o.Params()) < 2 {
		src = o.Fn()
	}
	item, e := parseTemplateJSON(src)
	if e != nil {
		return ""
	}
	if flat, ok := item.([]any); ok && o.HashProp("flatten") == true {
		list = append(list, flat...)
	} else {
		list = append(list, item)
	}
	if n := o.HashProp("maxItems"); n != nil {
		max := int(templateNumber(n, 0))
		if max < 0 {
			return ""
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
	b, _ := marshalJSON(root)
	return string(b)
}
