// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
)

// DecodeDocument preserves jsonBody member order, unlike decoding it into a Go map.
func DecodeDocument(data []byte) (map[string]any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	v, e := decodeValue(d)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, fmt.Errorf("trailing JSON data")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected JSON object")
	}
	return m, nil
}
func decodeValue(d *json.Decoder) (any, error) { return decodeValueContext(d, "") }
func decodeValueContext(d *json.Decoder, context string) (any, error) {
	t, e := d.Token()
	if e != nil {
		return nil, e
	}
	switch t {
	case json.Delim('{'):
		m := map[string]any{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, e
			}
			key := k.(string)
			var v any
			if key == "jsonBody" {
				var raw json.RawMessage
				e = d.Decode(&raw)
				var compact []byte
				if e == nil {
					compact, e = wireJSON(raw)
				}
				v = json.RawMessage(compact)
			} else if context == "body" && key == "data" {
				var raw json.RawMessage
				e = d.Decode(&raw)
				if e == nil {
					if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
						compact, err := wireJSON(raw)
						e = err
						v = json.RawMessage(compact)
					} else {
						e = json.Unmarshal(raw, &v)
					}
				}
			} else {
				v, e = decodeValueContext(d, key)
			}
			if e != nil {
				return nil, e
			}
			m[key] = v
		}
		_, e = d.Token()
		return m, e
	case json.Delim('['):
		a := []any{}
		for d.More() {
			v, e := decodeValue(d)
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
func marshalJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if e := enc.Encode(v); e != nil {
		return nil, e
	}
	out := bytes.TrimSuffix(b.Bytes(), []byte{'\n'})
	if raw, ok := v.(json.RawMessage); ok {
		return append([]byte(nil), raw...), nil
	}
	// Go escapes JS line separators even with EscapeHTML disabled; Jackson does not.
	return out, nil
}

// wireJSON serializes a JSON tree in Jackson's insertion order and decimal form.
// Unlike a Go map, tokens retain object field order and exact decimal precision.
func wireJSON(data []byte) ([]byte, error) {
	data, surrogates := protectUnpairedSurrogates(data)
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var out bytes.Buffer
	if err := wireJSONValue(d, &out); err != nil {
		return nil, err
	}
	result := out.Bytes()
	for placeholder, escape := range surrogates {
		result = bytes.ReplaceAll(result, []byte(placeholder), []byte(escape))
	}
	return result, nil
}
func wireJSONValue(d *json.Decoder, out *bytes.Buffer) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	switch v := t.(type) {
	case json.Delim:
		out.WriteRune(rune(v))
		first := true
		if v == '{' {
			keys := []string{}
			values := map[string][]byte{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return e
				}
				k := key.(string)
				if _, exists := values[k]; !exists {
					keys = append(keys, k)
				}
				var value bytes.Buffer
				if e = wireJSONValue(d, &value); e != nil {
					return e
				}
				values[k] = append([]byte(nil), value.Bytes()...)
			}
			if _, e = d.Token(); e != nil {
				return e
			}
			for i, k := range keys {
				if i > 0 {
					out.WriteByte(',')
				}
				wireJSONString(out, k)
				out.WriteByte(':')
				out.Write(values[k])
			}
			out.WriteByte('}')
		} else if v == '[' {
			for d.More() {
				if !first {
					out.WriteByte(',')
				}
				first = false
				if e = wireJSONValue(d, out); e != nil {
					return e
				}
			}
			if _, e = d.Token(); e != nil {
				return e
			}
			out.WriteByte(']')
		}
	case string:
		wireJSONString(out, v)
	case json.Number:
		n, e := wireJSONNumber(string(v))
		if e != nil {
			return e
		}
		out.WriteString(n)
	case bool:
		if v {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case nil:
		out.WriteString("null")
	}
	return nil
}
func wireJSONString(out *bytes.Buffer, s string) {
	out.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r < 32 {
				fmt.Fprintf(out, `\u%04X`, r)
			} else if r > 0xffff {
				hi, lo := utf16.EncodeRune(r)
				fmt.Fprintf(out, `\u%04X\u%04X`, hi, lo)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
}
func wireJSONNumber(s string) (string, error) {
	negative := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	exponent := 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		var e error
		exponent, e = strconv.Atoi(s[i+1:])
		if e != nil || exponent > 10000 || exponent < -10000 {
			return "", fmt.Errorf("JSON number exponent out of range")
		}
		s = s[:i]
	}
	decimal := strings.IndexByte(s, '.')
	if decimal < 0 {
		decimal = len(s)
	} else {
		s = strings.ReplaceAll(s, ".", "")
	}
	decimal += exponent
	var out string
	if decimal <= 0 {
		out = "0." + strings.Repeat("0", -decimal) + s
	} else if decimal >= len(s) {
		out = s + strings.Repeat("0", decimal-len(s))
	} else {
		out = s[:decimal] + "." + s[decimal:]
	}
	whole, fraction, hasFraction := strings.Cut(out, ".")
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	out = whole
	if hasFraction {
		out += "." + fraction
	}
	if negative && strings.Trim(out, "0.") != "" {
		out = "-" + out
	}
	return out, nil
}

// Go's JSON decoder replaces lone UTF-16 surrogates. Jackson preserves them.
// Protect only genuine JSON escapes; escaped literal backslashes are left alone.
func protectUnpairedSurrogates(data []byte) ([]byte, map[string]string) {
	var out bytes.Buffer
	replacements := map[string]string{}
	inString := false
	prefix := ""
	for i := 0; i < len(data); {
		c := data[i]
		if c == '"' {
			inString = !inString
			out.WriteByte(c)
			i++
			continue
		}
		if inString && c == '\\' && i+1 < len(data) {
			if data[i+1] == 'u' && i+6 <= len(data) {
				code, e := strconv.ParseUint(string(data[i+2:i+6]), 16, 16)
				if e == nil && code >= 0xD800 && code <= 0xDFFF {
					paired := false
					if code <= 0xDBFF && i+12 <= len(data) && string(data[i+6:i+8]) == `\u` {
						low, e := strconv.ParseUint(string(data[i+8:i+12]), 16, 16)
						paired = e == nil && low >= 0xDC00 && low <= 0xDFFF
					}
					if paired {
						out.Write(data[i : i+12])
						i += 12
						continue
					}
					if prefix == "" {
						prefix = "__bm_surrogate_" + newID() + "_"
					}
					key := fmt.Sprintf("%s%d__", prefix, len(replacements))
					replacements[key] = fmt.Sprintf(`\u%04X`, code)
					out.WriteString(key)
					i += 6
					continue
				}
			}
			out.Write(data[i : i+2])
			i += 2
			continue
		}
		out.WriteByte(c)
		i++
	}
	return out.Bytes(), replacements
}
