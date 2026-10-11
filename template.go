// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	raymond "github.com/Breaking-IT/BreakMock/internal/handlebars"
	"github.com/ohler55/ojg/jp"
)

var templateCache = struct {
	sync.RWMutex
	entries map[string]*raymond.Template
}{entries: make(map[string]*raymond.Template)}

func cachedTemplate(source string) (*raymond.Template, error) {
	templateCache.RLock()
	t := templateCache.entries[source]
	templateCache.RUnlock()
	if t != nil {
		return t.Clone(), nil
	}
	t, e := raymond.Parse(source)
	if e != nil {
		return nil, e
	}
	templateCache.Lock()
	if len(templateCache.entries) < 1024 && len(source) < 65536 {
		templateCache.entries[source] = t
	}
	templateCache.Unlock()
	return t.Clone(), nil
}

type templateValues []string

func (v templateValues) String() string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}
func (v templateValues) First() string { return v.String() }
func (v templateValues) Last() string {
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}
func (v templateValues) Size() int { return len(v) }

// RenderTemplate renders a response template against WireMock's request model.
// Helpers use request-local state so assignments cannot leak between requests.
func RenderTemplate(source string, request Request, parameters map[string]any) (string, error) {
	if plan := lookupTemplatePlan(source); plan != nil {
		return plan.render(request)
	}
	return renderTemplateFull(source, request, parameters)
}

func renderTemplateFull(source string, request Request, parameters map[string]any) (result string, err error) {
	defer func() {
		if v := recover(); v != nil {
			result = ""
			err = fmt.Errorf("response template: %v", v)
		}
	}()
	u, err := url.Parse(request.AbsoluteURL)
	if err != nil || u == nil || u.Host == "" {
		u, err = url.Parse(request.URL)
	}
	if err != nil {
		return "", err
	}
	query := map[string]any{}
	for k, v := range u.Query() {
		query[k] = templateValues(v)
	}
	headers := templateHeaders{}
	for k, v := range request.Headers {
		headers[k] = templateValues(v)
		headers[strings.ToLower(k)] = templateValues(v)
		headers[http.CanonicalHeaderKey(k)] = templateValues(v)
	}
	cookies := map[string]any{}
	hr := http.Request{Header: request.Headers}
	for _, c := range hr.Cookies() {
		cookies[c.Name] = c.Value
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	pn, _ := strconv.Atoi(port)
	ctx := map[string]any{"parameters": parameters, "request": map[string]any{"method": request.Method, "url": request.URL, "absoluteUrl": request.AbsoluteURL, "path": newTemplatePath(u.Path), "pathSegments": strings.Split(strings.Trim(u.Path, "/"), "/"), "query": query, "headers": headers, "cookies": cookies, "body": string(request.Body), "host": u.Hostname(), "port": pn, "scheme": u.Scheme, "baseUrl": u.Scheme + "://" + u.Host}}
	t, err := cachedTemplate(source)
	if err != nil {
		return "", err
	}
	// Helper names are literal in Handlebars expressions. A substring check is
	// a conservative superset (including text literals), so it cannot omit a used helper.
	add := func(name string, f func(*raymond.Options) any) {
		if strings.Contains(source, name) {
			t.RegisterHelper(name, f)
		}
	}
	add("assign", func(o *raymond.Options) any { ctx[o.ParamStr(0)] = o.Fn(); return "" })
	add("val", func(o *raymond.Options) any {
		v := o.Param(0)
		if v == nil {
			v = o.HashProp("or")
			if v == nil {
				v = o.HashProp("default")
			}
		}
		if n := o.HashStr("assign"); n != "" {
			ctx[n] = v
			return ""
		}
		return v
	})
	add("jsonPath", func(o *raymond.Options) any {
		var data any
		if input, ok := o.Param(0).(string); ok {
			if input == "" {
				return ""
			}
			var e error
			data, e = parseTemplateJSON(input)
			if e != nil {
				return ""
			}
		} else {
			data = o.Param(0)
		}
		p, e := jp.ParseString(o.ParamStr(1))
		if e != nil {
			panic(e)
		}
		v := p.Get(data)
		if len(v) == 0 {
			return o.HashProp("default")
		}
		if len(v) == 1 && p.Normal() {
			return templateJSONValue(v[0])
		}
		return templateJSONArray(v)
	})
	add("xPath", func(o *raymond.Options) any { return templateXPath(o, "") })
	add("soapXPath", func(o *raymond.Options) any {
		return templateXPath(o, "/*[local-name()='Envelope']/*[local-name()='Body']/")
	})
	add("formatXml", templateFormatXML)
	add("truncateDate", templateTruncateDate)

	add("parseJson", func(o *raymond.Options) any {
		src := o.ParamStr(0)
		name := ""
		if o.IsBlock() {
			src = o.Fn()
			name = o.ParamStr(0)
		}
		v, e := parseTemplateJSON(src)
		if e != nil {
			return ""
		}
		if name != "" {
			ctx[name] = v
			return ""
		}
		return v
	})
	add("toJson", func(o *raymond.Options) any { return templatePrettyJSON(o.Param(0), 0) })
	add("jsonMerge", templateJSONMerge)
	add("jsonRemove", templateJSONRemove)
	add("jsonArrayAdd", templateJSONArrayAdd)
	add("formatJson", func(o *raymond.Options) any {
		src := o.ParamStr(0)
		if src == "" {
			src = o.Fn()
		}
		v, e := parseTemplateJSON(src)
		if e != nil {
			return ""
		}
		var b []byte
		if o.HashStr("format") == "compact" {
			b, _ = json.Marshal(v)
		} else {
			return templatePrettyJSON(v, 0)
		}
		return string(b)
	})
	add("randomInt", func(o *raymond.Options) any {
		lo, hi := templateNumber(o.HashProp("lower"), -2147483648), templateNumber(o.HashProp("upper"), 2147483647)
		if hi <= lo {
			panic("randomInt upper must exceed lower")
		}
		return int64(lo) + rand.Int64N(int64(hi-lo))
	})
	add("randomDecimal", func(o *raymond.Options) any {
		lo, hi := templateNumber(o.HashProp("lower"), 0), templateNumber(o.HashProp("upper"), 1)
		return lo + rand.Float64()*(hi-lo)
	})
	add("randomValue", templateRandom)
	add("pickRandom", func(o *raymond.Options) any {
		v := o.Params()
		if len(v) == 1 {
			r := reflect.ValueOf(v[0])
			if r.IsValid() && (r.Kind() == reflect.Slice || r.Kind() == reflect.Array) {
				v = nil
				for i := 0; i < r.Len(); i++ {
					v = append(v, r.Index(i).Interface())
				}
			}
		}
		if len(v) == 0 {
			return ""
		}
		if count := int(templateNumber(o.HashProp("count"), 0)); count > 0 {
			if count > len(v) {
				count = len(v)
			}
			v = append([]any(nil), v...)
			rand.Shuffle(len(v), func(i, j int) { v[i], v[j] = v[j], v[i] })
			return templateList(v[:count])
		}
		return v[rand.IntN(len(v))]
	})
	add("math", func(o *raymond.Options) any {
		a, b := templateNumber(o.Param(0), 0), templateNumber(o.Param(2), 0)
		switch o.ParamStr(1) {
		case "+":
			return a + b
		case "-":
			return a - b
		case "*", "x":
			return a * b
		case "/":
			scale := 0
			left := o.ParamStr(0)
			if pos := strings.IndexByte(left, '.'); pos >= 0 {
				scale = len(left) - pos - 1
			}
			factor := math.Pow10(scale)
			return math.Round(a/b*factor) / factor
		case "%":
			return math.Mod(a, b)
		}
		panic("unknown math operator")
	})
	add("range", func(o *raymond.Options) any {
		a, b := int(templateNumber(o.Param(0), 0)), int(templateNumber(o.Param(1), 0))
		if b-a > 100000 {
			panic("range exceeds limit")
		}
		v := templateList{}
		for i := a; i <= b; i++ {
			v = append(v, i)
		}
		return v
	})
	add("array", func(o *raymond.Options) any { return templateList(o.Params()) })
	add("arrayAdd", func(o *raymond.Options) any {
		v := templateSlice(o.Param(0))
		position := len(v)
		if p := o.HashProp("position"); p != nil && raymond.Str(p) != "end" {
			if raymond.Str(p) == "start" {
				position = 0
			} else {
				position = int(templateNumber(p, 0))
			}
		}
		if position < 0 || position > len(v) {
			panic("arrayAdd position out of range")
		}
		v = append(v, nil)
		copy(v[position+1:], v[position:])
		v[position] = o.Param(1)
		return templateList(v)
	})
	add("arrayRemove", func(o *raymond.Options) any {
		v := templateSlice(o.Param(0))
		position := len(v) - 1
		if p := o.HashProp("position"); p != nil && raymond.Str(p) != "end" {
			if raymond.Str(p) == "start" {
				position = 0
			} else {
				position = int(templateNumber(p, 0))
			}
		}
		if position < 0 || position >= len(v) {
			panic("arrayRemove position out of range")
		}
		return templateList(append(v[:position], v[position+1:]...))
	})

	add("arrayJoin", func(o *raymond.Options) any {
		sep := o.ParamStr(0)
		r := reflect.ValueOf(o.Param(1))
		out := []string{}
		if r.IsValid() && (r.Kind() == reflect.Array || r.Kind() == reflect.Slice) {
			for i := 0; i < r.Len(); i++ {
				out = append(out, raymond.Str(r.Index(i).Interface()))
			}
		}
		return o.HashStr("prefix") + strings.Join(out, sep) + o.HashStr("suffix")
	})
	add("size", func(o *raymond.Options) any {
		v := reflect.ValueOf(o.Param(0))
		if !v.IsValid() {
			return 0
		}
		switch v.Kind() {
		case reflect.Map, reflect.Slice, reflect.Array, reflect.String:
			return v.Len()
		}
		return 0
	})
	add("trim", func(o *raymond.Options) any {
		s := o.ParamStr(0)
		if len(o.Params()) == 0 {
			s = o.Fn()
		}
		return strings.TrimSpace(s)
	})
	add("base64", func(o *raymond.Options) any {
		s := o.ParamStr(0)
		if len(o.Params()) == 0 {
			s = o.Fn()
		}
		if o.HashProp("decode") == true {
			b, e := base64.StdEncoding.DecodeString(s)
			if e != nil {
				panic(e)
			}
			return string(b)
		}
		return base64.StdEncoding.EncodeToString([]byte(s))
	})
	add("urlEncode", func(o *raymond.Options) any {
		s := o.ParamStr(0)
		if len(o.Params()) == 0 {
			s = o.Fn()
		}
		if o.HashProp("decode") == true {
			v, e := url.QueryUnescape(s)
			if e != nil {
				panic(e)
			}
			return v
		}
		return url.QueryEscape(s)
	})
	add("formData", func(o *raymond.Options) any {
		data := map[string]any{}
		for _, part := range strings.Split(o.ParamStr(0), "&") {
			key, val, _ := strings.Cut(part, "=")
			if o.HashProp("urlDecode") == true {
				key, _ = url.QueryUnescape(key)
				val, _ = url.QueryUnescape(val)
			}
			vs, _ := data[key].(templateValues)
			data[key] = append(vs, val)
		}
		if name := o.ParamStr(1); name != "" {
			ctx[name] = data
			return ""
		}
		return data
	})

	add("regexExtract", func(o *raymond.Options) any {
		r, e := regexp.Compile(o.ParamStr(1))
		if e != nil {
			panic(e)
		}
		matches := r.FindAllStringSubmatch(o.ParamStr(0), -1)
		if len(matches) == 0 {
			if d := o.HashProp("default"); d != nil {
				return d
			}
			return "[ERROR: Nothing matched " + o.ParamStr(1) + "]"
		}
		if len(o.Params()) > 2 {
			v := templateValues{}
			for _, m := range matches {
				v = append(v, m[1:]...)
			}
			ctx[o.ParamStr(2)] = v
			return ""
		}
		return matches[0][0]
	})
	add("contains", func(o *raymond.Options) any {
		v := strings.Contains(o.ParamStr(0), o.ParamStr(1))
		if o.IsBlock() {
			b := o.Fn()
			if v {
				return raymond.SafeString(b)
			}
			return raymond.SafeString(o.Inverse())
		}
		return v
	})
	add("matches", func(o *raymond.Options) any {
		v, e := regexp.MatchString("^(?:"+o.ParamStr(1)+")$", o.ParamStr(0))
		if e != nil {
			panic(e)
		}
		if o.IsBlock() {
			b := o.Fn()
			if v {
				return raymond.SafeString(b)
			}
			return raymond.SafeString(o.Inverse())
		}
		return v
	})
	for _, name := range []string{"lower", "upper", "capitalize", "capitalizeFirst", "cut", "replace", "substring", "defaultIfEmpty", "join"} {
		n := name
		add(n, func(o *raymond.Options) any { return templateStringHelper(n, o) })
	}
	add("now", templateDate)
	add("date", templateDate)
	add("parseDate", templateParseDate)
	return t.Exec(ctx)
}
func templateNumber(v any, d float64) float64 {
	if v == nil {
		return d
	}
	n, e := strconv.ParseFloat(raymond.Str(v), 64)
	if e != nil {
		panic(e)
	}
	return n
}
func templateRandom(o *raymond.Options) any {
	n := int(templateNumber(o.HashProp("length"), 36))
	if n < 0 || n > 1000000 {
		panic("invalid randomValue length")
	}
	alphabet := "abcdefghijklmnopqrstuvwxyz0123456789"
	switch o.HashStr("type") {
	case "UUID":
		b := make([]byte, 16)
		for i := range b {
			b[i] = byte(rand.IntN(256))
		}
		b[6] = b[6]&15 | 64
		b[8] = b[8]&63 | 128
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
	case "ALPHABETIC":
		alphabet = "abcdefghijklmnopqrstuvwxyz"
	case "NUMERIC":
		alphabet = "0123456789"
	case "HEXADECIMAL":
		alphabet = "abcdef0123456789"
	case "ALPHANUMERIC_AND_SYMBOLS":
		alphabet = ""
		for i := 33; i < 126; i++ {
			alphabet += string(rune(i))
		}
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rand.IntN(len(alphabet))]
	}
	s := string(b)
	if o.HashProp("uppercase") == true {
		s = strings.ToUpper(s)
	}
	return s
}
func templateDateLayout(s string) string {
	if s == "" {
		return "2006-01-02T15:04:05Z0700"
	}
	return strings.NewReplacer("yyyy", "2006", "SSS", "000", "XXX", "Z07:00", "ZZ", "-0700", "MM", "01", "dd", "02", "HH", "15", "mm", "04", "ss", "05", "'", "").Replace(s)
}
func templateDate(o *raymond.Options) any {
	v := time.Now()
	if t, ok := o.Param(0).(time.Time); ok {
		v = t
	}
	if t, ok := o.Param(0).(templateDateValue); ok {
		v = t.Time
	}
	if z := o.HashStr("timezone"); z != "" && !isTemplateParsedDate(o.Param(0)) {
		loc, e := time.LoadLocation(z)
		if e != nil {
			panic(e)
		}
		v = v.In(loc)
	}
	if offset := o.HashStr("offset"); offset != "" {
		parts := strings.Fields(offset)
		if len(parts) != 2 {
			panic("unsupported date offset")
		}
		n, e := strconv.Atoi(parts[0])
		if e != nil {
			panic(e)
		}
		switch strings.TrimSuffix(parts[1], "s") {
		case "second":
			v = v.Add(time.Duration(n) * time.Second)
		case "minute":
			v = v.Add(time.Duration(n) * time.Minute)
		case "hour":
			v = v.Add(time.Duration(n) * time.Hour)
		case "day":
			v = v.AddDate(0, 0, n)
		case "month":
			v = v.AddDate(0, n, 0)
		case "year":
			v = v.AddDate(n, 0, 0)
		default:
			panic("unsupported date offset")
		}
	}
	f := o.HashStr("format")
	if f == "" && o.HashStr("timezone") == "" {
		v = v.UTC()
	}
	if f == "epoch" {
		return v.UnixMilli()
	}
	if f == "unix" {
		return v.Unix()
	}
	return v.Format(templateDateLayout(f))
}
func templateStringHelper(name string, o *raymond.Options) any {
	s := o.ParamStr(0)
	switch name {
	case "lower":
		return strings.ToLower(s)
	case "upper":
		return strings.ToUpper(s)
	case "capitalize":
		return strings.Title(s)
	case "capitalizeFirst":
		if s != "" {
			r := []rune(s)
			return strings.ToUpper(string(r[0])) + string(r[1:])
		}
	case "cut":
		return strings.ReplaceAll(s, o.ParamStr(1), "")
	case "replace":
		return strings.ReplaceAll(s, o.ParamStr(1), o.ParamStr(2))
	case "substring":
		r := []rune(s)
		a := int(templateNumber(o.Param(1), 0))
		b := int(templateNumber(o.Param(2), float64(len(r))))
		if a < 0 || b < a || b > len(r) {
			panic("invalid substring range")
		}
		return string(r[a:b])
	case "defaultIfEmpty":
		if s == "" {
			return o.Param(1)
		}
	case "join":
		r := reflect.ValueOf(o.Param(0))
		if r.IsValid() && (r.Kind() == reflect.Array || r.Kind() == reflect.Slice) {
			v := []string{}
			for i := 0; i < r.Len(); i++ {
				v = append(v, raymond.Str(r.Index(i).Interface()))
			}
			return strings.Join(v, o.ParamStr(1))
		}
	}
	return s
}
