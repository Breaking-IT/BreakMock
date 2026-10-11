// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func matcherDateParse(s, format string) (time.Time, bool, error) {
	if strings.EqualFold(format, "unix") || strings.EqualFold(format, "epoch") {
		n, e := strconv.ParseInt(s, 10, 64)
		if e != nil {
			return time.Time{}, true, e
		}
		if strings.EqualFold(format, "unix") {
			return time.Unix(n, 0).UTC(), true, nil
		}
		return time.UnixMilli(n).UTC(), true, nil
	}
	if format != "" {
		var lastError error
		for _, variant := range matcherDateFormats(format) {
			zoned := false
			layout := templateDateTokens(variant, func(token string, isToken bool) string {
				if !isToken {
					return token
				}
				if strings.ContainsAny(token, "zZXxVO") {
					zoned = true
				}
				if token[0] == 'S' {
					return strings.Repeat("0", len(token))
				}
				if token[0] == 'u' {
					if len(token) == 2 {
						return "06"
					}
					return "2006"
				}
				if token[0] == 'x' {
					switch len(token) {
					case 1:
						return "-07"
					case 2:
						return "-0700"
					default:
						return "-07:00"
					}
				}
				return templateDateLayout(token)
			})
			parsed, err := time.Parse(layout, s)
			if err == nil {
				return parsed, zoned, nil
			}
			lastError = err
		}
		return time.Time{}, false, lastError
	}
	// Java ISO_ZONED_DATE_TIME also permits a bracketed zone ID.
	if i := strings.LastIndex(s, "["); i >= 0 && strings.HasSuffix(s, "]") {
		s = s[:i]
	}
	for _, f := range []string{time.RFC3339Nano, time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC} {
		if t, e := time.Parse(f, s); e == nil {
			return t, true, nil
		}
	}
	for _, f := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02", "2006-01-02 15:04:05"} {
		if t, e := time.Parse(f, s); e == nil {
			return t, false, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("invalid datetime")
}
func truncateMatcherDate(t time.Time, spec string) time.Time {
	y, m, d := t.Date()
	h, min, sec := t.Clock()
	ns := t.Nanosecond()
	switch strings.ToLower(strings.ReplaceAll(spec, "_", " ")) {
	case "first second of minute":
		sec, ns = 0, 0
	case "first minute of hour":
		min, sec, ns = 0, 0, 0
	case "first hour of day":
		h, min, sec, ns = 0, 0, 0, 0
	case "first day of month":
		d, h, min, sec, ns = 1, 0, 0, 0, 0
	case "first day of next month":
		m++
		d, h, min, sec, ns = 1, 0, 0, 0, 0
	case "last day of month":
		m++
		d, h, min, sec, ns = 0, 0, 0, 0, 0
	case "first day of year":
		m, d, h, min, sec, ns = 1, 1, 0, 0, 0, 0
	case "first day of next year":
		y++
		m, d, h, min, sec, ns = 1, 1, 0, 0, 0, 0
	case "last day of year":
		m, d, h, min, sec, ns = 12, 31, 0, 0, 0, 0
	}
	return time.Date(y, m, d, h, min, sec, ns, t.Location())
}

var nowDateExpression = regexp.MustCompile(`(?i)^(?:now\s*)?([+-]?\d+)\s+([a-z]+)$`)

func matchDate(operator, expected, actual string, p map[string]any) bool {
	af, _ := p["actualFormat"].(string)
	a, az, err := matcherDateParse(actual, af)
	if err != nil {
		return false
	}
	if spec, ok := p["truncateActual"].(string); ok && az {
		a = truncateMatcherDate(a, spec)
	}
	e, ez, err := matcherDateParse(expected, "")
	offset := nowDateExpression.FindStringSubmatch(expected)
	if strings.EqualFold(expected, "now") || offset != nil {
		e, ez, err = time.Now(), true, nil
		n := 0
		unit := "seconds"
		if offset != nil {
			n, _ = strconv.Atoi(offset[1])
			unit = offset[2]
		}
		if raw, ok := p["expectedOffset"]; ok {
			n, _ = strconv.Atoi(fmt.Sprint(raw))
			unit, _ = p["expectedOffsetUnit"].(string)
		}
		trunc, _ := p["truncateExpected"].(string)
		last, _ := p["applyTruncationLast"].(bool)
		if !last {
			e = truncateMatcherDate(e, trunc)
		}
		switch strings.TrimSuffix(strings.ToLower(unit), "s") {
		case "second":
			e = e.Add(time.Duration(n) * time.Second)
		case "minute":
			e = e.Add(time.Duration(n) * time.Minute)
		case "hour":
			e = e.Add(time.Duration(n) * time.Hour)
		case "day":
			e = e.AddDate(0, 0, n)
		case "month":
			e = matcherAddMonths(e, n)
		case "year":
			e = matcherAddMonths(e, n*12)
		default:
			return false
		}
		if last {
			e = truncateMatcherDate(e, trunc)
		}
	}
	if err != nil {
		return false
	}
	if !ez {
		a = time.Date(a.Year(), a.Month(), a.Day(), a.Hour(), a.Minute(), a.Second(), a.Nanosecond(), time.UTC)
	} else if !az {
		a = time.Date(a.Year(), a.Month(), a.Day(), a.Hour(), a.Minute(), a.Second(), a.Nanosecond(), time.Local)
	}
	switch operator {
	case "before":
		return a.Before(e)
	case "after":
		return a.After(e)
	default:
		return a.Equal(e)
	}
}
func matcherAddMonths(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m+time.Month(n), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
	if d > last {
		d = last
	}
	return first.AddDate(0, 0, d-1)
}

// Expand DateTimeFormatter optional sections. Quotes protect literal brackets.
func matcherDateFormats(format string) []string {
	quoted := false
	start := -1
	depth := 0
	for i := 0; i < len(format); i++ {
		switch format[i] {
		case '\'':
			quoted = !quoted
		case '[':
			if !quoted {
				if depth == 0 {
					start = i
				}
				depth++
			}
		case ']':
			if !quoted && depth > 0 {
				depth--
				if depth == 0 {
					with := matcherDateFormats(format[:start] + format[start+1:i] + format[i+1:])
					without := matcherDateFormats(format[:start] + format[i+1:])
					return append(with, without...)
				}
			}
		}
	}
	return []string{format}
}
