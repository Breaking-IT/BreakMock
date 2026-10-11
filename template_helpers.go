// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"fmt"
	raymond "github.com/Breaking-IT/BreakMock/internal/handlebars"
	"github.com/dlclark/regexp2"
	"math"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
)

type templateFloat float64

func (v templateFloat) String() string {
	s := strconv.FormatFloat(float64(v), 'f', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}
func (v templateFloat) MarshalJSON() ([]byte, error) { return []byte(v.String()), nil }
func templateUTF16Len(s string) int                  { return len(utf16.Encode([]rune(s))) }
func templateConditional(name string, o *raymond.Options) any {
	a, b := o.Param(0), o.Param(1)
	result := false
	switch name {
	case "eq", "neq":
		result = reflect.DeepEqual(a, b)
		if name == "neq" {
			result = !result
		}
	case "and":
		result = true
		for _, p := range o.Params() {
			result = result && raymond.IsTrue(p)
		}
	case "or":
		for _, p := range o.Params() {
			result = result || raymond.IsTrue(p)
		}
	case "not":
		result = !raymond.IsTrue(a)
	default:
		cmp := 0
		if av, ok := a.(string); ok {
			bv, ok := b.(string)
			if !ok {
				return false
			}
			cmp = strings.Compare(av, bv)
		} else {
			if _, ok := b.(string); ok {
				javaType := "Integer"
				switch a.(type) {
				case int64:
					javaType = "Long"
				case float64, templateFloat:
					javaType = "Double"
				}
				templateJavaPanic(o, "java.lang.ClassCastException: class java.lang.String cannot be cast to class java.lang."+javaType+" (java.lang.String and java.lang."+javaType+" are in module java.base of loader 'bootstrap')")
			}
			av, bv := templateNumber(a, 0), templateNumber(b, 0)
			if av < bv {
				cmp = -1
			} else if av > bv {
				cmp = 1
			}
		}
		switch name {
		case "gt":
			result = cmp > 0
		case "gte":
			result = cmp >= 0
		case "lt":
			result = cmp < 0
		case "lte":
			result = cmp <= 0
		}
	}
	if o.IsBlock() {
		if result {
			return o.Fn()
		}
		return o.Inverse()
	}
	return result
}
func templateExtraString(name string, o *raymond.Options) any {
	s := o.ParamStr(0)
	switch name {
	case "hostname":
		v, e := os.Hostname()
		if e != nil {
			return "localhost"
		}
		return v
	case "systemValue":
		key := o.HashStr("key")
		if key == "" {
			return "[ERROR: The key cannot be empty]"
		}
		if !strings.HasPrefix(strings.ToLower(key), "wiremock") {
			return "[ERROR: Access to " + key + " is denied]"
		}
		if o.HashStr("type") == "PROPERTY" {
			return o.HashProp("default")
		}
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		return o.HashProp("default")
	case "isEven", "isOdd", "stripes":
		even := int64(templateNumber(o.Param(0), 0))%2 == 0
		evenWord, oddWord := "even", "odd"
		if len(o.Params()) > 1 {
			evenWord = o.ParamStr(1)
		}
		if len(o.Params()) > 2 {
			oddWord = o.ParamStr(2)
		}
		switch name {
		case "isEven":
			if even {
				return evenWord
			}
			return ""
		case "isOdd":
			if !even {
				return oddWord
			}
			return ""
		default:
			if even {
				return evenWord
			}
			return oddWord
		}
	case "center", "ljust", "rjust":
		if o.HashProp("size") == nil {
			templateJavaPanic(o, "java.lang.NullPointerException: found 'null', expected 'size'")
		}
		size := int(templateNumber(o.HashProp("size"), float64(templateUTF16Len(s))))
		pad := o.HashStr("pad")
		if pad == "" {
			pad = " "
		}
		count := size - templateUTF16Len(s)
		if count <= 0 {
			return s
		}
		padding := strings.Repeat(pad, (count+len(pad)-1)/len(pad))
		padding = padding[:count]
		if name == "ljust" {
			return s + padding
		}
		if name == "rjust" {
			return padding + s
		}
		left := count / 2
		return padding[:left] + s + padding[left:]
	case "slugify":
		s = strings.ToLower(strings.TrimSpace(s))
		var b strings.Builder
		dash := false
		for _, r := range s {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
				b.WriteRune(r)
				dash = false
			} else if unicode.IsSpace(r) || r == '-' {
				if !dash {
					b.WriteByte('-')
					dash = true
				}
			}
		}
		return b.String()
	case "stringFormat":
		return fmt.Sprintf(strings.ReplaceAll(s, "%n", "\n"), o.Params()[1:]...)
	case "stripTags":
		return regexp.MustCompile(`<[^>]*>`).ReplaceAllString(s, "")
	case "abbreviate":
		n := int(templateNumber(o.Param(1), 0))
		r := []rune(s)
		if len(r) <= n {
			return s
		}
		if n < 4 {
			return ""
		}
		return string(r[:n-3]) + "..."
	case "wordWrap":
		width := int(templateNumber(o.Param(1), 80))
		if width < 1 {
			width = 1
		}
		words := strings.Fields(s)
		lines := []string{}
		line := ""
		for _, word := range words {
			if line != "" && len(line)+1+len(word) > width {
				lines = append(lines, line)
				line = ""
			}
			if line != "" {
				line += " "
			}
			line += word
		}
		if line != "" {
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n")
	case "yesno":
		choices := strings.Split(o.ParamStr(1), ",")
		if len(choices) < 2 {
			choices = []string{"yes", "no", "maybe"}
		}
		if o.Param(0) == nil && len(choices) > 2 {
			return choices[2]
		}
		if raymond.IsTrue(o.Param(0)) {
			return choices[0]
		}
		return choices[1]
	case "dateFormat":
		return templateStringDateFormat(o)
	case "numberFormat":
		return templateNumberFormat(o)
	}
	return ""
}

func templateMathResult(value float64, params ...any) any {
	for _, p := range params {
		switch p.(type) {
		case templateFloat, float64, float32:
			if !math.IsNaN(value) && !math.IsInf(value, 0) {
				return templateFloat(value)
			}
		}
		if strings.Contains(raymond.Str(p), ".") {
			return templateFloat(value)
		}
	}
	return value
}

func templateMatchesRegex(pattern, value string) bool {
	r, e := regexp2.Compile(`\A(?:`+pattern+`)\z`, 0)
	if e != nil {
		return false
	}
	r.MatchTimeout = 100 * time.Millisecond
	v, e := r.MatchString(value)
	return e == nil && v
}

// TemplateExecutionError preserves the diagnostic body emitted for helper errors.
// HTTP handlers should write Error() verbatim with status 500, without a newline.
type TemplateExecutionError struct{ Message, InlineMessage string }

func (e *TemplateExecutionError) Error() string { return e.Message }

type templateJavaFailure struct {
	message  string
	position int
}

func templateJavaPanic(o *raymond.Options, message string) {
	panic(templateJavaFailure{message: message, position: o.ErrorPosition()})
}

func templateStringDateFormat(o *raymond.Options) any {
	if o.Param(0) == nil {
		return o.ParamStr(1)
	}
	var t time.Time
	switch v := o.Param(0).(type) {
	case templateDateValue:
		t = v.Time
	case time.Time:
		t = v
	default:
		name := "String"
		switch v.(type) {
		case int, int64:
			name = "Integer"
		case float64, templateFloat:
			name = "Double"
		case bool:
			name = "Boolean"
		}
		templateJavaPanic(o, "java.lang.IllegalArgumentException: found instance of "+name+" with value '"+o.ParamStr(0)+"', but expected instance of TemporalAccessor")
	}
	location := time.Local
	if tz := o.HashStr("tz"); tz != "" {
		var e error
		location, e = time.LoadLocation(tz)
		if e != nil {
			location = time.UTC
		}
	}
	t = t.In(location)
	format := o.ParamStr(1)
	if format == "" {
		format = o.HashStr("format")
	}
	if format == "" {
		format = "medium"
	}
	locale := o.ParamStr(2)
	if locale == "" {
		locale = o.HashStr("locale")
	}
	if locale == "" {
		locale = templateDefaultLocale()
	}
	return templateLocalizedDate(t, format, o.HashStr("time"), locale)
}
